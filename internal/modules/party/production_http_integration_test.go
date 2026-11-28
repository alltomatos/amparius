package party_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/modules/party"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// partyAuthFixture monta o bootstrap real de /v1/parties (ADR 0041, issue
// #72): auth.Service de produção com identity.Repository como
// SubjectResolver/CompanyResolver, party.Repository real e
// httpserver.AuthenticatedRoute, tudo contra PostgreSQL real sob o papel
// amparius_app (NOSUPERUSER/NOBYPASSRLS). Somente o verificador OIDC e o
// verificador MFA são duplos de teste: nenhum provedor real foi escolhido
// ainda (ADR 0038), e emitir um token OIDC de verdade está fora do escopo
// desta fatia.
type partyAuthFixture struct {
	handler  http.Handler
	database *db.DB
	logs     *bytes.Buffer

	tenantA, companyA uuid.UUID
	tenantB, companyB uuid.UUID

	ownerToken    string
	auditorToken  string
	outsiderToken string
	tenantBToken  string
}

func setupPartyAuthFixture(t *testing.T) *partyAuthFixture {
	t.Helper()
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN são necessários")
	}

	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)

	var bypass bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatal("teste de /v1/parties exige papel de aplicação sem SUPERUSER/BYPASSRLS")
	}

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}

	identityRepository, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	partyRepository, err := party.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}

	// tenantIDs cresce conforme cada tenant é provisionado, e o cleanup é
	// registrado já com o primeiro tenant: se o onboarding do segundo tenant
	// falhar (t.Fatal), o primeiro ainda é limpo, em vez de ficar no banco
	// (correção da issue #72 — cleanup não podia depender de todo o setup
	// terminar com sucesso).
	var tenantIDs []uuid.UUID
	t.Cleanup(func() {
		defer adminPool.Close()
		cleanupCtx := context.Background()
		for _, statement := range []string{
			"DELETE FROM party.person WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.party WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.user_company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.user_role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.onboarding_request WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.app_user WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.establishment WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
		} {
			if _, cleanupErr := adminPool.Exec(cleanupCtx, statement, tenantIDs); cleanupErr != nil {
				t.Errorf("cleanup %q: %v", statement, cleanupErr)
				return
			}
		}
	})

	tenantA, err := identityRepository.Onboard(ctx, identity.DevelopmentRequest("party-v1-a-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, tenantA.TenantID)

	tenantB, err := identityRepository.Onboard(ctx, identity.DevelopmentRequest("party-v1-b-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, tenantB.TenantID)

	ownerSubject := "owner-" + uuid.NewString()
	tenantBOwnerSubject := "owner-b-" + uuid.NewString()
	auditorSubject := "auditor-" + uuid.NewString()

	if _, err := adminPool.Exec(ctx, `UPDATE identity.app_user SET auth_subject=$2, status='active', mfa_enabled=true WHERE tenant_id=$1 AND id=$3`,
		tenantA.TenantID, ownerSubject, tenantA.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, `UPDATE identity.app_user SET auth_subject=$2, status='active', mfa_enabled=true WHERE tenant_id=$1 AND id=$3`,
		tenantB.TenantID, tenantBOwnerSubject, tenantB.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	// identity.create_onboarding (migration 0042) já vincula cada owner à
	// empresa sede em identity.user_company; nenhum INSERT manual é necessário.

	// Usuário "auditor": papel sem party:write, só party:read (DefaultRolePolicy).
	var auditorUserID uuid.UUID
	auditorEmail := fmt.Sprintf("auditor.%s@amparius.invalid", strings.ToLower(uuid.NewString()))
	if err := adminPool.QueryRow(ctx, `INSERT INTO identity.app_user (tenant_id, email, name, status, mfa_enabled, auth_subject)
		VALUES ($1,$2,$3,'active',true,$4) RETURNING id`, tenantA.TenantID, auditorEmail, "Auditor E2E", auditorSubject).Scan(&auditorUserID); err != nil {
		t.Fatal(err)
	}
	var auditorRoleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO identity.role (tenant_id, code, name) VALUES ($1,'auditor','Auditor') RETURNING id`,
		tenantA.TenantID).Scan(&auditorRoleID); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, `INSERT INTO identity.user_role (tenant_id, user_id, role_id) VALUES ($1,$2,$3)`,
		tenantA.TenantID, auditorUserID, auditorRoleID); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, `INSERT INTO identity.user_company (tenant_id, user_id, company_id) VALUES ($1,$2,$3)`,
		tenantA.TenantID, auditorUserID, tenantA.CompanyID); err != nil {
		t.Fatal(err)
	}

	// outsiderCompanyID nunca é vinculada a nenhum usuário: prova a negação de
	// empresa sem vínculo (SubjectRecord.allowsCompany) de forma fail-closed,
	// com dado real resolvido via identity.Repository.ResolveSubject.
	outsiderCompanyID := uuid.New()

	ownerToken := "token-owner-" + uuid.NewString()
	tenantBToken := "token-tenant-b-" + uuid.NewString()
	auditorToken := "token-auditor-" + uuid.NewString()
	outsiderToken := "token-outsider-" + uuid.NewString()

	tokens := map[string]platformauth.OIDCIdentity{
		ownerToken:    {Issuer: "test-issuer", Subject: ownerSubject, TenantID: tenantA.TenantID, CompanyID: tenantA.CompanyID},
		tenantBToken:  {Issuer: "test-issuer", Subject: tenantBOwnerSubject, TenantID: tenantB.TenantID, CompanyID: tenantB.CompanyID},
		auditorToken:  {Issuer: "test-issuer", Subject: auditorSubject, TenantID: tenantA.TenantID, CompanyID: tenantA.CompanyID},
		outsiderToken: {Issuer: "test-issuer", Subject: ownerSubject, TenantID: tenantA.TenantID, CompanyID: outsiderCompanyID},
	}
	oidc := platformauth.OIDCVerifierFunc(func(_ context.Context, token string) (platformauth.OIDCIdentity, error) {
		identity, ok := tokens[token]
		if !ok {
			return platformauth.OIDCIdentity{}, platformauth.ErrInvalidOIDCToken
		}
		return identity, nil
	})
	mfa := platformauth.MFAVerifierFunc(func(context.Context, platformauth.OIDCIdentity) (bool, error) { return true, nil })
	policy, err := platformauth.DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	authService, err := platformauth.NewService(oidc, mfa, identityRepository, identityRepository, policy, logger)
	if err != nil {
		t.Fatal(err)
	}

	partyHandler := party.NewProductionHTTPHandler(partyRepository)
	handler := httpserver.New(logger, httpserver.Options{
		Authenticator: authService,
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "POST /v1/parties", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(partyHandler.Create)},
			{Pattern: "GET /v1/parties", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(partyHandler.Search)},
		},
	}).Handler()

	return &partyAuthFixture{
		handler:       handler,
		database:      database,
		logs:          logs,
		tenantA:       tenantA.TenantID,
		companyA:      tenantA.CompanyID,
		tenantB:       tenantB.TenantID,
		companyB:      tenantB.CompanyID,
		ownerToken:    ownerToken,
		auditorToken:  auditorToken,
		outsiderToken: outsiderToken,
		tenantBToken:  tenantBToken,
	}
}

func (f *partyAuthFixture) do(method, path, bearer, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestProductionPartiesIntegrationUnauthenticated(t *testing.T) {
	fixture := setupPartyAuthFixture(t)

	noBearer := fixture.do(http.MethodGet, "/v1/parties?q=qualquer", "", "")
	if noBearer.Code != http.StatusUnauthorized {
		t.Fatalf("sem Authorization: status = %d, esperado 401: %s", noBearer.Code, noBearer.Body.String())
	}

	invalidBearer := fixture.do(http.MethodGet, "/v1/parties?q=qualquer", "token-nao-existe", "")
	if invalidBearer.Code != http.StatusUnauthorized {
		t.Fatalf("bearer inválido: status = %d, esperado 401: %s", invalidBearer.Code, invalidBearer.Body.String())
	}
}

func TestProductionPartiesIntegrationDeniesInsufficientRole(t *testing.T) {
	fixture := setupPartyAuthFixture(t)

	// auditor tem party:read, mas não party:write (DefaultRolePolicy).
	denied := fixture.do(http.MethodPost, "/v1/parties", fixture.auditorToken, `{"name":"Maria Auditor"}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("POST com papel auditor: status = %d, esperado 403: %s", denied.Code, denied.Body.String())
	}
	if !strings.Contains(denied.Body.String(), `"code":"PERMISSION_DENIED"`) {
		t.Fatalf("code ausente: %s", denied.Body.String())
	}

	allowedRead := fixture.do(http.MethodGet, "/v1/parties?q=Maria", fixture.auditorToken, "")
	if allowedRead.Code != http.StatusOK {
		t.Fatalf("GET com papel auditor: status = %d, esperado 200: %s", allowedRead.Code, allowedRead.Body.String())
	}
}

func TestProductionPartiesIntegrationDeniesCompanyWithoutLink(t *testing.T) {
	fixture := setupPartyAuthFixture(t)

	denied := fixture.do(http.MethodGet, "/v1/parties?q=qualquer", fixture.outsiderToken, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("empresa sem vínculo: status = %d, esperado 403: %s", denied.Code, denied.Body.String())
	}
	if !strings.Contains(denied.Body.String(), `"code":"SCOPE_FORBIDDEN"`) {
		t.Fatalf("code ausente: %s", denied.Body.String())
	}
}

func TestProductionPartiesIntegrationFailsClosedWithoutAuthenticator(t *testing.T) {
	fixture := setupPartyAuthFixture(t)

	// Mesma superfície/rota, mas sem Authenticator: deve permanecer 503, nunca
	// cair para um caminho permissivo mesmo com banco e repositório reais.
	handler := httpserver.New(slog.New(slog.NewJSONHandler(fixture.logs, nil)), httpserver.Options{
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "GET /v1/parties", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(party.NewProductionHTTPHandler(nil).Search)},
		},
	}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/parties?q=qualquer", nil)
	req.Header.Set("Authorization", "Bearer "+fixture.ownerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, esperado 503: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"AUTHENTICATION_UNAVAILABLE"`) {
		t.Fatalf("code ausente: %s", rec.Body.String())
	}
}

func TestProductionPartiesIntegrationCreateAndSearchPersist(t *testing.T) {
	fixture := setupPartyAuthFixture(t)

	marker := "Maria-" + uuid.NewString()
	created := fixture.do(http.MethodPost, "/v1/parties", fixture.ownerToken, fmt.Sprintf(`{"name":%q,"document":"12345678900"}`, marker))
	if created.Code != http.StatusCreated {
		t.Fatalf("criação: status = %d, esperado 201: %s", created.Code, created.Body.String())
	}
	if created.Header().Get("Location") != "" {
		t.Fatalf("cabeçalho Location presente sem rota GET /v1/parties/{id}: %q", created.Header().Get("Location"))
	}
	var createdPayload struct {
		ID        string `json:"id"`
		CivilName string `json:"civilName"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdPayload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if createdPayload.CivilName != marker {
		t.Fatalf("civilName = %q, esperado %q", createdPayload.CivilName, marker)
	}

	found := fixture.do(http.MethodGet, "/v1/parties?q="+marker, fixture.ownerToken, "")
	if found.Code != http.StatusOK {
		t.Fatalf("busca: status = %d, esperado 200: %s", found.Code, found.Body.String())
	}
	var searchPayload struct {
		Items []struct {
			ID        string `json:"id"`
			CivilName string `json:"civilName"`
		} `json:"items"`
	}
	if err := json.Unmarshal(found.Body.Bytes(), &searchPayload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if len(searchPayload.Items) != 1 || searchPayload.Items[0].ID != createdPayload.ID {
		t.Fatalf("itens encontrados = %#v, esperado 1 igual ao criado (%s)", searchPayload.Items, createdPayload.ID)
	}
}

// TestProductionPartiesIntegrationTenantRLSIsolation prova isolamento de
// tenant em duas camadas independentes: (1) a API nega a visibilidade entre
// tenants via /v1/parties; (2) uma consulta direta à tabela sob o contexto
// do outro tenant, sem filtro de tenant_id na cláusula WHERE, devolve zero
// linhas — ou seja, é a política RLS do Postgres que nega, não só a lógica
// de aplicação.
func TestProductionPartiesIntegrationTenantRLSIsolation(t *testing.T) {
	fixture := setupPartyAuthFixture(t)

	marker := "Isolamento-" + uuid.NewString()
	created := fixture.do(http.MethodPost, "/v1/parties", fixture.ownerToken, fmt.Sprintf(`{"name":%q}`, marker))
	if created.Code != http.StatusCreated {
		t.Fatalf("criação no tenant A: status = %d: %s", created.Code, created.Body.String())
	}

	crossTenantSearch := fixture.do(http.MethodGet, "/v1/parties?q="+marker, fixture.tenantBToken, "")
	if crossTenantSearch.Code != http.StatusOK {
		t.Fatalf("busca no tenant B: status = %d, esperado 200: %s", crossTenantSearch.Code, crossTenantSearch.Body.String())
	}
	var crossPayload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(crossTenantSearch.Body.Bytes(), &crossPayload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if len(crossPayload.Items) != 0 {
		t.Fatalf("tenant B viu %d registros do tenant A via API", len(crossPayload.Items))
	}

	ctx := context.Background()
	var countTenantA, countTenantB int
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.party WHERE name = $1`, marker).Scan(&countTenantA)
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantB), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.party WHERE name = $1`, marker).Scan(&countTenantB)
	}); err != nil {
		t.Fatal(err)
	}
	if countTenantA != 1 {
		t.Fatalf("contagem sob RLS do tenant A = %d, esperado 1", countTenantA)
	}
	if countTenantB != 0 {
		t.Fatalf("contagem sob RLS do tenant B = %d, esperado 0 (RLS deveria isolar mesmo sem filtro de tenant_id na query)", countTenantB)
	}
}

// TestProductionPartiesIntegrationSearchExcludesCompanySharingLimit prova,
// com SQL real, que o filtro kind=person acontece antes de ORDER/LIMIT
// (correção da issue #72). Cria duas pessoas físicas e, depois, uma pessoa
// jurídica com o mesmo termo (mais recente que ambas por updated_at). Com
// limit=1, se o filtro fosse aplicado em memória depois do corte do banco, a
// pessoa jurídica (mais recente) teria sido a única linha trazida e a busca
// devolveria zero itens; filtrando no SQL, a linha trazida já é a pessoa
// física mais recente.
func TestProductionPartiesIntegrationSearchExcludesCompanySharingLimit(t *testing.T) {
	fixture := setupPartyAuthFixture(t)

	marker := "Zeta-Compartilhado-" + uuid.NewString()
	first := fixture.do(http.MethodPost, "/v1/parties", fixture.ownerToken, fmt.Sprintf(`{"name":%q}`, marker+" Um"))
	if first.Code != http.StatusCreated {
		t.Fatalf("criação da primeira PF: status = %d: %s", first.Code, first.Body.String())
	}
	second := fixture.do(http.MethodPost, "/v1/parties", fixture.ownerToken, fmt.Sprintf(`{"name":%q}`, marker+" Dois"))
	if second.Code != http.StatusCreated {
		t.Fatalf("criação da segunda PF: status = %d: %s", second.Code, second.Body.String())
	}
	var secondPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondPayload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}

	// Pessoa jurídica criada por fora de /v1/parties (esta fatia não cria PJ),
	// direto no repositório real, mais recente que as duas PFs acima.
	partyRepository, err := party.NewRepository(fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := partyRepository.Create(db.WithTenant(context.Background(), fixture.tenantA), party.Party{
		Kind: party.KindCompany, Name: marker + " LTDA", Status: party.StatusActive,
	}); err != nil {
		t.Fatalf("criação da PJ via repositório: %v", err)
	}

	search := fixture.do(http.MethodGet, "/v1/parties?q="+marker+"&limit=1", fixture.ownerToken, "")
	if search.Code != http.StatusOK {
		t.Fatalf("busca: status = %d, esperado 200: %s", search.Code, search.Body.String())
	}
	var payload struct {
		Items []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(search.Body.Bytes(), &payload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("items = %d, esperado 1 (a PF mais recente, nunca a PJ): %s", len(payload.Items), search.Body.String())
	}
	if payload.Items[0].Kind != party.KindPerson {
		t.Fatalf("kind = %q, esperado person", payload.Items[0].Kind)
	}
	if payload.Items[0].ID != secondPayload.ID {
		t.Fatalf("id = %q, esperado %q (a PF mais recente)", payload.Items[0].ID, secondPayload.ID)
	}
}

// TestProductionPartiesIntegrationNoPIIInErrorsOrLogs prova que nem o corpo
// de uma resposta de erro nem o log estruturado da decisão de autenticação
// carregam o nome, documento ou qualquer outro dado pessoal submetido.
func TestProductionPartiesIntegrationNoPIIInErrorsOrLogs(t *testing.T) {
	fixture := setupPartyAuthFixture(t)

	secretDocument := "99988877700"
	secretName := "Nome-Sigiloso-" + uuid.NewString()

	// Corpo inválido (campo desconhecido) ainda carrega dado pessoal: a
	// rejeição precisa ser genérica, sem ecoar o payload.
	invalid := fixture.do(http.MethodPost, "/v1/parties", fixture.ownerToken,
		fmt.Sprintf(`{"name":%q,"document":%q,"tenant_id":"%s"}`, secretName, secretDocument, uuid.NewString()))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado 400: %s", invalid.Code, invalid.Body.String())
	}
	if strings.Contains(invalid.Body.String(), secretName) || strings.Contains(invalid.Body.String(), secretDocument) {
		t.Fatalf("resposta de erro vazou dado pessoal: %s", invalid.Body.String())
	}

	// Negação de autenticação (empresa sem vínculo) também não deve ecoar nada
	// sensível, e o log de decisão de auth.Require não deve registrar o
	// bearer nem qualquer dado pessoal.
	denied := fixture.do(http.MethodGet, "/v1/parties?q=qualquer", fixture.outsiderToken, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("status = %d, esperado 403: %s", denied.Code, denied.Body.String())
	}

	logged := fixture.logs.String()
	if strings.Contains(logged, secretName) || strings.Contains(logged, secretDocument) {
		t.Fatalf("log de auth_decision vazou dado pessoal: %s", logged)
	}
	if strings.Contains(logged, fixture.outsiderToken) || strings.Contains(logged, fixture.ownerToken) {
		t.Fatalf("log de auth_decision vazou bearer token: %s", logged)
	}
}
