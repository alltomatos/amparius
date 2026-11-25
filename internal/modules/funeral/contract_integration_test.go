package funeral_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFuneralContractHTTPIntegrationRLSIdempotencyAndCleanup(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	baseURL, err := url.Parse(dsn)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	appDSN := baseURL.String()
	var role string
	var superuser, bypass bool
	if err := admin.QueryRow(ctx, `SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&role, &superuser, &bypass); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	// The admin DSN may be superuser; the application DSN must be the
	// dedicated NOSUPERUSER/NOBYPASSRLS role used to prove isolation.
	appDatabase, err := db.New(ctx, appDSN)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if err := appDatabase.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper, rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &superuser, &bypass)
	}); err != nil {
		appDatabase.Close()
		admin.Close()
		t.Fatal(err)
	}
	if superuser || bypass {
		appDatabase.Close()
		admin.Close()
		t.Fatalf("teste precisa de role NOSUPERUSER/NOBYPASSRLS; role=%s superuser=%v bypass=%v", role, superuser, bypass)
	}

	tenantA, tenantB := uuid.New(), uuid.New()
	companyA, companyB := uuid.New(), uuid.New()
	customerA, deceasedPartyA, relativeA := uuid.New(), uuid.New(), uuid.New()
	deceasedA, familyLinkA, itemA := uuid.New(), uuid.New(), uuid.New()
	customerB, deceasedPartyB, relativeB := uuid.New(), uuid.New(), uuid.New()
	deceasedB, familyLinkB, itemB := uuid.New(), uuid.New(), uuid.New()
	tenantIDs := []uuid.UUID{tenantA, tenantB}
	cleanup := func() error {
		cleanupCtx := context.Background()
		tx, err := admin.Begin(cleanupCtx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(cleanupCtx) }()
		for _, statement := range []string{
			"ALTER TABLE funeral.contract_status_history DISABLE TRIGGER contract_status_append_only",
			"ALTER TABLE funeral.contract_family_member DISABLE TRIGGER contract_family_append_only",
			"ALTER TABLE funeral.contract_item DISABLE TRIGGER contract_item_append_only",
			"ALTER TABLE funeral.contract DISABLE TRIGGER contract_history_preserve",
			"ALTER TABLE outbox.event_attempt DISABLE TRIGGER event_attempt_append_only",
			"ALTER TABLE outbox.delivery DISABLE TRIGGER delivery_append_only",
			"ALTER TABLE outbox.event DISABLE TRIGGER event_history_preserve",
			"DELETE FROM funeral.contract_status_history WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.contract_family_member WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.contract_item WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM funeral.contract WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM outbox.delivery WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM outbox.event_attempt WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM outbox.event WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.family_link WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.deceased WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.person WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM party.party WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM catalog.catalog_item WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
			"ALTER TABLE funeral.contract_status_history ENABLE TRIGGER contract_status_append_only",
			"ALTER TABLE funeral.contract_family_member ENABLE TRIGGER contract_family_append_only",
			"ALTER TABLE funeral.contract_item ENABLE TRIGGER contract_item_append_only",
			"ALTER TABLE funeral.contract ENABLE TRIGGER contract_history_preserve",
			"ALTER TABLE outbox.event_attempt ENABLE TRIGGER event_attempt_append_only",
			"ALTER TABLE outbox.delivery ENABLE TRIGGER delivery_append_only",
			"ALTER TABLE outbox.event ENABLE TRIGGER event_history_preserve",
		} {
			if strings.HasPrefix(statement, "ALTER TABLE") {
				_, err = tx.Exec(cleanupCtx, statement)
			} else {
				_, err = tx.Exec(cleanupCtx, statement, tenantIDs)
			}
			if err != nil {
				return err
			}
		}
		return tx.Commit(cleanupCtx)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("cleanup administrativo do contrato funerario: %v", err)
		}
		var remaining int
		if err := admin.QueryRow(context.Background(), `SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, tenantIDs).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup do contrato funerario: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup deixou %d tenant(s) residuais", remaining)
		}
		appDatabase.Close()
		admin.Close()
	})

	setup := func(tenant, company, customer, deceasedParty, relative, deceased, familyLink, item uuid.UUID) error {
		_, err := admin.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, tenant, "Funeral "+tenant.String(), "funeral-"+tenant.String())
		if err != nil {
			return err
		}
		if _, err := admin.Exec(ctx, `INSERT INTO identity.company (id,tenant_id,document,name,kind,status) VALUES ($1,$2,$3,'Empresa Funeral','funeral','active')`, company, tenant, "company-"+company.String()); err != nil {
			return err
		}
		for _, partyID := range []uuid.UUID{customer, deceasedParty, relative} {
			if _, err := admin.Exec(ctx, `INSERT INTO party.party (id,tenant_id,kind,name,status) VALUES ($1,$2,'person','Pessoa de teste','active')`, partyID, tenant); err != nil {
				return err
			}
			if _, err := admin.Exec(ctx, `INSERT INTO party.person (party_id,tenant_id,gender) VALUES ($1,$2,'unspecified')`, partyID, tenant); err != nil {
				return err
			}
		}
		if _, err := admin.Exec(ctx, `INSERT INTO party.deceased (id,tenant_id,person_id,death_at) VALUES ($1,$2,$3,now())`, deceased, tenant, deceasedParty); err != nil {
			return err
		}
		if _, err := admin.Exec(ctx, `INSERT INTO party.family_link (id,tenant_id,deceased_id,person_id,relation) VALUES ($1,$2,$3,$4,'child')`, familyLink, tenant, deceased, relative); err != nil {
			return err
		}
		_, err = admin.Exec(ctx, `INSERT INTO catalog.catalog_item (id,tenant_id,kind,name,unit,active) VALUES ($1,$2,'service','Servico funerario','service',true)`, item, tenant)
		return err
	}
	if err := setup(tenantA, companyA, customerA, deceasedPartyA, relativeA, deceasedA, familyLinkA, itemA); err != nil {
		t.Fatal(err)
	}
	if err := setup(tenantB, companyB, customerB, deceasedPartyB, relativeB, deceasedB, familyLinkB, itemB); err != nil {
		t.Fatal(err)
	}

	events, err := outbox.NewRepository(appDatabase)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := funeral.NewRepository(appDatabase, events)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpserver.New(nil, httpserver.Options{DevelopmentMode: true, DevelopmentFuneral: funeral.NewDevelopmentHTTPHandler(contractChecker{}, repository)}).Handler()
	request := func(method, path string, tenant, company uuid.UUID, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), tenant), company))
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	body := fmt.Sprintf(`{"customer_party_id":"%s","deceased_id":"%s","family_link_ids":["%s"],"items":[{"catalog_item_id":"%s","quantity":1,"unit_amount_cents":15000}]}`, customerA, deceasedA, familyLinkA, itemA)
	created := request(http.MethodPost, "/_dev/funeral/contracts", tenantA, companyA, "contract-integration-001", body)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST contrato = %d: %s", created.Code, created.Body.String())
	}
	var envelope struct {
		Contract struct {
			ID      string `json:"id"`
			Total   int64  `json:"total_amount_cents"`
			History []any  `json:"status_history"`
		} `json:"contract"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Contract.Total != 15000 || len(envelope.Contract.History) != 1 {
		t.Fatalf("contrato criado = %#v", envelope.Contract)
	}
	contractID := envelope.Contract.ID
	if replay := request(http.MethodPost, "/_dev/funeral/contracts", tenantA, companyA, "contract-integration-001", body); replay.Code != http.StatusCreated {
		t.Fatalf("replay = %d: %s", replay.Code, replay.Body.String())
	}
	if conflict := request(http.MethodPost, "/_dev/funeral/contracts", tenantA, companyA, "contract-integration-001", strings.Replace(body, "15000", "16000", 1)); conflict.Code != http.StatusConflict {
		t.Fatalf("conflito de idempotencia = %d: %s", conflict.Code, conflict.Body.String())
	}
	if foreign := request(http.MethodGet, "/_dev/funeral/contracts/"+contractID, tenantB, companyB, "", ""); foreign.Code != http.StatusNotFound {
		t.Fatalf("consulta tenant B = %d: %s", foreign.Code, foreign.Body.String())
	}
	if changed := request(http.MethodPatch, "/_dev/funeral/contracts/"+contractID+"/status", tenantA, companyA, "", `{"status":"active","reason":"confirmado"}`); changed.Code != http.StatusOK {
		t.Fatalf("mudanca de status = %d: %s", changed.Code, changed.Body.String())
	}
	read := request(http.MethodGet, "/_dev/funeral/contracts/"+contractID, tenantA, companyA, "", "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"to_status":"active"`) {
		t.Fatalf("consulta com historico = %d: %s", read.Code, read.Body.String())
	}
	var partyCount, eventCount int
	if err := appDatabase.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM party.party WHERE tenant_id=$1`, tenantA).Scan(&partyCount); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1 AND aggregate_id=$2`, tenantA, uuid.MustParse(contractID)).Scan(&eventCount)
	}); err != nil {
		t.Fatal(err)
	}
	if partyCount != 3 {
		t.Fatalf("contrato criou party implicitamente: count=%d", partyCount)
	}
	if eventCount != 2 {
		t.Fatalf("eventos do contrato = %d, esperado 2", eventCount)
	}
}
