package funeral_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFuneralJourneyProductionHTTPPostgres(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN são necessários")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)

	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)

	var tenants []uuid.UUID
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		funeralTables := []string{
			"funeral.journey_mutation", "funeral.contract_status_history", "funeral.contract_installment",
			"funeral.contract_family_member", "funeral.contract_item", "funeral.contract",
			"funeral.schedule_status_history", "funeral.schedule_mutation", "funeral.schedule",
			"funeral.service_order_status_history", "funeral.service_order_mutation", "funeral.service_order",
			"funeral.location", "funeral.resource", "outbox.event",
		}
		disable := make([]string, 0, len(funeralTables))
		enable := make([]string, 0, len(funeralTables))
		for _, tbl := range funeralTables {
			disable = append(disable, "ALTER TABLE "+tbl+" DISABLE TRIGGER ALL")
			enable = append(enable, "ALTER TABLE "+tbl+" ENABLE TRIGGER ALL")
		}
		for _, stmt := range disable {
			if _, err := admin.Exec(cleanupCtx, stmt); err != nil {
				t.Logf("desabilitar trigger: %v", err)
			}
		}
		tx, err := admin.Begin(cleanupCtx)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = tx.Rollback(cleanupCtx) }()
		for _, statement := range []string{
			"DELETE FROM funeral.journey_mutation WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.contract_status_history WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.contract_installment WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.contract_family_member WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.contract_item WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.contract WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.schedule_status_history WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.service_order_status_history WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.schedule WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.service_order WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.location WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM outbox.event WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.family_link WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.deceased WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.person WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.party WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM catalog.catalog_item WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.user_company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.user_role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.onboarding_request WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.app_user WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.establishment WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
		} {
			if _, err := tx.Exec(cleanupCtx, statement, tenants); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
				return
			}
		}
		if err := tx.Commit(cleanupCtx); err != nil {
			t.Error(err)
		}
		for _, stmt := range enable {
			if _, err := admin.Exec(cleanupCtx, stmt); err != nil {
				t.Logf("reabilitar trigger: %v", err)
			}
		}
	})

	outboxRepo, err := outbox.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := funeral.NewRepository(database, outboxRepo)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}

	onboard := func(role string) auth.OIDCIdentity {
		t.Helper()
		result, err := identities.Onboard(ctx, identity.DevelopmentRequest("fun76-"+uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, result.TenantID)
		subject := "funeral-" + uuid.NewString()
		if _, err := admin.Exec(ctx, `UPDATE identity.app_user SET auth_subject=$1, status='active', mfa_enabled=true WHERE id=$2`, subject, result.OwnerUserID); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `INSERT INTO identity.user_company (tenant_id, user_id, company_id) VALUES ($1,$2,$3)`, result.TenantID, result.OwnerUserID, result.CompanyID); err != nil {
			t.Fatal(err)
		}
		return auth.OIDCIdentity{Issuer: "funeral-test-issuer", Subject: subject, TenantID: result.TenantID, CompanyID: result.CompanyID}
	}

	ownerA := onboard("owner")
	ownerB := onboard("owner")
	tokens := map[string]auth.OIDCIdentity{"owner-a": ownerA, "owner-b": ownerB}
	invalidScope := ownerA
	invalidScope.CompanyID = ownerB.CompanyID
	tokens["invalid-scope"] = invalidScope

	oidc := auth.OIDCVerifierFunc(func(_ context.Context, token string) (auth.OIDCIdentity, error) {
		if claims, found := tokens[token]; found {
			return claims, nil
		}
		return auth.OIDCIdentity{}, auth.ErrInvalidOIDCToken
	})
	mfa := auth.MFAVerifierFunc(func(context.Context, auth.OIDCIdentity) (bool, error) { return true, nil })
	policy, err := auth.DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gate, err := auth.NewService(oidc, mfa, identities, identities, policy, logger)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpserver.New(logger, httpserver.Options{
		Authenticator:       gate,
		AuthenticatedRoutes: funeral.AuthenticatedRoutes(repo),
	}).Handler()

	scoped := func(identity auth.OIDCIdentity) context.Context {
		return db.WithCompany(db.WithTenant(ctx, identity.TenantID), identity.CompanyID)
	}

	setupTenant := func(identity auth.OIDCIdentity) (contractID, locationID uuid.UUID) {
		t.Helper()
		customer := uuid.New()
		deceasedParty := uuid.New()
		deceased := uuid.New()
		item := uuid.New()
		for _, partyID := range []uuid.UUID{customer, deceasedParty} {
			if _, err := admin.Exec(ctx, `INSERT INTO party.party (id,tenant_id,kind,name,status) VALUES ($1,$2,'person','Pessoa','active')`, partyID, identity.TenantID); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.Exec(ctx, `INSERT INTO party.person (party_id,tenant_id,gender) VALUES ($1,$2,'unspecified')`, partyID, identity.TenantID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := admin.Exec(ctx, `INSERT INTO party.deceased (id,tenant_id,person_id,death_at) VALUES ($1,$2,$3,now())`, deceased, identity.TenantID, deceasedParty); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `INSERT INTO catalog.catalog_item (id,tenant_id,kind,name,unit,active) VALUES ($1,$2,'service','Serviço','service',true)`, item, identity.TenantID); err != nil {
			t.Fatal(err)
		}

		location, err := repo.CreateLocation(scoped(identity), funeral.LocationCreate{
			CompanyID: identity.CompanyID, Name: "Capela", Kind: "wake", TimeZone: "America/Fortaleza", IdempotencyKey: "loc-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatal(err)
		}
		contract, err := repo.CreateContract(scoped(identity), funeral.ContractCreate{
			CompanyID: identity.CompanyID, CustomerPartyID: customer, DeceasedID: deceased, IdempotencyKey: "contract-" + uuid.NewString(),
			Items: []funeral.ContractItemCreate{{CatalogItemID: item, Quantity: 1, UnitAmountCents: 10000}},
			PaymentPlan: &funeral.ContractPaymentPlan{InstallmentCount: 3, FirstDueDate: time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC), IntervalMonths: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ChangeContractStatus(scoped(identity), contract.ID, "active", nil); err != nil {
			t.Fatal(err)
		}
		return contract.ID, location.ID
	}

	contractA, locationA := setupTenant(ownerA)
	_ = locationA
	contractB, _ := setupTenant(ownerB)
	_ = contractB

	journeyBody := `{"service_order":{"kind":"wake","description":"atendimento operacional","scheduled_for":"2026-11-25T12:00:00-03:00"},"schedule":{"kind":"wake","location_id":"%s","starts_at":"2026-11-25T12:00:00-03:00","ends_at":"2026-11-25T14:00:00-03:00","time_zone":"America/Fortaleza"}}`

	post := func(token, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/funeral/contracts/"+contractA.String()+"/journey", strings.NewReader(body)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+token)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	t.Run("jornada exige autenticação e funeral:write", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/funeral/contracts/"+contractA.String()+"/journey", strings.NewReader(journeyBody)).WithContext(ctx)
		req.Header.Set("Idempotency-Key", "k1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "AUTHENTICATION_REQUIRED") {
			t.Fatalf("sem autenticação: %d %s", rec.Code, rec.Body)
		}
		restricted, err := auth.NewRolePolicy(map[string][]auth.Permission{"owner": {auth.PermissionPartyWrite}})
		if err != nil {
			t.Fatal(err)
		}
		withoutFuneral, err := auth.NewService(oidc, mfa, identities, identities, restricted, logger)
		if err != nil {
			t.Fatal(err)
		}
		denied := httpserver.New(logger, httpserver.Options{Authenticator: withoutFuneral, AuthenticatedRoutes: funeral.AuthenticatedRoutes(repo)}).Handler()
		req2 := httptest.NewRequest(http.MethodPost, "/v1/funeral/contracts/"+contractA.String()+"/journey", strings.NewReader(journeyBody)).WithContext(ctx)
		req2.Header.Set("Authorization", "Bearer owner-a")
		req2.Header.Set("Idempotency-Key", "k1")
		rec2 := httptest.NewRecorder()
		denied.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusForbidden || !strings.Contains(rec2.Body.String(), "PERMISSION_DENIED") {
			t.Fatalf("sem permissão: %d %s", rec2.Code, rec2.Body)
		}
	})

	t.Run("cria jornada e replay devolve o mesmo id", func(t *testing.T) {
		body := fmtBody(t, journeyBody, locationA)
		first := post("owner-a", "journey-k1", body)
		if first.Code != http.StatusCreated {
			t.Fatalf("jornada: %d %s", first.Code, first.Body)
		}
		var journey struct {
			ID           string `json:"id"`
			ContractID   string `json:"contract_id"`
			ServiceOrder struct {
				ID string `json:"id"`
			} `json:"service_order"`
			Schedule struct {
				ID string `json:"id"`
			} `json:"schedule"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &journey); err != nil || journey.ID == "" || journey.ServiceOrder.ID == "" || journey.Schedule.ID == "" {
			t.Fatalf("jornada inválida: %s", first.Body)
		}
		if first.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("Cache-Control ausente")
		}
		replay := post("owner-a", "journey-k1", body)
		if replay.Code != http.StatusCreated {
			t.Fatalf("replay: %d %s", replay.Code, replay.Body)
		}
		var replayJourney struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(replay.Body.Bytes(), &replayJourney)
		if replayJourney.ID != journey.ID {
			t.Fatalf("replay com id diferente: %s vs %s", replayJourney.ID, journey.ID)
		}
		var mutations int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM funeral.journey_mutation WHERE tenant_id=$1 AND contract_id=$2`, ownerA.TenantID, contractA).Scan(&mutations); err != nil {
			t.Fatal(err)
		}
		if mutations != 1 {
			t.Fatalf("esperada exatamente uma mutação de jornada, obtido %d", mutations)
		}
	})

	t.Run("contrato de outro tenant é referência não encontrada", func(t *testing.T) {
		body := fmtBody(t, journeyBody, locationA)
		req := httptest.NewRequest(http.MethodPost, "/v1/funeral/contracts/"+contractB.String()+"/journey", strings.NewReader(body)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer owner-a")
		req.Header.Set("Idempotency-Key", "foreign-k1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "JOURNEY_REFERENCE_NOT_FOUND") {
			t.Fatalf("contrato estrangeiro: %d %s", rec.Code, rec.Body)
		}
	})
}

func fmtBody(t *testing.T, tmpl string, locationID uuid.UUID) string {
	t.Helper()
	return strings.ReplaceAll(tmpl, "%s", locationID.String())
}
