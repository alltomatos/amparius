package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

const (
	refreshTokenTTL       = 30 * 24 * time.Hour
	bootstrapTokenTTL     = 72 * time.Hour
	recoveryTokenTTL      = 2 * time.Hour
	refreshIdempotencyTTL = 10 * time.Second
	loginRateLimitWindow  = 15 * time.Minute
	// loginRateLimitMax é defesa adicional em memória (ver ratelimit.go); o
	// contador autoritativo contra força bruta é failed_attempts/locked_until
	// em identity.app_user, que nunca falha aberto.
	loginRateLimitMax = 20
)

var (
	// ErrInvalidCredentials cobre e-mail inexistente, senha incorreta e
	// resolução de tenant ambígua sem candidato único — nunca distinguidos
	// na resposta (ADR 0048 §5, contrato auth-login-http-v1).
	ErrInvalidCredentials   = errors.New("credenciais inválidas")
	ErrAccountLocked        = errors.New("conta bloqueada temporariamente")
	ErrAccountPendingAuth   = errors.New("conta pendente de ativação")
	ErrCompanyNotAllowed    = errors.New("empresa não vinculada ao usuário")
	ErrRefreshInvalid       = errors.New("refresh token inválido")
	ErrRefreshReuseDetected = errors.New("reuso de refresh token detectado")
)

// AccessTokenIssuer é satisfeito por *platformauth.TenantIssuer. Roles entra
// no token só como claim informativo (contrato); a autorização de cada
// requisição sempre revalida papéis no banco via ResolveSubject.
type AccessTokenIssuer interface {
	Issue(ctx context.Context, claims platformauth.TenantAccessClaims) (string, time.Duration, error)
}

// SessionResult é a resposta comum a login, refresh e bootstrap/recovery
// complete (contrato auth-login-http-v1).
type SessionResult struct {
	AccessToken           string
	AccessTokenExpiresIn  int
	RefreshToken          string
	RefreshTokenExpiresIn int
	TokenType             string
	UserID                uuid.UUID
	Roles                 []string
	CompanyID             uuid.UUID
}

// LoginService orquestra a autenticação de tenant: resolução de candidato,
// bloqueio, emissão/rotação de tokens e auditoria sem PII (ADR 0048).
type LoginService struct {
	repo        *Repository
	issuer      AccessTokenIssuer
	rate        *rateLimiter
	idempotency *idempotencyCache
}

// NewLoginService constrói o serviço com os limites padrão da fatia.
func NewLoginService(repo *Repository, issuer AccessTokenIssuer) *LoginService {
	return &LoginService{
		repo:        repo,
		issuer:      issuer,
		rate:        newRateLimiter(loginRateLimitWindow, loginRateLimitMax),
		idempotency: newIdempotencyCache(refreshIdempotencyTTL),
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func companyAllowed(allowed []uuid.UUID, companyID uuid.UUID) bool {
	for _, id := range allowed {
		if id == companyID {
			return true
		}
	}
	return false
}

// Login resolve o(s) tenant(s) do e-mail, verifica a senha contra cada
// candidato (decoy quando não há nenhum, para igualar custo de CPU) e só
// prossegue com exatamente um tenant resolvido pela senha correta.
func (s *LoginService) Login(ctx context.Context, email, password string, companyID uuid.UUID, clientIP string) (SessionResult, error) {
	if !s.rate.Allow(clientIP + "|" + normalizeEmail(email)) {
		return SessionResult{}, ErrInvalidCredentials
	}

	candidates, err := s.repo.ResolveLoginCandidates(ctx, email)
	if err != nil {
		return SessionResult{}, err
	}
	if len(candidates) == 0 {
		_, _, _ = platformauth.VerifyPassword(platformauth.DecoyHash(), platformauth.PasswordAlgoArgon2idV1, password)
		return SessionResult{}, ErrInvalidCredentials
	}

	matchIdx := -1
	var rehash *PasswordRehash
	for i, candidate := range candidates {
		hash, algo := candidate.PasswordHash, candidate.PasswordAlgo
		if hash == "" {
			hash, algo = platformauth.DecoyHash(), platformauth.PasswordAlgoArgon2idV1
		}
		ok, needsRehash, verr := platformauth.VerifyPassword(hash, algo, password)
		if verr != nil || !ok {
			continue
		}
		matchIdx = i
		if needsRehash {
			if newHash, newAlgo, hashErr := platformauth.HashPassword(password); hashErr == nil {
				rehash = &PasswordRehash{Hash: newHash, Algo: newAlgo}
			}
		}
		break
	}

	if len(candidates) == 1 {
		matched := candidates[0]
		ctxTenant := db.WithTenant(ctx, matched.TenantID)
		if matchIdx == -1 {
			_ = s.repo.RecordLoginFailure(ctxTenant, matched.UserID)
			_ = s.repo.WriteLoginAudit(ctxTenant, matched.UserID, LoginAuditInvalidCredentials)
			return SessionResult{}, ErrInvalidCredentials
		}
		return s.afterPasswordMatch(ctxTenant, matched, companyID, rehash)
	}

	// N > 1 candidatos: sem um match, a tentativa não tem um único tenant a
	// escopar — nenhuma linha de auditoria é gravada (ADR 0048 §5).
	if matchIdx == -1 {
		return SessionResult{}, ErrInvalidCredentials
	}
	matched := candidates[matchIdx]
	ctxTenant := db.WithTenant(ctx, matched.TenantID)
	return s.afterPasswordMatch(ctxTenant, matched, companyID, rehash)
}

func (s *LoginService) afterPasswordMatch(ctxTenant context.Context, matched LoginCandidate, companyID uuid.UUID, rehash *PasswordRehash) (SessionResult, error) {
	if matched.Status == "pending_auth" {
		_ = s.repo.WriteLoginAudit(ctxTenant, matched.UserID, LoginAuditPendingAuth)
		return SessionResult{}, ErrAccountPendingAuth
	}
	if matched.Locked() {
		_ = s.repo.WriteLoginAudit(ctxTenant, matched.UserID, LoginAuditLocked)
		return SessionResult{}, ErrAccountLocked
	}

	subject, err := s.repo.ResolveSubject(ctxTenant, matched.TenantID, matched.UserID.String())
	if err != nil {
		return SessionResult{}, err
	}
	if !companyAllowed(subject.CompanyIDs, companyID) {
		return SessionResult{}, ErrCompanyNotAllowed
	}

	if err := s.repo.RecordLoginSuccess(ctxTenant, matched.UserID, rehash); err != nil {
		return SessionResult{}, err
	}
	if err := s.repo.WriteLoginAudit(ctxTenant, matched.UserID, LoginAuditSuccess); err != nil {
		return SessionResult{}, err
	}

	return s.issueSession(ctxTenant, matched.TenantID, matched.UserID, companyID, subject.Roles)
}

func (s *LoginService) issueSession(ctxTenant context.Context, tenantID, userID, companyID uuid.UUID, roles []string) (SessionResult, error) {
	accessToken, accessTTL, err := s.issuer.Issue(ctxTenant, platformauth.TenantAccessClaims{
		UserID: userID, TenantID: tenantID, CompanyID: companyID, Roles: roles,
	})
	if err != nil {
		return SessionResult{}, fmt.Errorf("%w: %v", platformauth.ErrAuthenticationUnavailable, err)
	}
	refreshToken, err := s.repo.IssueRefreshToken(ctxTenant, userID, companyID, uuid.New(), refreshTokenTTL)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{
		AccessToken:           accessToken,
		AccessTokenExpiresIn:  int(accessTTL.Seconds()),
		RefreshToken:          refreshToken,
		RefreshTokenExpiresIn: int(refreshTokenTTL.Seconds()),
		TokenType:             "Bearer",
		UserID:                userID,
		Roles:                 roles,
		CompanyID:             companyID,
	}, nil
}

// Refresh resolve o token cru, detecta reuso (revogando a família inteira) e
// rotaciona para um novo par. Sem companyID, mantém a empresa gravada no
// token anterior; com companyID diferente, valida contra identity.user_company
// antes de emitir.
func (s *LoginService) Refresh(ctx context.Context, rawToken string, companyID *uuid.UUID, idempotencyKey string) (SessionResult, error) {
	tokenHash := hashOpaqueToken(rawToken)
	if cached, ok := s.idempotency.Get(idempotencyKey, tokenHash); ok {
		return cached, nil
	}

	record, err := s.repo.ResolveRefreshToken(ctx, rawToken)
	if err != nil {
		return SessionResult{}, ErrRefreshInvalid
	}
	if record.Expired() || record.Revoked() {
		return SessionResult{}, ErrRefreshInvalid
	}

	ctxTenant := db.WithTenant(ctx, record.TenantID)

	if record.Reused() {
		_ = s.repo.RevokeRefreshFamily(ctxTenant, record.FamilyID, "refresh_reuse_detected")
		_ = s.repo.WriteLoginAudit(ctxTenant, record.UserID, LoginAuditRefreshReuseDetect)
		return SessionResult{}, ErrRefreshReuseDetected
	}

	subject, err := s.repo.ResolveSubject(ctxTenant, record.TenantID, record.UserID.String())
	if err != nil {
		return SessionResult{}, err
	}

	finalCompanyID := record.CompanyID
	if companyID != nil && *companyID != record.CompanyID {
		if !companyAllowed(subject.CompanyIDs, *companyID) {
			return SessionResult{}, ErrCompanyNotAllowed
		}
		finalCompanyID = *companyID
	}

	rawNext, err := s.repo.RotateRefreshToken(ctxTenant, record, finalCompanyID, refreshTokenTTL)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenAlreadyRotated) {
			// Corrida perdida contra outra rotação concorrente do mesmo
			// token: trata como reuso, já que a vencedora já rotacionou.
			_ = s.repo.RevokeRefreshFamily(ctxTenant, record.FamilyID, "refresh_reuse_detected")
			_ = s.repo.WriteLoginAudit(ctxTenant, record.UserID, LoginAuditRefreshReuseDetect)
			return SessionResult{}, ErrRefreshReuseDetected
		}
		return SessionResult{}, err
	}

	accessToken, accessTTL, err := s.issuer.Issue(ctxTenant, platformauth.TenantAccessClaims{
		UserID: record.UserID, TenantID: record.TenantID, CompanyID: finalCompanyID, Roles: subject.Roles,
	})
	if err != nil {
		return SessionResult{}, fmt.Errorf("%w: %v", platformauth.ErrAuthenticationUnavailable, err)
	}

	result := SessionResult{
		AccessToken:           accessToken,
		AccessTokenExpiresIn:  int(accessTTL.Seconds()),
		RefreshToken:          rawNext,
		RefreshTokenExpiresIn: int(refreshTokenTTL.Seconds()),
		TokenType:             "Bearer",
		UserID:                record.UserID,
		Roles:                 subject.Roles,
		CompanyID:             finalCompanyID,
	}
	if idempotencyKey != "" {
		s.idempotency.Put(idempotencyKey, tokenHash, result)
	}
	return result, nil
}

// Logout revoga a família do refresh token informado. Token desconhecido,
// já revogado ou pertencente a outro usuário também responde com sucesso sem
// revogar nada (logout é idempotente por natureza e authenticatedUserID
// impede que um access token revogue a sessão de outro usuário apenas por
// conhecer o valor de um refresh token).
func (s *LoginService) Logout(ctx context.Context, authenticatedUserID uuid.UUID, rawRefreshToken string) error {
	record, err := s.repo.ResolveRefreshToken(ctx, rawRefreshToken)
	if err != nil {
		return nil
	}
	if record.UserID != authenticatedUserID {
		return nil
	}
	ctxTenant := db.WithTenant(ctx, record.TenantID)
	if record.Revoked() {
		return nil
	}
	if err := s.repo.RevokeRefreshFamily(ctxTenant, record.FamilyID, "logout"); err != nil {
		return err
	}
	return s.repo.WriteLoginAudit(ctxTenant, record.UserID, LoginAuditLogout)
}

// Revoke exige a senha atual do usuário já autenticado e revoga todas as
// famílias de refresh token (todas as sessões, todos os dispositivos). ctx
// precisa carregar o tenant já resolvido pelo middleware de autenticação.
func (s *LoginService) Revoke(ctx context.Context, userID uuid.UUID, password string) error {
	hash, algo, err := s.repo.PasswordHashForUser(ctx, userID)
	if err != nil {
		return err
	}
	if hash == "" {
		hash, algo = platformauth.DecoyHash(), platformauth.PasswordAlgoArgon2idV1
	}
	ok, _, verr := platformauth.VerifyPassword(hash, algo, password)
	if verr != nil || !ok {
		return ErrInvalidCredentials
	}
	if err := s.repo.RevokeAllRefreshTokens(ctx, userID, "revoke"); err != nil {
		return err
	}
	return s.repo.WriteLoginAudit(ctx, userID, LoginAuditRevoke)
}

// CompleteBootstrap consome o token de bootstrap/convite, ativa a senha e
// devolve uma sessão (login automático), igual ao corpo de POST /v1/auth/login.
func (s *LoginService) CompleteBootstrap(ctx context.Context, rawToken, password string, companyID uuid.UUID) (SessionResult, error) {
	return s.completeCredential(ctx, rawToken, "bootstrap", password, companyID)
}

// CompleteRecovery tem a mesma semântica de CompleteBootstrap, mas não altera
// o status do usuário (já era active).
func (s *LoginService) CompleteRecovery(ctx context.Context, rawToken, password string, companyID uuid.UUID) (SessionResult, error) {
	return s.completeCredential(ctx, rawToken, "recovery", password, companyID)
}

func (s *LoginService) completeCredential(ctx context.Context, rawToken, purpose, password string, companyID uuid.UUID) (SessionResult, error) {
	hash, algo, err := platformauth.HashPassword(password)
	if err != nil {
		return SessionResult{}, err
	}
	tenantID, userID, err := s.repo.CompleteCredential(ctx, rawToken, purpose, hash, algo)
	if err != nil {
		return SessionResult{}, err
	}
	ctxTenant := db.WithTenant(ctx, tenantID)
	subject, err := s.repo.ResolveSubject(ctxTenant, tenantID, userID.String())
	if err != nil {
		return SessionResult{}, err
	}
	if !companyAllowed(subject.CompanyIDs, companyID) {
		return SessionResult{}, ErrCompanyNotAllowed
	}
	if err := s.repo.WriteLoginAudit(ctxTenant, userID, LoginAuditSuccess); err != nil {
		return SessionResult{}, err
	}
	return s.issueSession(ctxTenant, tenantID, userID, companyID, subject.Roles)
}

// Invite cria o usuário convidado e emite o token de bootstrap cru, devolvido
// uma única vez ao chamador (POST /v1/auth/invite). ctx precisa carregar o
// tenant do convidante.
func (s *LoginService) Invite(ctx context.Context, email, name string, roleCodes []string, companyIDs []uuid.UUID) (userID uuid.UUID, bootstrapToken string, err error) {
	userID, err = s.repo.CreateInvite(ctx, email, name, roleCodes, companyIDs)
	if err != nil {
		return uuid.Nil, "", err
	}
	bootstrapToken, _, err = s.repo.IssueCredentialToken(ctx, userID, "bootstrap", bootstrapTokenTTL)
	if err != nil {
		return uuid.Nil, "", err
	}
	return userID, bootstrapToken, nil
}
