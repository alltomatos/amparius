package party_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// secondSyntheticCNPJ e thirdSyntheticCNPJ são CNPJs sintéticos adicionais
// (AGENTS.md #5), validados em cnpj_test.go, usados para não colidir com
// testSyntheticCNPJ dentro do mesmo tenant em cenários que precisam de mais
// de uma pessoa jurídica.
const (
	secondSyntheticCNPJ = "11222333000181"
	thirdSyntheticCNPJ  = "1234ABCD001198"
)

// legalEntityFixture monta o bootstrap real de /v1/legal-entities e
// /v1/parties/{partyId}/contacts|addresses (issue #78): auth.Service de
// produção, party.Repository real contra PostgreSQL sob o papel de aplicação
// (NOSUPERUSER/NOBYPASSRLS) e um audit.PolicyProvider sintético — não
// aprovado por nenhum DPO real — usado apenas para exercitar o caminho feliz
// do contrato Go/HTTP. O verificador OIDC e o MFA são duplos de teste, como
// no restante da suíte de /v1/parties.
type legalEntityFixture struct {
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

func setupLegalEntityFixture(t *testing.T) *legalEntityFixture {
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
		t.Fatal("teste de pessoa jurídica exige papel de aplicação sem SUPERUSER/BYPASSRLS")
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
		// audit.log é append-only (trigger de banco); o cleanup
		// administrativo de teste precisa desarmar o trigger de usuário só
		// para as linhas dos tenants de teste, nunca em produção (mesmo
		// padrão de internal/platform/audit/integration_test.go).
		tx, cleanupErr := adminPool.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, statement := range []string{
				"ALTER TABLE audit.log DISABLE TRIGGER audit_log_append_only",
				"DELETE FROM audit.log WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.company_representative WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.contact WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.address WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.party_company WHERE tenant_id = ANY($1::uuid[])",
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
			t.Errorf("cleanup administrativo de pessoa jurídica: %v", cleanupErr)
			return
		}
		// Prova de limpeza: nenhuma linha residual em nenhuma das tabelas
		// tocadas por esta fatia permanece associada aos tenants de teste.
		var remainingCompanies, remainingReps, remainingContacts, remainingAddresses, remainingAudit, remainingTenants int
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM party.party_company WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingCompanies); err != nil {
			t.Errorf("confirmar cleanup de party.party_company: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM party.company_representative WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingReps); err != nil {
			t.Errorf("confirmar cleanup de party.company_representative: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM party.contact WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingContacts); err != nil {
			t.Errorf("confirmar cleanup de party.contact: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM party.address WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingAddresses); err != nil {
			t.Errorf("confirmar cleanup de party.address: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM audit.log WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingAudit); err != nil {
			t.Errorf("confirmar cleanup de audit.log: %v", err)
		}
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingTenants); err != nil {
			t.Errorf("confirmar cleanup de identity.tenant: %v", err)
		}
		if remainingCompanies != 0 || remainingReps != 0 || remainingContacts != 0 || remainingAddresses != 0 || remainingAudit != 0 || remainingTenants != 0 {
			t.Errorf("cleanup deixou linhas residuais: party_company=%d company_representative=%d contact=%d address=%d audit=%d tenant=%d",
				remainingCompanies, remainingReps, remainingContacts, remainingAddresses, remainingAudit, remainingTenants)
		} else {
			t.Log("cleanup: zero resíduos em party_company, company_representative, contact, address e audit.log")
		}
	})

	tenantA, err := identityRepository.Onboard(ctx, identity.DevelopmentRequest("legal-entity-a-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, tenantA.TenantID)

	tenantB, err := identityRepository.Onboard(ctx, identity.DevelopmentRequest("legal-entity-b-"+uuid.NewString()))
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
	// identity.create_onboarding (migration 0042) já vincula cada owner à
	// empresa sede em identity.user_company; nenhum INSERT manual é necessário.

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
	// continua com policy nil.
	policyProvider := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode:    "test.legal_entity",
		LegalBasisCode: "test.legitimate_interest",
		RetentionUntil: time.Now().Add(72 * time.Hour),
	}}

	handler := httpserver.New(logger, httpserver.Options{
		Authenticator:            authService,
		DevelopmentMode:          true,
		DevelopmentLegalEntities: party.NewDevelopmentLegalEntityHTTPHandler(partyRepository),
		AuthenticatedRoutes:      party.LegalEntityAuthenticatedRoutes(partyRepository, policyProvider),
	}).Handler()

	return &legalEntityFixture{
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

func (f *legalEntityFixture) do(method, path, bearer, body string) *httptest.ResponseRecorder {
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

// createPerson cria uma pessoa física diretamente pelo repositório real (fora
// do contrato /v1/parties, que não faz parte desta fatia) para servir de
// representante nos testes.
func (f *legalEntityFixture) createPerson(t *testing.T, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	created, err := f.partyRepository.Create(db.WithTenant(context.Background(), tenantID), party.Party{
		Kind: party.KindPerson, Name: name, Status: party.StatusActive,
	})
	if err != nil {
		t.Fatalf("criar pessoa de apoio (%s): %v", name, err)
	}
	return created.ID
}

func (f *legalEntityFixture) countByTenant(t *testing.T, table string, tenantID uuid.UUID) int {
	t.Helper()
	var count int
	if err := f.adminPool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1`, table), tenantID).Scan(&count); err != nil {
		t.Fatalf("contar %s: %v", table, err)
	}
	return count
}

func (f *legalEntityFixture) createLegalEntity(t *testing.T, bearer, legalName, cnpj string) string {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/legal-entities", bearer,
		fmt.Sprintf(`{"legalName":%q,"cnpj":%q}`, legalName, cnpj))
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar pessoa jurídica: status = %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.ID
}

func assertLegalEntityWriteCounts(t *testing.T, fixture *legalEntityFixture, companies, representatives, contacts, addresses, auditCount int) {
	t.Helper()
	for table, want := range map[string]int{
		"party.party_company": companies, "party.company_representative": representatives,
		"party.contact": contacts, "party.address": addresses, "audit.log": auditCount,
	} {
		if got := fixture.countByTenant(t, table, fixture.tenantA); got != want {
			t.Errorf("%s = %d, esperado %d", table, got, want)
		}
	}
}

// TestProductionLegalEntityIntegrationCreateGetContactAddressRepresentative
// prova, com Postgres real, o caminho feliz completo da issue #78: criação
// de pessoa jurídica, contato, endereço e representante, cada um com a
// trilha de auditoria exigida (contato/endereço/representante são dados
// pessoais) gravada na mesma transação, com Detail só com purpose_code e
// legal_basis_code — nunca o valor do contato/endereço.
func TestProductionLegalEntityIntegrationCreateGetContactAddressRepresentative(t *testing.T) {
	fixture := setupLegalEntityFixture(t)

	companyID := fixture.createLegalEntity(t, fixture.ownerToken, "Funerária Esperança LTDA", testSyntheticCNPJ)
	representativePersonID := fixture.createPerson(t, fixture.tenantA, "Carlos Representante E2E")

	createdContact := fixture.do(http.MethodPost, "/v1/parties/"+companyID+"/contacts", fixture.ownerToken,
		`{"kind":"email","value":"contato@esperanca.example.com","primary":true}`)
	if createdContact.Code != http.StatusCreated {
		t.Fatalf("POST contatos: status = %d: %s", createdContact.Code, createdContact.Body.String())
	}
	var contactPayload struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(createdContact.Body.Bytes(), &contactPayload); err != nil {
		t.Fatal(err)
	}

	createdAddress := fixture.do(http.MethodPost, "/v1/parties/"+companyID+"/addresses", fixture.ownerToken,
		`{"street":"Rua das Flores","number":"100","city":"Fortaleza","state":"ce","zip":"60000-000","primary":true}`)
	if createdAddress.Code != http.StatusCreated {
		t.Fatalf("POST endereços: status = %d: %s", createdAddress.Code, createdAddress.Body.String())
	}

	createdRepresentative := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"role":"legal_representative"}`, representativePersonID))
	if createdRepresentative.Code != http.StatusCreated {
		t.Fatalf("POST representantes: status = %d: %s", createdRepresentative.Code, createdRepresentative.Body.String())
	}

	getAggregate := fixture.do(http.MethodGet, "/v1/legal-entities/"+companyID, fixture.ownerToken, "")
	if getAggregate.Code != http.StatusOK {
		t.Fatalf("GET agregado: status = %d: %s", getAggregate.Code, getAggregate.Body.String())
	}
	var aggregate struct {
		CNPJ      string                   `json:"cnpj"`
		Contacts  []struct{ Value string } `json:"contacts"`
		Addresses []struct {
			City  string `json:"city"`
			State string `json:"state"`
			Zip   string `json:"zip"`
		} `json:"addresses"`
		Representatives []struct {
			PersonID string `json:"personId"`
			Role     string `json:"role"`
		} `json:"representatives"`
	}
	if err := json.Unmarshal(getAggregate.Body.Bytes(), &aggregate); err != nil {
		t.Fatal(err)
	}
	if aggregate.CNPJ != testSyntheticCNPJ || len(aggregate.Contacts) != 1 || len(aggregate.Addresses) != 1 || len(aggregate.Representatives) != 1 {
		t.Fatalf("agregado inesperado: %#v", aggregate)
	}
	if aggregate.Addresses[0].State != "CE" || aggregate.Addresses[0].Zip != "60000000" {
		t.Fatalf("endereço deveria normalizar state/zip sem máscara, obteve: %#v", aggregate.Addresses[0])
	}
	if aggregate.Representatives[0].PersonID != representativePersonID.String() || aggregate.Representatives[0].Role != "legal_representative" {
		t.Fatalf("representante inesperado: %#v", aggregate.Representatives[0])
	}

	assertLegalEntityWriteCounts(t, fixture, 1, 1, 1, 1, 3)

	for action, resourceType := range map[string]string{
		"party.contact.add":         "party.contact",
		"party.address.add":         "party.address",
		"party.representative.link": "party.company_representative",
	} {
		var count int
		if err := fixture.adminPool.QueryRow(context.Background(), `
			SELECT count(*) FROM audit.log
			WHERE tenant_id = $1 AND company_id = $2 AND actor_kind = 'user' AND actor_user_id IS NOT NULL
			  AND action = $3 AND resource_type = $4 AND outcome = 'success' AND trace_id IS NOT NULL
			  AND detail = jsonb_build_object('purpose_code', $5::text, 'legal_basis_code', $6::text)`,
			fixture.tenantA, fixture.companyA, action, resourceType,
			fixture.policy.policy.PurposeCode, fixture.policy.policy.LegalBasisCode).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("trilha de auditoria %s com escopo e detail esperados = %d, esperado 1", action, count)
		}
	}
	var detailValues int
	if err := fixture.adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit.log WHERE tenant_id = $1 AND detail::text ILIKE '%esperanca%'`,
		fixture.tenantA).Scan(&detailValues); err != nil {
		t.Fatal(err)
	}
	if detailValues != 0 {
		t.Fatalf("detail da auditoria vazou valor de contato/endereço")
	}
}

// TestProductionLegalEntityIntegrationDuplicateCNPJAndConcurrency prova que
// uma segunda criação com o mesmo CNPJ no tenant é rejeitada, sequencialmente
// e sob corrida entre a rota de produção e a rota dev (mesmo advisory lock
// lockPartyCreate("legal_entity", ...) do issue #72).
func TestProductionLegalEntityIntegrationDuplicateCNPJAndConcurrency(t *testing.T) {
	fixture := setupLegalEntityFixture(t)

	fixture.createLegalEntity(t, fixture.ownerToken, "Primeira Jurídica E2E", testSyntheticCNPJ)
	second := fixture.do(http.MethodPost, "/v1/legal-entities", fixture.ownerToken,
		fmt.Sprintf(`{"legalName":"Segunda Jurídica E2E","cnpj":%q}`, testSyntheticCNPJ))
	if second.Code != http.StatusConflict {
		t.Fatalf("segunda criação: status = %d, esperado 409: %s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), `"code":"LEGAL_ENTITY_ALREADY_EXISTS"`) {
		t.Fatalf("code ausente: %s", second.Body.String())
	}
	if got := fixture.countByTenant(t, "party.party_company", fixture.tenantA); got != 1 {
		t.Fatalf("party.party_company = %d, esperado 1 após duplicidade sequencial", got)
	}

	assertConcurrentCreate(t, func(index int) *httptest.ResponseRecorder {
		if index == 0 {
			return fixture.do(http.MethodPost, "/v1/legal-entities", fixture.ownerToken,
				fmt.Sprintf(`{"legalName":"Corrida Produção E2E","cnpj":%q}`, secondSyntheticCNPJ))
		}
		return fixture.do(http.MethodPost, "/_dev/legal-entities", "",
			fmt.Sprintf(`{"legalName":"Corrida Dev E2E","cnpj":%q}`, secondSyntheticCNPJ))
	}, "LEGAL_ENTITY_ALREADY_EXISTS")
	if got := fixture.countByTenant(t, "party.party_company", fixture.tenantA); got != 2 {
		t.Fatalf("party.party_company após corrida dev/v1: %d, esperado 2", got)
	}
}

// TestProductionLegalEntityIntegrationCrossTenantInvisible prova isolamento
// de tenant em duas camadas: a API nega a visibilidade entre tenants (404,
// nunca revelando existência) para a pessoa jurídica, seus contatos,
// endereços e representantes; e uma consulta direta sob o contexto do outro
// tenant, sem bypass de RLS, devolve zero linhas mesmo sem filtro explícito
// de tenant_id.
func TestProductionLegalEntityIntegrationCrossTenantInvisible(t *testing.T) {
	fixture := setupLegalEntityFixture(t)

	companyID := fixture.createLegalEntity(t, fixture.ownerToken, "Isolamento Jurídica E2E", testSyntheticCNPJ)
	representativePersonID := fixture.createPerson(t, fixture.tenantA, "Isolamento Representante E2E")

	createdRepresentative := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"role":"administrator"}`, representativePersonID))
	if createdRepresentative.Code != http.StatusCreated {
		t.Fatalf("vínculo no tenant A: status = %d: %s", createdRepresentative.Code, createdRepresentative.Body.String())
	}

	crossGet := fixture.do(http.MethodGet, "/v1/legal-entities/"+companyID, fixture.tenantBToken, "")
	if crossGet.Code != http.StatusNotFound {
		t.Fatalf("GET pessoa jurídica pelo tenant B: status = %d, esperado 404: %s", crossGet.Code, crossGet.Body.String())
	}

	crossContact := fixture.do(http.MethodPost, "/v1/parties/"+companyID+"/contacts", fixture.tenantBToken,
		`{"kind":"email","value":"cross@example.com"}`)
	if crossContact.Code != http.StatusNotFound {
		t.Fatalf("POST contato pelo tenant B: status = %d, esperado 404: %s", crossContact.Code, crossContact.Body.String())
	}
	crossAddress := fixture.do(http.MethodPost, "/v1/parties/"+companyID+"/addresses", fixture.tenantBToken,
		`{"street":"Rua Cross","number":"1","city":"Fortaleza","state":"CE","zip":"60000000"}`)
	if crossAddress.Code != http.StatusNotFound {
		t.Fatalf("POST endereço pelo tenant B: status = %d, esperado 404: %s", crossAddress.Code, crossAddress.Body.String())
	}
	foreignPersonID := fixture.createPerson(t, fixture.tenantB, "Pessoa de Outro Tenant")
	crossRepresentative := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.tenantBToken,
		fmt.Sprintf(`{"personId":%q,"role":"administrator"}`, foreignPersonID))
	if crossRepresentative.Code != http.StatusNotFound {
		t.Fatalf("POST representante pelo tenant B: status = %d, esperado 404: %s", crossRepresentative.Code, crossRepresentative.Body.String())
	}

	// Representante com pessoa de outro tenant, pelo tenant A: 404 sem
	// revelar que a pessoa existe no tenant B.
	crossPersonFromA := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"role":"contact"}`, foreignPersonID))
	if crossPersonFromA.Code != http.StatusNotFound {
		t.Fatalf("vínculo com pessoa de outro tenant = %d %s, esperado 404", crossPersonFromA.Code, crossPersonFromA.Body)
	}

	ctx := context.Background()
	var countCompanyA, countCompanyB, countRepA, countRepB int
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.party_company WHERE party_id = $1`, companyID).Scan(&countCompanyA)
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantB), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.party_company WHERE party_id = $1`, companyID).Scan(&countCompanyB)
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.company_representative WHERE company_party_id = $1`, companyID).Scan(&countRepA)
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(db.WithTenant(ctx, fixture.tenantB), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.company_representative WHERE company_party_id = $1`, companyID).Scan(&countRepB)
	}); err != nil {
		t.Fatal(err)
	}
	if countCompanyA != 1 || countRepA != 1 {
		t.Fatalf("RLS do tenant A não viu seus próprios registros: party_company=%d company_representative=%d", countCompanyA, countRepA)
	}
	if countCompanyB != 0 || countRepB != 0 {
		t.Fatalf("RLS do tenant B viu registros do tenant A: party_company=%d company_representative=%d (deveria ser 0 mesmo sem filtro de tenant_id)", countCompanyB, countRepB)
	}
}

// TestProductionLegalEntityIntegrationRepresentativeErrorMappings prova,
// contra Postgres real, os três mapeamentos de erro do vínculo de
// representante: pessoa de outro tenant (404 NOT_FOUND, sem revelar
// existência), pessoa jurídica oferecida como representante (409
// PARTY_KIND_INVALID) e duplicidade de (empresa, pessoa, papel) (409
// REPRESENTATIVE_ALREADY_EXISTS).
func TestProductionLegalEntityIntegrationRepresentativeErrorMappings(t *testing.T) {
	fixture := setupLegalEntityFixture(t)
	companyID := fixture.createLegalEntity(t, fixture.ownerToken, "Erros Representante E2E", testSyntheticCNPJ)

	foreignPersonID := fixture.createPerson(t, fixture.tenantB, "Pessoa de Outro Tenant E2E")
	notFound := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"role":"contact"}`, foreignPersonID))
	if notFound.Code != http.StatusNotFound || !strings.Contains(notFound.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("pessoa de outro tenant = %d %s, esperado 404 NOT_FOUND", notFound.Code, notFound.Body)
	}

	otherCompanyID := fixture.createLegalEntity(t, fixture.ownerToken, "Outra Jurídica Como Representante E2E", secondSyntheticCNPJ)
	kindInvalid := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"role":"contact"}`, otherCompanyID))
	if kindInvalid.Code != http.StatusConflict || !strings.Contains(kindInvalid.Body.String(), `"code":"PARTY_KIND_INVALID"`) {
		t.Fatalf("pessoa jurídica como representante = %d %s, esperado 409 PARTY_KIND_INVALID", kindInvalid.Code, kindInvalid.Body)
	}

	personID := fixture.createPerson(t, fixture.tenantA, "Representante Duplicado E2E")
	first := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"role":"attorney"}`, personID))
	if first.Code != http.StatusCreated {
		t.Fatalf("primeiro vínculo: status = %d: %s", first.Code, first.Body.String())
	}
	duplicate := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"role":"attorney"}`, personID))
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), `"code":"REPRESENTATIVE_ALREADY_EXISTS"`) {
		t.Fatalf("vínculo duplicado = %d %s, esperado 409 REPRESENTATIVE_ALREADY_EXISTS", duplicate.Code, duplicate.Body)
	}

	// O mesmo par (empresa, pessoa) com outro papel não é duplicidade.
	otherRole := fixture.do(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", fixture.ownerToken,
		fmt.Sprintf(`{"personId":%q,"role":"administrator"}`, personID))
	if otherRole.Code != http.StatusCreated {
		t.Fatalf("vínculo com outro papel: status = %d, esperado 201: %s", otherRole.Code, otherRole.Body.String())
	}
}

func TestProductionLegalEntityIntegrationLegacyCompanySearchAndLink(t *testing.T) {
	fixture := setupLegalEntityFixture(t)
	const name = "Empresa Legada Sintética"
	const legacyDocument = "12.34a.bcD/0011-98"
	ctx := context.Background()

	insertLegacy := func(name, document string) string {
		t.Helper()
		var id string
		if err := fixture.adminPool.QueryRow(ctx, `
			INSERT INTO party.party (tenant_id, kind, name, document, status)
			VALUES ($1, 'company', $2, $3, 'active') RETURNING id`,
			fixture.tenantA, name, document).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id := insertLegacy(name, legacyDocument)
	redactedID := insertLegacy("Empresa com documento ocultado", "12.***.***/0011-98")

	for _, endpoint := range []struct{ path, bearer string }{
		{"/v1/legal-entities", fixture.ownerToken}, {"/_dev/legal-entities", ""},
	} {
		for _, query := range []string{name, thirdSyntheticCNPJ, legacyDocument} {
			rec := fixture.do(http.MethodGet, endpoint.path+"?q="+url.QueryEscape(query)+"&limit=1", endpoint.bearer, "")
			var payload struct {
				Items []struct{ ID, CNPJ string }
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &payload) != nil ||
				len(payload.Items) != 1 || payload.Items[0].ID != id || payload.Items[0].CNPJ != legacyDocument {
				t.Fatalf("GET %s com consulta %q = %d %s", endpoint.path, query, rec.Code, rec.Body)
			}
		}
	}
	invisible := fixture.do(http.MethodGet, "/v1/legal-entities?q="+thirdSyntheticCNPJ, fixture.tenantBToken, "")
	if invisible.Code != http.StatusOK || !strings.Contains(invisible.Body.String(), `"items":[]`) {
		t.Fatalf("busca em outro tenant = %d %s", invisible.Code, invisible.Body)
	}

	detail := fixture.do(http.MethodGet, "/v1/legal-entities/"+id, fixture.ownerToken, "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), legacyDocument) {
		t.Fatalf("detalhe legado = %d %s", detail.Code, detail.Body)
	}

	for _, endpoint := range []struct{ path, bearer string }{
		{"/v1/legal-entities", fixture.ownerToken}, {"/_dev/legal-entities", ""},
	} {
		duplicate := fixture.do(http.MethodPost, endpoint.path, endpoint.bearer,
			fmt.Sprintf(`{"legalName":"Duplicada","cnpj":%q}`, thirdSyntheticCNPJ))
		if duplicate.Code != http.StatusConflict {
			t.Fatalf("duplicação em %s = %d %s", endpoint.path, duplicate.Code, duplicate.Body)
		}
	}

	personID := fixture.createPerson(t, fixture.tenantA, "Representante PF de Teste")
	body := fmt.Sprintf(`{"personId":%q,"role":"administrator"}`, personID)
	redacted := fixture.do(http.MethodPost, "/v1/legal-entities/"+redactedID+"/representatives", fixture.ownerToken, body)
	if redacted.Code != http.StatusBadRequest || !strings.Contains(redacted.Body.String(), "VALIDATION_ERROR") {
		t.Fatalf("vínculo com documento ocultado = %d %s", redacted.Code, redacted.Body)
	}
	fixture.policy.err = audit.ErrPolicyUnavailable
	denied := fixture.do(http.MethodPost, "/v1/legal-entities/"+id+"/representatives", fixture.ownerToken, body)
	if denied.Code != http.StatusServiceUnavailable || !strings.Contains(denied.Body.String(), "PRIVACY_POLICY_UNAVAILABLE") {
		t.Fatalf("vínculo sem política = %d %s", denied.Code, denied.Body)
	}
	assertLegalEntityWriteCounts(t, fixture, 0, 0, 0, 0, 0)
	fixture.policy.err = nil
	linked := fixture.do(http.MethodPost, "/v1/legal-entities/"+id+"/representatives", fixture.ownerToken, body)
	if linked.Code != http.StatusCreated || !strings.Contains(linked.Body.String(), personID.String()) {
		t.Fatalf("vínculo em PJ legada = %d %s", linked.Code, linked.Body)
	}
	assertLegalEntityWriteCounts(t, fixture, 1, 1, 0, 0, 1)

	var storedDocument string
	var emptyExtension bool
	if err := fixture.adminPool.QueryRow(ctx, `
		SELECT p.document, pc.trade_name IS NULL AND pc.state_registration IS NULL AND pc.municipal_registration IS NULL
		FROM party.party p JOIN party.party_company pc ON pc.tenant_id = p.tenant_id AND pc.party_id = p.id
		WHERE p.tenant_id = $1 AND p.id = $2`, fixture.tenantA, id).Scan(&storedDocument, &emptyExtension); err != nil {
		t.Fatal(err)
	}
	if storedDocument != legacyDocument || !emptyExtension {
		t.Fatal("vínculo deve preservar o CNPJ armazenado e criar só a extensão vazia")
	}
}

// TestProductionLegalEntityIntegrationPrimarySwapIsAtomic prova que marcar um
// novo contato (por party+kind) ou endereço (por party) como principal
// rebaixa o anterior na mesma transação, nunca deixando dois principais
// simultâneos.
func TestProductionLegalEntityIntegrationPrimarySwapIsAtomic(t *testing.T) {
	fixture := setupLegalEntityFixture(t)
	companyID := fixture.createLegalEntity(t, fixture.ownerToken, "Troca de Principal E2E", testSyntheticCNPJ)

	for _, check := range []struct {
		endpoint, table, body string
	}{
		{"contacts", "party.contact", `{"kind":"email","value":"contato%d@example.test","primary":true}`},
		{"addresses", "party.address", `{"street":"Rua %d","number":"1","city":"Fortaleza","state":"CE","zip":"60000000","primary":true}`},
	} {
		t.Run(check.endpoint, func(t *testing.T) {
			path := "/parties/" + companyID + "/" + check.endpoint
			first := fixture.do(http.MethodPost, "/v1"+path, fixture.ownerToken, fmt.Sprintf(check.body, 0))
			if first.Code != http.StatusCreated {
				t.Fatalf("criação inicial = %d %s", first.Code, first.Body)
			}

			start := make(chan struct{})
			var responses [2]*httptest.ResponseRecorder
			var workers sync.WaitGroup
			workers.Add(2)
			go func() {
				defer workers.Done()
				<-start
				responses[0] = fixture.do(http.MethodPost, "/v1"+path, fixture.ownerToken, fmt.Sprintf(check.body, 1))
			}()
			go func() {
				defer workers.Done()
				<-start
				responses[1] = fixture.do(http.MethodPost, "/_dev"+path, "", fmt.Sprintf(check.body, 2))
			}()
			close(start)
			workers.Wait()

			for _, response := range responses {
				if response.Code != http.StatusCreated {
					t.Fatalf("criação concorrente = %d %s", response.Code, response.Body)
				}
			}
			var total, primary int
			if err := fixture.adminPool.QueryRow(context.Background(), `
				SELECT count(*), count(*) FILTER (WHERE "primary") FROM `+check.table+`
				WHERE tenant_id = $1 AND party_id = $2`, fixture.tenantA, companyID).Scan(&total, &primary); err != nil {
				t.Fatal(err)
			}
			if total != 3 || primary != 1 {
				t.Fatalf("após troca concorrente = %d registros, %d principais", total, primary)
			}
		})
	}
}

// TestProductionLegalEntityIntegrationFailsClosedWithoutPolicy prova que, sem
// política do DPO resolvida, os três POSTs de dados pessoais (contato,
// endereço, representante) respondem 503 PRIVACY_POLICY_UNAVAILABLE sem
// tocar o repositório — nenhuma linha é gravada.
func TestProductionLegalEntityIntegrationFailsClosedWithoutPolicy(t *testing.T) {
	fixture := setupLegalEntityFixture(t)
	companyID := fixture.createLegalEntity(t, fixture.ownerToken, "Sem Política E2E", testSyntheticCNPJ)
	personID := fixture.createPerson(t, fixture.tenantA, "Sem Política Representante E2E")

	fixture.policy.err = audit.ErrPolicyUnavailable
	for _, request := range legalEntityIntegrationMutationRequests(companyID, personID) {
		rec := fixture.do(http.MethodPost, request.path, fixture.ownerToken, request.body)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"PRIVACY_POLICY_UNAVAILABLE"`) {
			t.Fatalf("%s = %d %s, esperado 503 PRIVACY_POLICY_UNAVAILABLE", request.path, rec.Code, rec.Body)
		}
	}
	assertLegalEntityWriteCounts(t, fixture, 1, 0, 0, 0, 0)
}

// TestProductionLegalEntityIntegrationAtomicRollbackOnAuditFailure prova que,
// quando a gravação da auditoria falha, o contato/endereço/representante não
// permanece gravado — tudo ou nada na mesma transação.
func TestProductionLegalEntityIntegrationAtomicRollbackOnAuditFailure(t *testing.T) {
	fixture := setupLegalEntityFixture(t)
	companyID := fixture.createLegalEntity(t, fixture.ownerToken, "Rollback E2E", testSyntheticCNPJ)
	personID := fixture.createPerson(t, fixture.tenantA, "Rollback Representante E2E")

	const constraint = "issue78_test_audit_failure"
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
	for _, request := range legalEntityIntegrationMutationRequests(companyID, personID) {
		rec := fixture.do(http.MethodPost, request.path, fixture.ownerToken, request.body)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"PARTY_UNAVAILABLE"`) {
			t.Fatalf("%s = %d %s, esperado 503 PARTY_UNAVAILABLE", request.path, rec.Code, rec.Body)
		}
	}
	assertLegalEntityWriteCounts(t, fixture, 1, 0, 0, 0, 0)
}

func legalEntityIntegrationMutationRequests(companyID string, personID uuid.UUID) []struct{ path, body string } {
	return []struct{ path, body string }{
		{"/v1/parties/" + companyID + "/contacts", `{"kind":"email","value":"mutacao@example.com"}`},
		{"/v1/parties/" + companyID + "/addresses", `{"street":"Rua Mutação","number":"1","city":"Fortaleza","state":"CE","zip":"60000000"}`},
		{"/v1/legal-entities/" + companyID + "/representatives", fmt.Sprintf(`{"personId":%q,"role":"contact"}`, personID)},
	}
}
