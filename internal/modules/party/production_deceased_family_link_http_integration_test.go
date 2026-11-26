package party_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/modules/party"
	"github.com/alltomatos/amparius/internal/platform/audit"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// deceasedFamilyLinkFixture monta o bootstrap real de /v1/deceased e
// /v1/deceased/{deceasedId}/family-links (ADR 0042, issue #73): auth.Service
// de produção, party.Repository real contra PostgreSQL sob o papel de
// aplicação (NOSUPERUSER/NOBYPASSRLS) e um audit.PolicyProvider sintético —
// não aprovado por nenhum DPO real — usado apenas para exercitar o caminho
// feliz do contrato Go/HTTP (ADR 0042 §4). O verificador OIDC e o MFA são
// duplos de teste, como no restante da suíte de /v1/parties.
type deceasedFamilyLinkFixture struct {
	handler         http.Handler
	database        *db.DB
	adminPool       *pgxpool.Pool
	partyRepository *party.Repository
	policy          *syntheticPolicyProvider

	tenantA, companyA uuid.UUID
	tenantB, companyB uuid.UUID

	ownerToken   string
	tenantBToken string
}

func setupDeceasedFamilyLinkFixture(t *testing.T) *deceasedFamilyLinkFixture {
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
		t.Fatal("teste de falecido/vínculo familiar exige papel de aplicação sem SUPERUSER/BYPASSRLS")
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

	// tenantIDs cresce conforme cada tenant é provisionado; o cleanup é
	// registrado já com o primeiro tenant para não depender do setup inteiro
	// terminar com sucesso (mesma correção da issue #72 reaproveitada aqui).
	var tenantIDs []uuid.UUID
	t.Cleanup(func() {
		defer adminPool.Close()
		cleanupCtx := context.Background()
		// privacy.consent_record e audit.log são append-only (trigger de
		// banco); o cleanup administrativo de teste precisa desarmar o
		// trigger de usuário só para as linhas dos tenants de teste, nunca em
		// produção (mesmo padrão de internal/platform/audit/integration_test.go).
		tx, cleanupErr := adminPool.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, statement := range []string{
				"ALTER TABLE privacy.consent_record DISABLE TRIGGER consent_record_append_only",
				"ALTER TABLE audit.log DISABLE TRIGGER audit_log_append_only",
				"DELETE FROM privacy.consent_record WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM audit.log WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.family_link WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.deceased WHERE tenant_id = ANY($1::uuid[])",
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
				"ALTER TABLE audit.log ENABLE TRIGGER audit_log_append_only",
				"ALTER TABLE privacy.consent_record ENABLE TRIGGER consent_record_append_only",
			} {
				if strings.HasPrefix(statement, "DELETE") {
					_, cleanupErr = tx.Exec(cleanupCtx, statement, tenantIDs)
				} else {
					_, cleanupErr = tx.Exec(cleanupCtx, statement)
				}
				if cleanupErr != nil {
					break
				}
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(cleanupCtx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup administrativo de falecido/vínculo familiar: %v", cleanupErr)
			return
		}
		// Prova de limpeza: nenhuma linha residual em nenhuma das tabelas
		// tocadas por esta fatia permanece associada aos tenants de teste.
		var remainingDeceased, remainingLinks, remainingConsent, remainingAudit, remainingTenants int
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM party.deceased WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingDeceased); err != nil {
			t.Errorf("confirmar cleanup de party.deceased: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM party.family_link WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingLinks); err != nil {
			t.Errorf("confirmar cleanup de party.family_link: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM privacy.consent_record WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingConsent); err != nil {
			t.Errorf("confirmar cleanup de privacy.consent_record: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM audit.log WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingAudit); err != nil {
			t.Errorf("confirmar cleanup de audit.log: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingTenants); err != nil {
			t.Errorf("confirmar cleanup de identity.tenant: %v", err)
		}
		if remainingDeceased != 0 || remainingLinks != 0 || remainingConsent != 0 || remainingAudit != 0 || remainingTenants != 0 {
			t.Errorf("cleanup deixou linhas residuais: deceased=%d family_link=%d consent=%d audit=%d tenant=%d",
				remainingDeceased, remainingLinks, remainingConsent, remainingAudit, remainingTenants)
		} else {
			t.Log("cleanup: zero resíduos em deceased, family_link, consent_record, audit.log e identity.tenant")
		}
	})

	tenantA, err := identityRepository.Onboard(ctx, identity.DevelopmentRequest("deceased-fl-a-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, tenantA.TenantID)

	tenantB, err := identityRepository.Onboard(ctx, identity.DevelopmentRequest("deceased-fl-b-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, tenantB.TenantID)

	ownerSubject := "owner-" + uuid.NewString()
	tenantBOwnerSubject := "owner-b-" + uuid.NewString()
	if _, err := adminPool.Exec(ctx, `UPDATE identity.app_user SET auth_subject=$2, status='active', mfa_enabled=true WHERE tenant_id=$1 AND id=$3`,
		tenantA.TenantID, ownerSubject, tenantA.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, `UPDATE identity.app_user SET auth_subject=$2, status='active', mfa_enabled=true WHERE tenant_id=$1 AND id=$3`,
		tenantB.TenantID, tenantBOwnerSubject, tenantB.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, `INSERT INTO identity.user_company (tenant_id, user_id, company_id) VALUES ($1,$2,$3)`,
		tenantA.TenantID, tenantA.OwnerUserID, tenantA.CompanyID); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, `INSERT INTO identity.user_company (tenant_id, user_id, company_id) VALUES ($1,$2,$3)`,
		tenantB.TenantID, tenantB.OwnerUserID, tenantB.CompanyID); err != nil {
		t.Fatal(err)
	}

	ownerToken := "token-owner-" + uuid.NewString()
	tenantBToken := "token-tenant-b-" + uuid.NewString()
	tokens := map[string]platformauth.OIDCIdentity{
		ownerToken:   {Issuer: "test-issuer", Subject: ownerSubject, TenantID: tenantA.TenantID, CompanyID: tenantA.CompanyID},
		tenantBToken: {Issuer: "test-issuer", Subject: tenantBOwnerSubject, TenantID: tenantB.TenantID, CompanyID: tenantB.CompanyID},
	}
	oidc := platformauth.OIDCVerifierFunc(func(_ context.Context, token string) (platformauth.OIDCIdentity, error) {
		found, ok := tokens[token]
		if !ok {
			return platformauth.OIDCIdentity{}, platformauth.ErrInvalidOIDCToken
		}
		return found, nil
	})
	mfa := platformauth.MFAVerifierFunc(func(context.Context, platformauth.OIDCIdentity) (bool, error) { return true, nil })
	rolePolicy, err := platformauth.DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	authService, err := platformauth.NewService(oidc, mfa, identityRepository, identityRepository, rolePolicy, logger)
	if err != nil {
		t.Fatal(err)
	}

	// policyProvider é sintético: nenhum DPO aprovou esta finalidade, base
	// legal ou retenção. Existe só para provar o caminho feliz do contrato
	// Go/HTTP contra Postgres real; o bootstrap de produção (cmd/api/main.go)
	// continua com policy nil (ADR 0042 §4).
	policyProvider := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode:    "test.deceased_family_link",
		LegalBasisCode: "test.legitimate_interest",
		RetentionUntil: time.Now().Add(72 * time.Hour),
	}}

	deceasedHandler := party.NewProductionDeceasedHTTPHandler(partyRepository, policyProvider)
	familyLinkHandler := party.NewProductionFamilyLinkHTTPHandler(partyRepository, policyProvider)
	handler := httpserver.New(logger, httpserver.Options{
		Authenticator:          authService,
		DevelopmentMode:        true,
		DevelopmentDeceased:    party.NewDevelopmentDeceasedHTTPHandler(partyRepository),
		DevelopmentFamilyLinks: party.NewDevelopmentFamilyLinkHTTPHandler(partyRepository),
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "POST /v1/deceased", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(deceasedHandler.Create)},
			{Pattern: "GET /v1/deceased/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(deceasedHandler.Get)},
			{Pattern: "POST /v1/deceased/{deceasedId}/family-links", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(familyLinkHandler.Create)},
			{Pattern: "GET /v1/family-links/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(familyLinkHandler.Get)},
		},
	}).Handler()

	return &deceasedFamilyLinkFixture{
		handler:         handler,
		database:        database,
		adminPool:       adminPool,
		partyRepository: partyRepository,
		policy:          policyProvider,
		tenantA:         tenantA.TenantID,
		companyA:        tenantA.CompanyID,
		tenantB:         tenantB.TenantID,
		companyB:        tenantB.CompanyID,
		ownerToken:      ownerToken,
		tenantBToken:    tenantBToken,
	}
}

func (f *deceasedFamilyLinkFixture) do(method, path, bearer, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if strings.HasPrefix(path, "/_dev/") {
		req.Header.Set("X-Amparius-Development-Tenant-ID", f.tenantA.String())
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// createPerson cria uma pessoa física diretamente pelo repositório real
// (fora do contrato /v1/parties, que não faz parte desta fatia) para servir
// de falecido ou de familiar nos testes.
func (f *deceasedFamilyLinkFixture) createPerson(t *testing.T, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	created, err := f.partyRepository.Create(db.WithTenant(context.Background(), tenantID), party.Party{
		Kind: party.KindPerson, Name: name, Status: party.StatusActive,
	})
	if err != nil {
		t.Fatalf("criar pessoa de apoio (%s): %v", name, err)
	}
	return created.ID
}

func (f *deceasedFamilyLinkFixture) countByTenant(t *testing.T, table string, tenantID uuid.UUID) int {
	t.Helper()
	var count int
	if err := f.adminPool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1`, table), tenantID).Scan(&count); err != nil {
		t.Fatalf("contar %s: %v", table, err)
	}
	return count
}

// TestProductionDeceasedFamilyLinkIntegrationCreateAndGet prova, com Postgres
// real, a criação e consulta autenticadas de falecido e vínculo familiar, e
// que o consentimento/auditoria exigidos pelo ADR 0042 foram gravados na
// mesma operação.
func TestProductionDeceasedFamilyLinkIntegrationCreateAndGet(t *testing.T) {
	fixture := setupDeceasedFamilyLinkFixture(t)

	deceasedPersonID := fixture.createPerson(t, fixture.tenantA, "Maria Falecida E2E")
	relativeID := fixture.createPerson(t, fixture.tenantA, "João Familiar E2E")

	createdDeceased := fixture.do(http.MethodPost, "/v1/deceased", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"deathAt":"2026-11-20T09:00:00Z","deathCertificate":"DC-E2E-1"}`, deceasedPersonID))
	if createdDeceased.Code != http.StatusCreated {
		t.Fatalf("POST /v1/deceased: status = %d, esperado 201: %s", createdDeceased.Code, createdDeceased.Body.String())
	}
	var deceasedPayload struct {
		ID               string  `json:"id"`
		PersonID         string  `json:"personId"`
		DeathCertificate *string `json:"deathCertificate"`
		CivilName        string  `json:"civilName"`
	}
	if err := json.Unmarshal(createdDeceased.Body.Bytes(), &deceasedPayload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if deceasedPayload.PersonID != deceasedPersonID.String() || deceasedPayload.CivilName != "Maria Falecida E2E" {
		t.Fatalf("payload de falecido inesperado: %#v", deceasedPayload)
	}

	getDeceased := fixture.do(http.MethodGet, "/v1/deceased/"+deceasedPayload.ID, fixture.ownerToken, "")
	if getDeceased.Code != http.StatusOK {
		t.Fatalf("GET /v1/deceased/{id}: status = %d: %s", getDeceased.Code, getDeceased.Body.String())
	}

	createdLink := fixture.do(http.MethodPost, "/v1/deceased/"+deceasedPayload.ID+"/family-links", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"relation":"child","consentEvidenceRef":"`+testConsentEvidence+`"}`, relativeID))
	if createdLink.Code != http.StatusCreated {
		t.Fatalf("POST .../family-links: status = %d, esperado 201: %s", createdLink.Code, createdLink.Body.String())
	}
	var linkPayload struct {
		ID       string `json:"id"`
		Relation string `json:"relation"`
		Person   struct {
			ID string `json:"id"`
		} `json:"person"`
	}
	if err := json.Unmarshal(createdLink.Body.Bytes(), &linkPayload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if linkPayload.Relation != "child" || linkPayload.Person.ID != relativeID.String() {
		t.Fatalf("payload de vínculo inesperado: %#v", linkPayload)
	}

	getLink := fixture.do(http.MethodGet, "/v1/family-links/"+linkPayload.ID, fixture.ownerToken, "")
	if getLink.Code != http.StatusOK {
		t.Fatalf("GET /v1/family-links/{id}: status = %d: %s", getLink.Code, getLink.Body.String())
	}

	if got := fixture.countByTenant(t, "party.deceased", fixture.tenantA); got != 1 {
		t.Fatalf("party.deceased = %d, esperado 1", got)
	}
	if got := fixture.countByTenant(t, "party.family_link", fixture.tenantA); got != 1 {
		t.Fatalf("party.family_link = %d, esperado 1", got)
	}
	var deceasedAuditCount int
	if err := fixture.adminPool.QueryRow(context.Background(), `
		SELECT count(*) FROM audit.log
		WHERE tenant_id = $1 AND company_id = $2 AND actor_kind = 'user' AND actor_user_id IS NOT NULL
		  AND action = 'party.deceased.register' AND resource_type = 'party.deceased' AND resource_id = $3
		  AND outcome = 'success' AND retention_until = $4 AND trace_id IS NOT NULL
		  AND detail = jsonb_build_object('purpose_code', $5::text, 'legal_basis_code', $6::text)`,
		fixture.tenantA, fixture.companyA, deceasedPayload.ID, fixture.policy.policy.RetentionUntil,
		fixture.policy.policy.PurposeCode, fixture.policy.policy.LegalBasisCode).Scan(&deceasedAuditCount); err != nil {
		t.Fatal(err)
	}
	if deceasedAuditCount != 1 {
		t.Fatalf("trilhas de falecido com escopo, recurso e retenção esperados = %d, esperado 1", deceasedAuditCount)
	}
	var consentCount int
	if err := fixture.adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM privacy.consent_record WHERE tenant_id = $1 AND subject_id = $2 AND evidence_ref = $3`,
		fixture.tenantA, relativeID, testConsentEvidence).Scan(&consentCount); err != nil {
		t.Fatal(err)
	}
	if consentCount != 1 {
		t.Fatalf("privacy.consent_record = %d, esperado 1 com a evidência informada", consentCount)
	}
	var auditCount int
	if err := fixture.adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit.log WHERE tenant_id = $1 AND action = 'privacy.consent.recorded'`,
		fixture.tenantA).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit.log privacy.consent.recorded = %d, esperado 1", auditCount)
	}
	if strings.Contains(createdLink.Body.String(), testConsentEvidence) {
		t.Fatalf("resposta HTTP vazou a referência de evidência de consentimento: %s", createdLink.Body.String())
	}
}

func TestProductionDeceasedFamilyLinkIntegrationDuplicateAndConcurrency(t *testing.T) {
	fixture := setupDeceasedFamilyLinkFixture(t)

	personID := fixture.createPerson(t, fixture.tenantA, "Pedro Duplicado E2E")
	firstDeceased := fixture.do(http.MethodPost, "/v1/deceased", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"deathAt":"2026-11-21T09:00:00Z"}`, personID))
	if firstDeceased.Code != http.StatusCreated {
		t.Fatalf("primeira criação de falecido: status = %d: %s", firstDeceased.Code, firstDeceased.Body.String())
	}
	var firstDeceasedPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(firstDeceased.Body.Bytes(), &firstDeceasedPayload); err != nil {
		t.Fatal(err)
	}

	secondDeceased := fixture.do(http.MethodPost, "/v1/deceased", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"deathAt":"2026-11-22T09:00:00Z"}`, personID))
	if secondDeceased.Code != http.StatusConflict {
		t.Fatalf("segunda criação de falecido: status = %d, esperado 409: %s", secondDeceased.Code, secondDeceased.Body.String())
	}
	if !strings.Contains(secondDeceased.Body.String(), `"code":"DECEASED_ALREADY_EXISTS"`) {
		t.Fatalf("code ausente: %s", secondDeceased.Body.String())
	}
	if got := fixture.countByTenant(t, "party.deceased", fixture.tenantA); got != 1 {
		t.Fatalf("party.deceased = %d, esperado 1 após duplicidade sequencial", got)
	}

	relativeID := fixture.createPerson(t, fixture.tenantA, "Ana Concorrente E2E")
	firstLink := fixture.do(http.MethodPost, "/v1/deceased/"+firstDeceasedPayload.ID+"/family-links", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"relation":"spouse","consentEvidenceRef":"`+testConsentEvidence+`"}`, relativeID))
	if firstLink.Code != http.StatusCreated {
		t.Fatalf("primeiro vínculo: status = %d: %s", firstLink.Code, firstLink.Body.String())
	}
	secondLink := fixture.do(http.MethodPost, "/v1/deceased/"+firstDeceasedPayload.ID+"/family-links", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"relation":"spouse","consentEvidenceRef":"`+testConsentEvidence+`"}`, relativeID))
	if secondLink.Code != http.StatusConflict {
		t.Fatalf("segundo vínculo: status = %d, esperado 409: %s", secondLink.Code, secondLink.Body.String())
	}
	if !strings.Contains(secondLink.Body.String(), `"code":"FAMILY_LINK_ALREADY_EXISTS"`) {
		t.Fatalf("code ausente: %s", secondLink.Body.String())
	}

	racePersonID := fixture.createPerson(t, fixture.tenantA, "Falecido Concorrente E2E")
	assertConcurrentCreate(t, func(index int) *httptest.ResponseRecorder {
		if index == 0 {
			return fixture.do(http.MethodPost, "/v1/deceased", fixture.ownerToken,
				fmt.Sprintf(`{"personId":%q,"deathAt":"2026-11-23T09:00:00Z"}`, racePersonID))
		}
		return fixture.do(http.MethodPost, "/_dev/deceased", "",
			fmt.Sprintf(`{"person_id":%q,"death_at":"2026-11-23T09:00:00Z"}`, racePersonID))
	}, "DECEASED_ALREADY_EXISTS")
	var deceasedID uuid.UUID
	if err := fixture.adminPool.QueryRow(context.Background(),
		`SELECT id FROM party.deceased WHERE tenant_id = $1 AND person_id = $2`,
		fixture.tenantA, racePersonID).Scan(&deceasedID); err != nil {
		t.Fatal(err)
	}
	if got := fixture.countByTenant(t, "party.deceased", fixture.tenantA); got != 2 {
		t.Fatalf("deceased após corrida dev/v1: %d, esperado 2", got)
	}

	raceRelativeID := fixture.createPerson(t, fixture.tenantA, "Familiar Concorrente E2E")
	assertConcurrentCreate(t, func(index int) *httptest.ResponseRecorder {
		if index == 0 {
			return fixture.do(http.MethodPost, "/v1/deceased/"+deceasedID.String()+"/family-links", fixture.ownerToken,
				fmt.Sprintf(`{"personId":%q,"relation":"sibling","consentEvidenceRef":%q}`, raceRelativeID, testConsentEvidence))
		}
		return fixture.do(http.MethodPost, "/_dev/family-links", "",
			fmt.Sprintf(`{"deceased_id":%q,"person_id":%q,"relation":"sibling"}`, deceasedID, raceRelativeID))
	}, "FAMILY_LINK_ALREADY_EXISTS")
	if got := fixture.countByTenant(t, "party.family_link", fixture.tenantA); got != 2 {
		t.Fatalf("family_link após corrida dev/v1: %d, esperado 2", got)
	}
}

func assertConcurrentCreate(t *testing.T, request func(int) *httptest.ResponseRecorder, conflictCode string) {
	t.Helper()
	start := make(chan struct{})
	var responses [2]*httptest.ResponseRecorder
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			responses[i] = request(i)
		}()
	}
	close(start)
	wg.Wait()
	prod, dev := responses[0], responses[1]
	if prod.Code == http.StatusCreated && dev.Code == http.StatusConflict {
		return
	}
	if dev.Code == http.StatusCreated && prod.Code == http.StatusConflict &&
		strings.Contains(prod.Body.String(), `"code":"`+conflictCode+`"`) {
		return
	}
	t.Fatalf("corrida dev/v1: produção=%d %s; desenvolvimento=%d %s", prod.Code, prod.Body, dev.Code, dev.Body)
}

// TestProductionDeceasedFamilyLinkIntegrationCrossTenantInvisible prova
// isolamento de tenant em duas camadas: a API nega a visibilidade de
// falecido e vínculo entre tenants (404, nunca revelando existência); e uma
// consulta direta sob o contexto do outro tenant, sem bypass de RLS, devolve
// zero linhas mesmo sem filtro explícito de tenant_id.
func TestProductionDeceasedFamilyLinkIntegrationCrossTenantInvisible(t *testing.T) {
	fixture := setupDeceasedFamilyLinkFixture(t)

	personID := fixture.createPerson(t, fixture.tenantA, "Isolamento Falecido E2E")
	relativeID := fixture.createPerson(t, fixture.tenantA, "Isolamento Familiar E2E")

	createdDeceased := fixture.do(http.MethodPost, "/v1/deceased", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"deathAt":"2026-11-20T09:00:00Z"}`, personID))
	if createdDeceased.Code != http.StatusCreated {
		t.Fatalf("criação no tenant A: status = %d: %s", createdDeceased.Code, createdDeceased.Body.String())
	}
	var deceasedPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createdDeceased.Body.Bytes(), &deceasedPayload); err != nil {
		t.Fatal(err)
	}
	createdLink := fixture.do(http.MethodPost, "/v1/deceased/"+deceasedPayload.ID+"/family-links", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"relation":"child","consentEvidenceRef":"`+testConsentEvidence+`"}`, relativeID))
	if createdLink.Code != http.StatusCreated {
		t.Fatalf("vínculo no tenant A: status = %d: %s", createdLink.Code, createdLink.Body.String())
	}
	var linkPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createdLink.Body.Bytes(), &linkPayload); err != nil {
		t.Fatal(err)
	}

	crossDeceased := fixture.do(http.MethodGet, "/v1/deceased/"+deceasedPayload.ID, fixture.tenantBToken, "")
	if crossDeceased.Code != http.StatusNotFound {
		t.Fatalf("GET falecido pelo tenant B: status = %d, esperado 404: %s", crossDeceased.Code, crossDeceased.Body.String())
	}
	crossLink := fixture.do(http.MethodGet, "/v1/family-links/"+linkPayload.ID, fixture.tenantBToken, "")
	if crossLink.Code != http.StatusNotFound {
		t.Fatalf("GET vínculo pelo tenant B: status = %d, esperado 404: %s", crossLink.Code, crossLink.Body.String())
	}

	for _, request := range mutationRequests(uuid.MustParse(deceasedPayload.ID), relativeID) {
		rec := fixture.do(http.MethodPost, request.path, fixture.tenantBToken, request.body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s pelo tenant B = %d %s, esperado 404", request.path, rec.Code, rec.Body)
		}
	}
	foreignRelativeID := fixture.createPerson(t, fixture.tenantB, "Familiar de Outro Tenant")
	request := mutationRequests(uuid.MustParse(deceasedPayload.ID), foreignRelativeID)[1]
	if rec := fixture.do(http.MethodPost, request.path, fixture.ownerToken, request.body); rec.Code != http.StatusNotFound {
		t.Fatalf("vínculo com familiar de outro tenant = %d %s, esperado 404", rec.Code, rec.Body)
	}

	ctx := context.Background()
	var countDeceasedA, countDeceasedB, countLinkA, countLinkB int
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.deceased WHERE id = $1`, deceasedPayload.ID).Scan(&countDeceasedA)
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantB), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.deceased WHERE id = $1`, deceasedPayload.ID).Scan(&countDeceasedB)
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.family_link WHERE id = $1`, linkPayload.ID).Scan(&countLinkA)
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantB), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.family_link WHERE id = $1`, linkPayload.ID).Scan(&countLinkB)
	}); err != nil {
		t.Fatal(err)
	}
	if countDeceasedA != 1 || countLinkA != 1 {
		t.Fatalf("RLS do tenant A não viu seus próprios registros: deceased=%d link=%d", countDeceasedA, countLinkA)
	}
	if countDeceasedB != 0 || countLinkB != 0 {
		t.Fatalf("RLS do tenant B viu registros do tenant A: deceased=%d link=%d (deveria ser 0 mesmo sem filtro de tenant_id)", countDeceasedB, countLinkB)
	}
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantB), func(ctx context.Context, tx pgx.Tx) error {
		var consent, auditRows int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM privacy.consent_record), (SELECT count(*) FROM audit.log)`).
			Scan(&consent, &auditRows); err != nil {
			return err
		}
		if consent != 0 || auditRows != 0 {
			return fmt.Errorf("tenant B viu trilhas do tenant A: consent=%d audit=%d", consent, auditRows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func mutationRequests(deceasedID, personID uuid.UUID) []struct{ path, body string } {
	return []struct{ path, body string }{
		{"/v1/deceased", fmt.Sprintf(`{"personId":%q,"deathAt":"2026-11-20T09:00:00Z"}`, personID)},
		{"/v1/deceased/" + deceasedID.String() + "/family-links",
			fmt.Sprintf(`{"personId":%q,"relation":"child","consentEvidenceRef":%q}`, personID, testConsentEvidence)},
	}
}

func TestProductionDeceasedFamilyLinkIntegrationFailsClosedWithoutPolicy(t *testing.T) {
	fixture := setupDeceasedFamilyLinkFixture(t)
	personID := fixture.createPerson(t, fixture.tenantA, "Sem Política Falecido E2E")
	relativeID := fixture.createPerson(t, fixture.tenantA, "Sem Política Familiar E2E")
	deceased, err := fixture.partyRepository.CreateDeceased(db.WithTenant(context.Background(), fixture.tenantA),
		party.DeceasedCreate{PersonID: personID, DeathAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	fixture.policy.err = audit.ErrPolicyUnavailable
	for _, request := range mutationRequests(deceased.ID, relativeID) {
		rec := fixture.do(http.MethodPost, request.path, fixture.ownerToken, request.body)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"PRIVACY_POLICY_UNAVAILABLE"`) {
			t.Fatalf("%s = %d %s, esperado 503 PRIVACY_POLICY_UNAVAILABLE", request.path, rec.Code, rec.Body)
		}
	}
	assertFixtureWriteCounts(t, fixture, 1, 0, 0, 0)
}

func TestProductionDeceasedFamilyLinkIntegrationAtomicRollbackOnAuditFailure(t *testing.T) {
	fixture := setupDeceasedFamilyLinkFixture(t)
	personID := fixture.createPerson(t, fixture.tenantA, "Rollback Falecido E2E")
	relativeID := fixture.createPerson(t, fixture.tenantA, "Rollback Familiar E2E")
	deceased, err := fixture.partyRepository.CreateDeceased(db.WithTenant(context.Background(), fixture.tenantA),
		party.DeceasedCreate{PersonID: personID, DeathAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	// Reject audit inserts for this fixture, after the domain insert and,
	// for family links, after the consent insert.
	const constraint = "issue73_test_audit_failure"
	ctx := context.Background()
	if _, err := fixture.adminPool.Exec(ctx, fmt.Sprintf(
		`ALTER TABLE audit.log ADD CONSTRAINT %s CHECK (tenant_id <> '%s') NOT VALID`,
		constraint, fixture.tenantA)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := fixture.adminPool.Exec(ctx, "ALTER TABLE audit.log DROP CONSTRAINT "+constraint); err != nil {
			t.Errorf("remover restrição de teste: %v", err)
		}
	})
	for _, request := range mutationRequests(deceased.ID, relativeID) {
		rec := fixture.do(http.MethodPost, request.path, fixture.ownerToken, request.body)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"PARTY_UNAVAILABLE"`) {
			t.Fatalf("%s = %d %s, esperado 503 PARTY_UNAVAILABLE", request.path, rec.Code, rec.Body)
		}
		assertFixtureWriteCounts(t, fixture, 1, 0, 0, 0)
	}
}

func assertFixtureWriteCounts(t *testing.T, fixture *deceasedFamilyLinkFixture, deceased, links, consent, auditCount int) {
	t.Helper()
	for table, want := range map[string]int{
		"party.deceased": deceased, "party.family_link": links,
		"privacy.consent_record": consent, "audit.log": auditCount,
	} {
		if got := fixture.countByTenant(t, table, fixture.tenantA); got != want {
			t.Errorf("%s = %d, esperado %d", table, got, want)
		}
	}
}

func TestProductionDeceasedFamilyLinkIntegrationPreservesLegacyDuplicates(t *testing.T) {
	fixture := setupDeceasedFamilyLinkFixture(t)
	personID := fixture.createPerson(t, fixture.tenantA, "Falecido Legado")
	relativeID := fixture.createPerson(t, fixture.tenantA, "Familiar Legado")
	deceasedIDs := []uuid.UUID{uuid.New(), uuid.New()}
	linkIDs := []uuid.UUID{uuid.New(), uuid.New()}
	ctx := context.Background()

	// Seed the duplicates accepted by the schema at 0035 with their own IDs.
	if _, err := fixture.adminPool.Exec(ctx, `
		INSERT INTO party.deceased (tenant_id,person_id,id,death_at)
		VALUES ($1,$2,$3,now()), ($1,$2,$4,now())`,
		fixture.tenantA, personID, deceasedIDs[0], deceasedIDs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.adminPool.Exec(ctx, `
		INSERT INTO party.family_link (tenant_id,deceased_id,person_id,id,relation)
		VALUES ($1,$2,$3,$4,'child'), ($1,$2,$3,$5,'spouse')`,
		fixture.tenantA, deceasedIDs[0], relativeID, linkIDs[0], linkIDs[1]); err != nil {
		t.Fatal(err)
	}

	deceasedRequest := mutationRequests(deceasedIDs[0], personID)[0]
	linkRequest := mutationRequests(deceasedIDs[0], relativeID)[1]
	for _, test := range []struct{ path, body, code string }{
		{deceasedRequest.path, deceasedRequest.body, "DECEASED_ALREADY_EXISTS"},
		{linkRequest.path, linkRequest.body, "FAMILY_LINK_ALREADY_EXISTS"},
	} {
		rec := fixture.do(http.MethodPost, test.path, fixture.ownerToken, test.body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"`+test.code+`"`) {
			t.Fatalf("POST %s = %d %s, esperado 409 %s", test.path, rec.Code, rec.Body, test.code)
		}
	}
	assertFixtureWriteCounts(t, fixture, 2, 2, 0, 0)

	for _, test := range []struct {
		prefix string
		ids    []uuid.UUID
	}{
		{"/v1/deceased/", deceasedIDs},
		{"/v1/family-links/", linkIDs},
	} {
		for _, id := range test.ids {
			rec := fixture.do(http.MethodGet, test.prefix+id.String(), fixture.ownerToken, "")
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"`+id.String()+`"`) {
				t.Fatalf("consulta do ID legado %s = %d %s", id, rec.Code, rec.Body)
			}
		}
	}
}

func TestProductionDeceasedFamilyLinkIntegrationRejectsSelfLink(t *testing.T) {
	fixture := setupDeceasedFamilyLinkFixture(t)
	personID := fixture.createPerson(t, fixture.tenantA, "Falecido sem Autovínculo")
	deceased, err := fixture.partyRepository.CreateDeceased(db.WithTenant(context.Background(), fixture.tenantA),
		party.DeceasedCreate{PersonID: personID, DeathAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	request := mutationRequests(deceased.ID, personID)[1]
	prod := fixture.do(http.MethodPost, request.path, fixture.ownerToken, request.body)
	if prod.Code != http.StatusBadRequest || !strings.Contains(prod.Body.String(), `"code":"VALIDATION_ERROR"`) {
		t.Fatalf("autovínculo v1 = %d %s, esperado 400 VALIDATION_ERROR", prod.Code, prod.Body)
	}
	dev := fixture.do(http.MethodPost, "/_dev/family-links", "",
		fmt.Sprintf(`{"deceased_id":%q,"person_id":%q,"relation":"child"}`, deceased.ID, personID))
	if dev.Code != http.StatusBadRequest {
		t.Fatalf("autovínculo dev = %d %s, esperado 400", dev.Code, dev.Body)
	}
	assertFixtureWriteCounts(t, fixture, 1, 0, 0, 0)
}
