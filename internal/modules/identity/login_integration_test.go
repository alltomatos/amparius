package identity_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/identity"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// loginFixture agrupa as dependências dos testes de integração do AUTH-01B:
// o pool de aplicação (amparius_app, sob RLS, igual a produção) e um pool
// administrativo (dono das tabelas, sem RLS) usado só para fixtures e
// cleanup — nunca para exercitar o caminho de login em si.
type loginFixture struct {
	ctx       context.Context
	database  *db.DB
	adminPool *pgxpool.Pool
	repo      *identity.Repository
	service   *identity.LoginService
	tenantIDs []uuid.UUID
	password  string
}

func newLoginFixture(t *testing.T) *loginFixture {
	t.Helper()
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_ADMIN_DSN não definido; fixtures exigem cleanup administrativo")
	}

	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	if err := adminPool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	repo, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}

	keys, err := platformauth.NewEnvKeyProvider(testSigningSeed(t), "kid-test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := platformauth.NewTenantIssuer(keys, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	f := &loginFixture{ctx: ctx, database: database, adminPool: adminPool, repo: repo, service: identity.NewLoginService(repo, issuer)}
	t.Cleanup(func() { f.cleanup(t) })
	return f
}

func testSigningSeed(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(priv.Seed())
}

// onboard cria um tenant/empresa/owner sintéticos (pending_auth, sem senha)
// e registra o tenant para cleanup administrativo ao final do teste.
func (f *loginFixture) onboard(t *testing.T, key string) identity.OnboardingResult {
	t.Helper()
	result, err := f.repo.Onboard(f.ctx, identity.DevelopmentRequest(key))
	if err != nil {
		t.Fatal(err)
	}
	f.tenantIDs = append(f.tenantIDs, result.TenantID)
	return result
}

// activate consome um token de bootstrap para o owner recém-criado por
// onboard, ativando a senha informada e devolvendo a primeira sessão —
// mesmo caminho de POST /v1/auth/bootstrap/complete.
func (f *loginFixture) activate(t *testing.T, result identity.OnboardingResult, password string) identity.SessionResult {
	t.Helper()
	ctxTenant := db.WithTenant(f.ctx, result.TenantID)
	token, _, err := f.repo.IssueCredentialToken(ctxTenant, result.OwnerUserID, "bootstrap", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	session, err := f.service.CompleteBootstrap(ctxTenant, token, password, result.CompanyID)
	if err != nil {
		t.Fatal(err)
	}
	f.password = password
	return session
}

func (f *loginFixture) cleanup(t *testing.T) {
	t.Helper()
	if len(f.tenantIDs) == 0 {
		return
	}
	ctx := context.Background()
	tx, err := f.adminPool.Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: abrir transação administrativa: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Ordem respeita as FKs introduzidas pela migration 0041 (credential_token,
	// login_audit e refresh_token referenciam identity.app_user).
	statements := []string{
		"DELETE FROM identity.credential_token WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.login_audit WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.refresh_token WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.user_company WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.onboarding_request WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.user_role WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.app_user WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.role WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.establishment WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement, f.tenantIDs); err != nil {
			t.Errorf("cleanup: %s: %v", statement, err)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
		return
	}
	var remaining int
	if err := f.adminPool.QueryRow(ctx,
		`SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, f.tenantIDs,
	).Scan(&remaining); err != nil {
		t.Errorf("cleanup: confirmar remoção: %v", err)
	} else if remaining != 0 {
		t.Errorf("cleanup deixou %d tenant(s) residuais", remaining)
	}
}

// addCompany cria uma segunda empresa no tenant via pool administrativo (sem
// passar pelo caminho de amparius_app, que não tem INSERT em user_company) e
// vincula o usuário a ela, para exercitar troca de empresa no refresh.
func (f *loginFixture) addCompany(t *testing.T, tenantID, userID uuid.UUID, document string) uuid.UUID {
	t.Helper()
	var companyID uuid.UUID
	ctx := context.Background()
	if err := f.adminPool.QueryRow(ctx, `
		INSERT INTO identity.company (tenant_id, document, name, kind, status)
		VALUES ($1, $2, 'Empresa Secundária Teste', 'funeral', 'active')
		RETURNING id`, tenantID, document).Scan(&companyID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.adminPool.Exec(ctx, `
		INSERT INTO identity.user_company (tenant_id, user_id, company_id)
		VALUES ($1, $2, $3)`, tenantID, userID, companyID); err != nil {
		t.Fatal(err)
	}
	return companyID
}

// assertNoBypassRLS confirma, a cada teste, que o papel de aplicação não tem
// SUPERUSER nem BYPASSRLS — as funções SECURITY DEFINER da migration 0041
// são a única forma de ignorar RLS, nunca um grant de papel.
func assertNoBypassRLS(t *testing.T, database *db.DB) {
	t.Helper()
	var role string
	var bypass bool
	if err := database.WithSystemTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user, rolsuper OR rolbypassrls
			  FROM pg_roles WHERE rolname = current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if role != "amparius_app" || bypass {
		t.Fatalf("integração exige papel sem SUPERUSER/BYPASSRLS; role=%s bypass=%v", role, bypass)
	}
}

func TestAUTH01BResolveLoginCandidatesAcrossTenantsAndNoBypassRLS(t *testing.T) {
	f := newLoginFixture(t)
	assertNoBypassRLS(t, f.database)

	first := f.onboard(t, "auth01b-candidatos-1-"+uuid.NewString())
	second := f.onboard(t, "auth01b-candidatos-2-"+uuid.NewString())

	candidates, err := f.repo.ResolveLoginCandidates(f.ctx, "owner.dev@amparius.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("esperava exatamente 2 candidatos (um por tenant com o mesmo e-mail), obtido %d", len(candidates))
	}
	seen := map[uuid.UUID]bool{}
	for _, candidate := range candidates {
		seen[candidate.TenantID] = true
		if candidate.PasswordHash != "" {
			t.Fatalf("owner recém-criado não deveria ter password_hash antes do bootstrap: tenant=%s", candidate.TenantID)
		}
		if candidate.Status != "pending_auth" {
			t.Fatalf("status do owner recém-criado = %q, esperado pending_auth", candidate.Status)
		}
	}
	if !seen[first.TenantID] || !seen[second.TenantID] {
		t.Fatalf("candidatos não cobrem os dois tenants esperados: %+v", candidates)
	}
	// LoginCandidate não tem campo de e-mail/nome: a ausência é garantida em
	// tempo de compilação pelo próprio tipo (ADR 0048 §5).
}

func TestAUTH01BLoginAntiEnumerationLockoutAndRecovery(t *testing.T) {
	f := newLoginFixture(t)
	owner := f.onboard(t, "auth01b-lockout-"+uuid.NewString())
	password := "Correct-Horse-Battery-123!"
	session := f.activate(t, owner, password)
	if session.AccessToken == "" || session.RefreshToken == "" || session.CompanyID != owner.CompanyID {
		t.Fatalf("sessão de bootstrap incompleta: %+v", session)
	}

	// Anti-enumeração: e-mail inexistente e e-mail existente com senha errada
	// devolvem exatamente o mesmo sentinel, sem distinção observável.
	_, errUnknownEmail := f.service.Login(f.ctx, "nao-existe-"+uuid.NewString()+"@amparius.invalid", "qualquer", owner.CompanyID, "198.51.100.10")
	if !errors.Is(errUnknownEmail, identity.ErrInvalidCredentials) {
		t.Fatalf("e-mail inexistente deveria devolver ErrInvalidCredentials, obtido %v", errUnknownEmail)
	}
	_, errWrongPassword := f.service.Login(f.ctx, "owner.dev@amparius.invalid", "senha-errada-1", owner.CompanyID, "198.51.100.11")
	if !errors.Is(errWrongPassword, identity.ErrInvalidCredentials) {
		t.Fatalf("senha errada deveria devolver ErrInvalidCredentials, obtido %v", errWrongPassword)
	}

	// 5 tentativas erradas aplicam o bloqueio (ADR 0048 §6).
	for i := 0; i < 5; i++ {
		if _, err := f.service.Login(f.ctx, "owner.dev@amparius.invalid", "senha-errada-2", owner.CompanyID, "198.51.100.12"); !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Fatalf("tentativa %d: esperava ErrInvalidCredentials, obtido %v", i, err)
		}
	}
	ctxTenant := db.WithTenant(f.ctx, owner.TenantID)
	var failedAttempts int
	var lockedUntil *time.Time
	if err := f.database.WithTenantTx(ctxTenant, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT failed_attempts, locked_until FROM identity.app_user WHERE id = $1`, owner.OwnerUserID).Scan(&failedAttempts, &lockedUntil)
	}); err != nil {
		t.Fatal(err)
	}
	if failedAttempts < 5 || lockedUntil == nil || !lockedUntil.After(time.Now().UTC()) {
		t.Fatalf("esperava failed_attempts>=5 e locked_until futuro; obtido failed_attempts=%d locked_until=%v", failedAttempts, lockedUntil)
	}

	// Mesmo com a senha CORRETA, a conta bloqueada rejeita o login.
	if _, err := f.service.Login(f.ctx, "owner.dev@amparius.invalid", password, owner.CompanyID, "198.51.100.13"); !errors.Is(err, identity.ErrAccountLocked) {
		t.Fatalf("conta bloqueada com senha correta deveria devolver ErrAccountLocked, obtido %v", err)
	}

	// Simula a janela de bloqueio expirando e confirma a recuperação.
	if _, err := f.adminPool.Exec(context.Background(),
		`UPDATE identity.app_user SET failed_attempts = 0, locked_until = NULL WHERE id = $1`, owner.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	success, err := f.service.Login(f.ctx, "owner.dev@amparius.invalid", password, owner.CompanyID, "198.51.100.14")
	if err != nil {
		t.Fatalf("login com senha correta após reset deveria suceder: %v", err)
	}
	if success.UserID != owner.OwnerUserID || success.CompanyID != owner.CompanyID {
		t.Fatalf("sessão de sucesso inesperada: %+v", success)
	}
}

func TestAUTH01BRefreshRotationReuseDetectionAndIdempotentReplay(t *testing.T) {
	f := newLoginFixture(t)
	owner := f.onboard(t, "auth01b-refresh-"+uuid.NewString())
	password := "Refresh-Rotation-Pass-456!"
	session := f.activate(t, owner, password)

	// Rotação simples: a empresa é preservada quando companyId não é informado.
	rotated, err := f.service.Refresh(f.ctx, session.RefreshToken, nil, "")
	if err != nil {
		t.Fatalf("rotação inicial deveria suceder: %v", err)
	}
	if rotated.CompanyID != owner.CompanyID {
		t.Fatalf("rotação sem companyId deveria preservar a empresa anterior; obtido %s", rotated.CompanyID)
	}
	if rotated.RefreshToken == session.RefreshToken {
		t.Fatal("rotação deveria emitir um novo refresh token")
	}

	// Reuso do token já rotacionado revoga a família inteira.
	if _, err := f.service.Refresh(f.ctx, session.RefreshToken, nil, ""); !errors.Is(err, identity.ErrRefreshReuseDetected) {
		t.Fatalf("reuso do token antigo deveria devolver ErrRefreshReuseDetected, obtido %v", err)
	}
	if _, err := f.service.Refresh(f.ctx, rotated.RefreshToken, nil, ""); !errors.Is(err, identity.ErrRefreshInvalid) {
		t.Fatalf("token novo da mesma família deveria ter sido revogado pelo reuso detectado, obtido %v", err)
	}

	// Idempotência: duas chamadas com a mesma Idempotency-Key e o mesmo token
	// cru devolvem a mesma sessão, sem rotacionar duas vezes.
	second := f.loginAgain(t, owner)
	idempotencyKey := "replay-" + uuid.NewString()
	first, err := f.service.Refresh(f.ctx, second.RefreshToken, nil, idempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.Refresh(f.ctx, second.RefreshToken, nil, idempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	if replay.AccessToken != first.AccessToken || replay.RefreshToken != first.RefreshToken {
		t.Fatalf("reenvio com a mesma Idempotency-Key deveria devolver a mesma sessão: first=%+v replay=%+v", first, replay)
	}

	// Troca de empresa válida e inválida.
	third := f.loginAgain(t, owner)
	secondCompanyID := f.addCompany(t, owner.TenantID, owner.OwnerUserID, "00.000.000/0002-SWITCH-"+uuid.NewString()[:8])
	switched, err := f.service.Refresh(f.ctx, third.RefreshToken, &secondCompanyID, "")
	if err != nil {
		t.Fatalf("troca para empresa vinculada deveria suceder: %v", err)
	}
	if switched.CompanyID != secondCompanyID {
		t.Fatalf("CompanyID da sessão trocada = %s, esperado %s", switched.CompanyID, secondCompanyID)
	}

	fourth := f.loginAgain(t, owner)
	notAllowed := uuid.New()
	if _, err := f.service.Refresh(f.ctx, fourth.RefreshToken, &notAllowed, ""); !errors.Is(err, identity.ErrCompanyNotAllowed) {
		t.Fatalf("troca para empresa não vinculada deveria devolver ErrCompanyNotAllowed, obtido %v", err)
	}
}

// loginAgain faz login com a senha já ativada por activate, para obter uma
// nova sessão/refresh token independente sem precisar de outro onboarding.
func (f *loginFixture) loginAgain(t *testing.T, owner identity.OnboardingResult) identity.SessionResult {
	t.Helper()
	session, err := f.service.Login(f.ctx, "owner.dev@amparius.invalid", f.password, owner.CompanyID, "198.51.100.20")
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestAUTH01BLogoutIsIdempotentAndRespectsOwnership(t *testing.T) {
	f := newLoginFixture(t)
	ownerA := f.onboard(t, "auth01b-logout-a-"+uuid.NewString())
	ownerB := f.onboard(t, "auth01b-logout-b-"+uuid.NewString())
	sessionA := f.activate(t, ownerA, "Logout-Owner-A-789!")
	f.activate(t, ownerB, "Logout-Owner-B-789!")

	// Ownership: o userID de B não pode encerrar a sessão de A.
	if err := f.service.Logout(f.ctx, ownerB.OwnerUserID, sessionA.RefreshToken); err != nil {
		t.Fatalf("logout com ownership divergente deveria ser um no-op silencioso, obtido %v", err)
	}
	if _, err := f.service.Refresh(f.ctx, sessionA.RefreshToken, nil, ""); err != nil {
		t.Fatalf("sessão de A deveria continuar válida após logout de B com o token de A: %v", err)
	}

	// Logout legítimo revoga a família; chamadas repetidas continuam ok (idempotente).
	if err := f.service.Logout(f.ctx, ownerA.OwnerUserID, sessionA.RefreshToken); err != nil {
		t.Fatalf("logout legítimo não deveria falhar: %v", err)
	}
	if err := f.service.Logout(f.ctx, ownerA.OwnerUserID, sessionA.RefreshToken); err != nil {
		t.Fatalf("logout repetido deveria continuar idempotente: %v", err)
	}
}

func TestAUTH01BRevokeRequiresPasswordAndInvalidatesAllSessions(t *testing.T) {
	f := newLoginFixture(t)
	owner := f.onboard(t, "auth01b-revoke-"+uuid.NewString())
	password := "Revoke-All-Sessions-321!"
	sessionOne := f.activate(t, owner, password)
	sessionTwo, err := f.service.Login(f.ctx, "owner.dev@amparius.invalid", password, owner.CompanyID, "198.51.100.30")
	if err != nil {
		t.Fatal(err)
	}

	ctxTenant := db.WithTenant(f.ctx, owner.TenantID)
	if err := f.service.Revoke(ctxTenant, owner.OwnerUserID, "senha-errada"); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("revoke com senha errada deveria devolver ErrInvalidCredentials, obtido %v", err)
	}
	if _, err := f.service.Refresh(f.ctx, sessionOne.RefreshToken, nil, ""); err != nil {
		t.Fatalf("revoke com senha errada não deveria ter afetado sessões: %v", err)
	}

	if err := f.service.Revoke(ctxTenant, owner.OwnerUserID, password); err != nil {
		t.Fatalf("revoke com senha correta não deveria falhar: %v", err)
	}
	if _, err := f.service.Refresh(f.ctx, sessionOne.RefreshToken, nil, ""); !errors.Is(err, identity.ErrRefreshInvalid) {
		t.Fatalf("sessão 1 deveria estar revogada após revoke, obtido %v", err)
	}
	if _, err := f.service.Refresh(f.ctx, sessionTwo.RefreshToken, nil, ""); !errors.Is(err, identity.ErrRefreshInvalid) {
		t.Fatalf("sessão 2 deveria estar revogada após revoke (todas as sessões), obtido %v", err)
	}
}

func TestAUTH01BInviteBootstrapTokenSingleUseExpiryAndUnknownToken(t *testing.T) {
	f := newLoginFixture(t)
	owner := f.onboard(t, "auth01b-invite-"+uuid.NewString())
	ctxTenant := db.WithTenant(f.ctx, owner.TenantID)

	inviteeID, bootstrapToken, err := f.service.Invite(ctxTenant, "convidado@amparius.invalid", "Convidado Teste", []string{"owner"}, []uuid.UUID{owner.CompanyID})
	if err != nil {
		t.Fatal(err)
	}
	if inviteeID == uuid.Nil || bootstrapToken == "" {
		t.Fatalf("convite deveria devolver userID e token: %s / %q", inviteeID, bootstrapToken)
	}

	session, err := f.service.CompleteBootstrap(ctxTenant, bootstrapToken, "Invitee-Pass-654!", owner.CompanyID)
	if err != nil {
		t.Fatalf("primeiro uso do token de convite deveria suceder: %v", err)
	}
	if session.UserID != inviteeID {
		t.Fatalf("sessão do bootstrap pertence a %s, esperado %s", session.UserID, inviteeID)
	}

	// Reuso do mesmo token já consumido.
	if _, err := f.service.CompleteBootstrap(ctxTenant, bootstrapToken, "Outra-Senha-111!", owner.CompanyID); !errors.Is(err, identity.ErrCredentialTokenConsumed) {
		t.Fatalf("reuso do token consumido deveria devolver ErrCredentialTokenConsumed, obtido %v", err)
	}

	// Token expirado (emitido já no passado) para o mesmo usuário.
	expiredToken, _, err := f.repo.IssueCredentialToken(ctxTenant, inviteeID, "recovery", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CompleteRecovery(ctxTenant, expiredToken, "Mais-Uma-Senha-222!", owner.CompanyID); !errors.Is(err, identity.ErrCredentialTokenExpired) {
		t.Fatalf("token expirado deveria devolver ErrCredentialTokenExpired, obtido %v", err)
	}

	// Token nunca emitido (hex aleatório de 64 chars).
	if _, err := f.service.CompleteBootstrap(ctxTenant, "0000000000000000000000000000000000000000000000000000000000000a", "Senha-333!", owner.CompanyID); !errors.Is(err, identity.ErrCredentialTokenNotFound) {
		t.Fatalf("token inexistente deveria devolver ErrCredentialTokenNotFound, obtido %v", err)
	}

	// Papel desconhecido no convite é rejeitado antes de criar qualquer linha.
	if _, _, err := f.service.Invite(ctxTenant, "outro@amparius.invalid", "Outro", []string{"papel-inexistente"}, []uuid.UUID{owner.CompanyID}); !errors.Is(err, identity.ErrInviteRoleUnknown) {
		t.Fatalf("papel desconhecido deveria devolver ErrInviteRoleUnknown, obtido %v", err)
	}
}

func TestAUTH01BNewTablesEnforceTenantIsolation(t *testing.T) {
	f := newLoginFixture(t)
	tenantA := f.onboard(t, "auth01b-isolamento-a-"+uuid.NewString())
	tenantB := f.onboard(t, "auth01b-isolamento-b-"+uuid.NewString())
	f.activate(t, tenantA, "Isolamento-A-987!")
	f.activate(t, tenantB, "Isolamento-B-987!")

	ctxTenantB := db.WithTenant(f.ctx, tenantB.TenantID)
	if err := f.database.WithTenantTx(ctxTenantB, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM identity.refresh_token WHERE user_id = $1`, tenantA.OwnerUserID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("RLS permitiu que o tenant B visse refresh_token do tenant A (%d linhas)", count)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM identity.login_audit WHERE user_id = $1`, tenantA.OwnerUserID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("RLS permitiu que o tenant B visse login_audit do tenant A (%d linhas)", count)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM identity.credential_token WHERE user_id = $1`, tenantA.OwnerUserID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("RLS permitiu que o tenant B visse credential_token do tenant A (%d linhas)", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Confirmação simétrica: o próprio tenant A vê sua linha de refresh_token.
	ctxTenantA := db.WithTenant(f.ctx, tenantA.TenantID)
	if err := f.database.WithTenantTx(ctxTenantA, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM identity.refresh_token WHERE user_id = $1`, tenantA.OwnerUserID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("RLS bloqueou o tenant A de ver sua própria linha de refresh_token")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAUTH01BLoginAuditNeverPersistsPII(t *testing.T) {
	f := newLoginFixture(t)
	owner := f.onboard(t, "auth01b-pii-"+uuid.NewString())
	f.activate(t, owner, "PII-Scan-Pass-555!")
	_, _ = f.service.Login(f.ctx, "owner.dev@amparius.invalid", "senha-errada", owner.CompanyID, "198.51.100.40")

	rows, err := f.adminPool.Query(context.Background(), `
		SELECT column_name FROM information_schema.columns
		 WHERE table_schema = 'identity' AND table_name = 'login_audit'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	expected := []string{"id", "tenant_id", "user_id", "result", "at"}
	if len(columns) != len(expected) {
		t.Fatalf("identity.login_audit tem %d colunas, esperado exatamente %v", len(columns), expected)
	}
	for _, name := range expected {
		if !columns[name] {
			t.Fatalf("identity.login_audit não tem a coluna esperada %q", name)
		}
	}

	ctxTenant := db.WithTenant(f.ctx, owner.TenantID)
	if err := f.database.WithTenantTx(ctxTenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT result FROM identity.login_audit WHERE user_id = $1`, owner.OwnerUserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		found := 0
		for rows.Next() {
			var result string
			if err := rows.Scan(&result); err != nil {
				return err
			}
			found++
			allowed := map[string]bool{
				"success": true, "invalid_credentials": true, "locked": true,
				"pending_auth": true, "refresh_reuse_detected": true, "logout": true, "revoke": true,
			}
			if !allowed[result] {
				return fmt.Errorf("valor de result fora do enum esperado: %q", result)
			}
		}
		if found < 2 {
			return fmt.Errorf("esperava ao menos 2 linhas de auditoria (sucesso do bootstrap + falha de login), obtido %d", found)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAUTH01BLoginLatencyP95 mede a latência de POST /v1/auth/login (caminho
// de sucesso, um único candidato) contra o Postgres descartável, para a
// evidência de performance exigida em docs/qa/auth-01b-evidencias.md. Cada
// chamada usa um clientIP sintético distinto para não acionar o rate limiter
// em memória (loginRateLimitMax=20/15min por IP+e-mail), que não é o alvo
// desta medição.
func TestAUTH01BLoginLatencyP95(t *testing.T) {
	f := newLoginFixture(t)
	owner := f.onboard(t, "auth01b-latencia-"+uuid.NewString())
	f.activate(t, owner, "Latencia-P95-Pass-777!")

	const iterations = 60
	durations := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		clientIP := fmt.Sprintf("203.0.113.%d", i%250+1)
		start := time.Now()
		if _, err := f.service.Login(f.ctx, "owner.dev@amparius.invalid", "Latencia-P95-Pass-777!", owner.CompanyID, clientIP); err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(start))
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[int(float64(len(durations))*0.95)-1]
	var total time.Duration
	for _, d := range durations {
		total += d
	}
	t.Logf("AUTH-01B login latency: n=%d avg=%s p95=%s max=%s", iterations, total/time.Duration(iterations), p95, durations[len(durations)-1])
	if p95 > 500*time.Millisecond {
		t.Fatalf("p95 de login = %s, acima do limite de 500ms para o Postgres descartável de teste", p95)
	}
}
