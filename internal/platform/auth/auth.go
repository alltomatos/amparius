// Package auth implementa a fronteira de autenticação e autorização da API.
//
// O pacote não conhece um provedor OIDC específico. O bootstrap injeta um
// verificador que valida assinatura, issuer e audience do token antes de
// devolver os claims mínimos usados pelo Amparius. Na ausência de qualquer
// dependência obrigatória, a decisão é sempre negar.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

var (
	ErrAuthenticationUnavailable = errors.New("autenticação indisponível")
	ErrInvalidBearer             = errors.New("credencial bearer inválida")
	ErrInvalidOIDCToken          = errors.New("token OIDC inválido")
	ErrInvalidIdentity           = errors.New("identidade OIDC inválida")
	ErrMFARequired               = errors.New("MFA obrigatório")
	ErrScopeForbidden            = errors.New("escopo não autorizado")
	ErrScopeUnavailable          = errors.New("escopo indisponível")
	ErrPermissionDenied          = errors.New("permissão negada")
	ErrContextConflict           = errors.New("conflito de contexto autenticado")
)

// Permission identifica uma capacidade de negócio. Valores desconhecidos
// nunca são autorizados por acidente.
type Permission string

const (
	PermissionIdentityRead     Permission = "identity:read"
	PermissionContextRead      Permission = "context:read"
	PermissionEntitlementRead  Permission = "entitlement:read"
	PermissionEntitlementWrite Permission = "entitlement:write"
	PermissionIdentityWrite    Permission = "identity:write"
	PermissionPartyRead        Permission = "party:read"
	PermissionPartyWrite       Permission = "party:write"
	PermissionCatalogRead      Permission = "catalog:read"
	PermissionCatalogWrite     Permission = "catalog:write"
	PermissionFinanceRead      Permission = "finance:read"
	PermissionFinanceWrite     Permission = "finance:write"
	PermissionFinancePost      Permission = "finance:post"
	PermissionFuneralRead      Permission = "funeral:read"
	PermissionFuneralWrite     Permission = "funeral:write"
)

// OIDCIdentity é o resultado já validado pelo adaptador OIDC. O adaptador é
// responsável por validar criptografia, issuer, audience, expiração e nonce;
// este pacote valida somente os campos de escopo necessários ao SaaS.
type OIDCIdentity struct {
	Issuer    string
	Subject   string
	TenantID  uuid.UUID
	CompanyID uuid.UUID
}

// OIDCVerifier é o contrato para o provedor OIDC escolhido pela implantação.
// Nenhum token bruto deve ser persistido ou registrado pelo adaptador.
type OIDCVerifier interface {
	Verify(context.Context, string) (OIDCIdentity, error)
}

// OIDCVerifierFunc facilita adaptadores pequenos e testes sem criar um
// provedor externo fictício.
type OIDCVerifierFunc func(context.Context, string) (OIDCIdentity, error)

func (f OIDCVerifierFunc) Verify(ctx context.Context, token string) (OIDCIdentity, error) {
	if f == nil {
		return OIDCIdentity{}, ErrAuthenticationUnavailable
	}
	return f(ctx, token)
}

// MFAVerifier confirma que a autenticação OIDC foi elevada por MFA. A política
// é deliberadamente separada do OIDC para permitir TOTP, WebAuthn ou IdP que
// emita amr/acr, sem acoplar o domínio a um fornecedor.
type MFAVerifier interface {
	Verify(context.Context, OIDCIdentity) (bool, error)
}

// MFAVerifierFunc facilita adaptadores pequenos e testes.
type MFAVerifierFunc func(context.Context, OIDCIdentity) (bool, error)

func (f MFAVerifierFunc) Verify(ctx context.Context, identity OIDCIdentity) (bool, error) {
	if f == nil {
		return false, ErrAuthenticationUnavailable
	}
	return f(ctx, identity)
}

// SubjectRecord é a autorização persistida para um subject OIDC. Os papéis
// efetivos vêm do banco da célula, nunca de um campo controlado pelo cliente.
type SubjectRecord struct {
	UserID     uuid.UUID
	Roles      []string
	CompanyIDs []uuid.UUID
	MFAEnabled bool
}

// SubjectResolver resolve o vínculo OIDC -> usuário ativo -> papéis sob RLS.
type SubjectResolver interface {
	ResolveSubject(context.Context, uuid.UUID, string) (SubjectRecord, error)
}

// CompanyResolver é implementado pela identidade da célula. A implementação
// real consulta identity.company sob RLS, usando o tenant já fixado no contexto.
type CompanyResolver interface {
	CompanyBelongsToTenant(context.Context, uuid.UUID, uuid.UUID) (bool, error)
}

// Principal é a identidade segura da requisição. Subject não é exposto em
// logs; ele existe apenas para auditoria/casos de uso que realmente precisem
// do ator.
type Principal struct {
	Issuer           string
	Subject          string
	UserID           uuid.UUID
	TenantID         uuid.UUID
	CompanyID        uuid.UUID
	Roles            []string
	MFAAuthenticated bool
}

type principalContextKey struct{}

// WithPrincipal associa o principal autenticado ao contexto da requisição.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext devolve o principal autenticado ou falha fechado.
func PrincipalFromContext(ctx context.Context) (Principal, error) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok || principal.Subject == "" || principal.TenantID == uuid.Nil || principal.CompanyID == uuid.Nil || !principal.MFAAuthenticated {
		return Principal{}, ErrAuthenticationUnavailable
	}
	return principal, nil
}

// RolePolicy é uma cópia imutável das permissões efetivas por papel.
type RolePolicy struct {
	roles map[string]map[Permission]struct{}
}

// NewRolePolicy valida e copia a política. Papel ou permissão vazios são
// rejeitados para evitar uma regra ampla acidental.
func NewRolePolicy(bindings map[string][]Permission) (RolePolicy, error) {
	if len(bindings) == 0 {
		return RolePolicy{}, errors.New("política RBAC vazia")
	}
	roles := make(map[string]map[Permission]struct{}, len(bindings))
	for role, permissions := range bindings {
		role = strings.TrimSpace(role)
		if role == "" || len(permissions) == 0 {
			return RolePolicy{}, errors.New("papel RBAC inválido")
		}
		allowed := make(map[Permission]struct{}, len(permissions))
		for _, permission := range permissions {
			if strings.TrimSpace(string(permission)) == "" {
				return RolePolicy{}, errors.New("permissão RBAC inválida")
			}
			allowed[permission] = struct{}{}
		}
		roles[role] = allowed
	}
	return RolePolicy{roles: roles}, nil
}

// DefaultRolePolicy é a política inicial documentada do SaaS. Ela é criada a
// cada chamada para não expor estado global mutável.
func DefaultRolePolicy() (RolePolicy, error) {
	all := []Permission{
		PermissionIdentityRead, PermissionIdentityWrite,
		PermissionPartyRead, PermissionPartyWrite,
		PermissionCatalogRead, PermissionCatalogWrite,
		PermissionFinanceRead, PermissionFinanceWrite, PermissionFinancePost,
		PermissionFuneralRead, PermissionFuneralWrite,
	}
	return NewRolePolicy(map[string][]Permission{
		"owner":    append(all, PermissionContextRead, PermissionEntitlementRead, PermissionEntitlementWrite),
		"admin":    append(all, PermissionContextRead, PermissionEntitlementRead, PermissionEntitlementWrite),
		"manager":  {PermissionContextRead, PermissionPartyRead, PermissionPartyWrite, PermissionCatalogRead, PermissionCatalogWrite, PermissionFinanceRead, PermissionFinanceWrite, PermissionFinancePost, PermissionFuneralRead, PermissionFuneralWrite},
		"operator": {PermissionPartyRead, PermissionPartyWrite, PermissionCatalogRead, PermissionFinanceRead, PermissionFinancePost, PermissionFuneralRead, PermissionFuneralWrite},
		"auditor":  {PermissionIdentityRead, PermissionEntitlementRead, PermissionPartyRead, PermissionCatalogRead, PermissionFinanceRead, PermissionFuneralRead},
	})
}

func (p RolePolicy) Allows(roles []string, permission Permission) bool {
	if permission == "" {
		return false
	}
	for _, role := range roles {
		if allowed, ok := p.roles[strings.TrimSpace(role)]; ok {
			if _, allowed := allowed[permission]; allowed {
				return true
			}
		}
	}
	return false
}

// MFAPolicy decide se Service.Authenticate exige um MFAVerifier real. O
// emissor próprio de tenant (ADR 0048 §9/Consequências) adia MFA por decisão
// de produto desta fatia; a plataforma (platform.go) não usa este tipo e
// continua exigindo MFA sempre.
type MFAPolicy int

const (
	// MFAPolicyRequired é o comportamento histórico: MFAVerifier obrigatório e
	// MFAEnabled/Verify sempre avaliados. Valor zero: NewService sem o
	// parâmetro variádico preserva o comportamento anterior.
	MFAPolicyRequired MFAPolicy = iota
	// MFAPolicyDisabled pula MFAEnabled e MFAVerifier.Verify inteiramente;
	// usado pelo emissor de tenant enquanto não há MFA de plataforma neste
	// slice (ADR 0048, fora de escopo de AUTH-01B).
	MFAPolicyDisabled
	// MFAPolicyOptional não é usado por nenhum caminho desta fatia; trata-se
	// como MFAPolicyRequired para nunca afrouxar silenciosamente uma política
	// não implementada.
	MFAPolicyOptional
)

// Service autentica um bearer token e compõe o contexto confiável.
type Service struct {
	oidc      OIDCVerifier
	mfa       MFAVerifier
	subjects  SubjectResolver
	companies CompanyResolver
	policy    RolePolicy
	logger    *slog.Logger
	mfaPolicy MFAPolicy
}

// NewService constrói a autenticação. Todas as dependências são obrigatórias,
// exceto mfa quando mfaPolicy é MFAPolicyDisabled: uma configuração
// incompleta não pode iniciar um caminho permissivo. mfaPolicy é variádico e
// opcional para preservar chamadores existentes; na ausência, o padrão é
// MFAPolicyRequired (comportamento anterior, inalterado).
func NewService(oidc OIDCVerifier, mfa MFAVerifier, subjects SubjectResolver, companies CompanyResolver, policy RolePolicy, logger *slog.Logger, mfaPolicy ...MFAPolicy) (*Service, error) {
	policyValue := MFAPolicyRequired
	if len(mfaPolicy) > 0 {
		policyValue = mfaPolicy[0]
	}
	if policyValue != MFAPolicyDisabled {
		policyValue = MFAPolicyRequired
	}
	if oidc == nil || subjects == nil || companies == nil || len(policy.roles) == 0 {
		return nil, ErrAuthenticationUnavailable
	}
	if policyValue != MFAPolicyDisabled && mfa == nil {
		return nil, ErrAuthenticationUnavailable
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{oidc: oidc, mfa: mfa, subjects: subjects, companies: companies, policy: policy, logger: logger, mfaPolicy: policyValue}, nil
}

// Authenticate valida o valor completo do cabeçalho Authorization.
func (s *Service) Authenticate(ctx context.Context, authorization string) (Principal, error) {
	if s == nil || s.oidc == nil || s.subjects == nil || s.companies == nil || len(s.policy.roles) == 0 {
		return Principal{}, ErrAuthenticationUnavailable
	}
	if s.mfaPolicy != MFAPolicyDisabled && s.mfa == nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	token, err := bearerToken(authorization)
	if err != nil {
		return Principal{}, err
	}
	identity, err := s.oidc.Verify(ctx, token)
	if err != nil {
		return Principal{}, fmt.Errorf("verificar identidade OIDC: %w", err)
	}
	if err := validateIdentity(identity); err != nil {
		return Principal{}, err
	}
	if current, err := db.TenantFromContext(ctx); err == nil && current != identity.TenantID {
		return Principal{}, ErrContextConflict
	}
	if current, err := db.CompanyFromContext(ctx); err == nil && current != identity.CompanyID {
		return Principal{}, ErrContextConflict
	}
	ctxTenant := db.WithTenant(ctx, identity.TenantID)
	record, err := s.subjects.ResolveSubject(ctxTenant, identity.TenantID, identity.Subject)
	if err != nil {
		return Principal{}, fmt.Errorf("resolver vínculo OIDC: %w", err)
	}
	if s.mfaPolicy != MFAPolicyDisabled && !record.MFAEnabled {
		return Principal{}, ErrMFARequired
	}
	if !record.allowsCompany(identity.CompanyID) {
		return Principal{}, ErrScopeForbidden
	}
	if s.mfaPolicy != MFAPolicyDisabled {
		mfaOK, err := s.mfa.Verify(ctx, identity)
		if err != nil {
			return Principal{}, fmt.Errorf("verificar MFA: %w", err)
		}
		if !mfaOK {
			return Principal{}, ErrMFARequired
		}
	}

	belongs, err := s.companies.CompanyBelongsToTenant(ctxTenant, identity.TenantID, identity.CompanyID)
	if err != nil {
		return Principal{}, fmt.Errorf("resolver empresa autenticada: %w", err)
	}
	if !belongs {
		return Principal{}, ErrScopeForbidden
	}

	return Principal{
		Issuer: identity.Issuer, Subject: identity.Subject,
		UserID:   record.UserID,
		TenantID: identity.TenantID, CompanyID: identity.CompanyID,
		Roles: append([]string(nil), record.Roles...), MFAAuthenticated: true,
	}, nil
}

func (r SubjectRecord) allowsCompany(companyID uuid.UUID) bool {
	for _, allowed := range r.CompanyIDs {
		if allowed == companyID {
			return true
		}
	}
	return false
}

// Allows aplica o RBAC deny-by-default para um principal autenticado.
func (s *Service) Allows(principal Principal, permission Permission) bool {
	if s == nil || !principal.MFAAuthenticated || principal.TenantID == uuid.Nil || principal.CompanyID == uuid.Nil {
		return false
	}
	return s.policy.Allows(principal.Roles, permission)
}

func validateIdentity(identity OIDCIdentity) error {
	if strings.TrimSpace(identity.Issuer) == "" || strings.TrimSpace(identity.Subject) == "" || len(identity.Subject) > 255 || identity.TenantID == uuid.Nil || identity.CompanyID == uuid.Nil {
		return ErrInvalidIdentity
	}
	return nil
}

func bearerToken(value string) (string, error) {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" || len(parts[1]) > 8192 {
		return "", ErrInvalidBearer
	}
	return parts[1], nil
}
