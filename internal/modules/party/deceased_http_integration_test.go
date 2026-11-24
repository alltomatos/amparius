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
	"testing"

	"github.com/alltomatos/amparius/internal/modules/party"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDevelopmentDeceasedHTTPIntegration proves create/read, name resolution
// and tenant isolation with a role that cannot bypass RLS.
func TestDevelopmentDeceasedHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_ADMIN_DSN não definido; teste não criará dados sem cleanup administrativo")
	}

	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}
	baseURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	roleName := "amparius_deceased_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	rolePassword := "test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(
		"CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT",
		roleName, rolePassword)); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}
	dropRole := func(dropCtx context.Context) error {
		if _, err := adminPool.Exec(dropCtx, fmt.Sprintf("DROP OWNED BY %s", roleName)); err != nil {
			return err
		}
		_, err := adminPool.Exec(dropCtx, fmt.Sprintf("DROP ROLE %s", roleName))
		return err
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("GRANT USAGE ON SCHEMA party TO %s", roleName)); err != nil {
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("GRANT SELECT ON party.party, party.person TO %s", roleName)); err != nil {
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("GRANT SELECT, INSERT ON party.deceased TO %s", roleName)); err != nil {
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatal(err)
	}
	baseURL.User = url.UserPassword(roleName, rolePassword)
	appDatabase, err := db.New(ctx, baseURL.String())
	if err != nil {
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatal(err)
	}

	var role string
	var superuser, bypassRLS, schemaUsage bool
	var partySelect, partyInsert, partyUpdate bool
	var personSelect, personInsert, personUpdate bool
	var deceasedSelect, deceasedInsert, deceasedUpdate bool
	var uuidExec bool
	if err := appDatabase.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user, rolsuper, rolbypassrls,
			       has_schema_privilege(current_user, 'party', 'USAGE'),
			       has_table_privilege(current_user, 'party.party', 'SELECT'),
			       has_table_privilege(current_user, 'party.party', 'INSERT'),
			       has_table_privilege(current_user, 'party.party', 'UPDATE'),
			       has_table_privilege(current_user, 'party.person', 'SELECT'),
			       has_table_privilege(current_user, 'party.person', 'INSERT'),
			       has_table_privilege(current_user, 'party.person', 'UPDATE'),
			       has_table_privilege(current_user, 'party.deceased', 'SELECT'),
			       has_table_privilege(current_user, 'party.deceased', 'INSERT'),
			       has_table_privilege(current_user, 'party.deceased', 'UPDATE'),
			       has_function_privilege(current_user, 'public.uuidv7()', 'EXECUTE')
			FROM pg_roles WHERE rolname = current_user
		`).Scan(&role, &superuser, &bypassRLS, &schemaUsage,
			&partySelect, &partyInsert, &partyUpdate,
			&personSelect, &personInsert, &personUpdate,
			&deceasedSelect, &deceasedInsert, &deceasedUpdate, &uuidExec)
	}); err != nil {
		appDatabase.Close()
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatal(err)
	}
	if role != roleName || superuser || bypassRLS || !schemaUsage ||
		!partySelect || partyInsert || partyUpdate || !personSelect || personInsert || personUpdate ||
		!deceasedSelect || !deceasedInsert || deceasedUpdate || !uuidExec {
		appDatabase.Close()
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatalf("privilégios efetivos não mínimos: role=%s superuser=%v bypass=%v schema=%v party=%v/%v/%v person=%v/%v/%v deceased=%v/%v/%v uuid=%v",
			role, superuser, bypassRLS, schemaUsage, partySelect, partyInsert, partyUpdate,
			personSelect, personInsert, personUpdate, deceasedSelect, deceasedInsert, deceasedUpdate, uuidExec)
	}

	tenantA, tenantB := uuid.New(), uuid.New()
	tenantIDs := []uuid.UUID{tenantA, tenantB}
	t.Cleanup(func() {
		appDatabase.Close()
		defer adminPool.Close()
		cleanupCtx := context.Background()
		tx, cleanupErr := adminPool.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, statement := range []string{
				"DELETE FROM party.family_link WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.deceased WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.person WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.party_company WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.party WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
			} {
				if _, cleanupErr = tx.Exec(cleanupCtx, statement, tenantIDs); cleanupErr != nil {
					break
				}
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(cleanupCtx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup administrativo de deceased: %v", cleanupErr)
		}
		if cleanupErr = dropRole(cleanupCtx); cleanupErr != nil {
			t.Errorf("remover role temporária de deceased: %v", cleanupErr)
		}
		var remaining int
		if err := adminPool.QueryRow(cleanupCtx,
			`SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, tenantIDs).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup de deceased: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup deixou %d tenant(s) residuais", remaining)
		}
	})

	personAID, personBID, companyID := uuid.New(), uuid.New(), uuid.New()
	social := "Nome Social"
	setupTx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		if _, err := setupTx.Exec(ctx, `INSERT INTO identity.tenant (id, name, document, regime, status) VALUES ($1,$2,$3,'economic_group','active')`,
			tenantID, "Deceased "+tenantID.String(), "doc-"+tenantID.String()); err != nil {
			_ = setupTx.Rollback(ctx)
			t.Fatal(err)
		}
	}
	if _, err := setupTx.Exec(ctx, `INSERT INTO party.party (id, tenant_id, kind, name, status) VALUES
		($1,$2,'person','Nome Civil','active'), ($3,$4,'person','Pessoa B','active'),
		($5,$2,'company','Empresa','active')`, personAID, tenantA, personBID, tenantB, companyID); err != nil {
		_ = setupTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := setupTx.Exec(ctx, `INSERT INTO party.person (tenant_id, party_id, social_name) VALUES ($1,$2,'Nome Social'), ($3,$4,NULL)`,
		tenantA, personAID, tenantB, personBID); err != nil {
		_ = setupTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	repository, err := party.NewRepository(appDatabase)
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger, httpserver.Options{
		DevelopmentMode:     true,
		DevelopmentDeceased: party.NewDevelopmentDeceasedHTTPHandler(repository),
	}).Handler()
	request := func(method, path string, tenantID uuid.UUID, body string) *httptest.ResponseRecorder {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("X-Amparius-Development-Tenant-ID", tenantID.String())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	created := request(http.MethodPost, "/_dev/deceased", tenantA,
		`{"person_id":"`+personAID.String()+`","death_at":"2026-11-24T12:00:00-03:00","death_certificate":"CERT-1","burial_site":"Jazigo 7"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST status = %d: %s", created.Code, created.Body.String())
	}
	if created.Header().Get("X-Amparius-Development-Only") != "true" || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers de proteção = %#v", created.Header())
	}
	var envelope struct {
		Deceased struct {
			ID          string  `json:"id"`
			PersonID    string  `json:"person_id"`
			CivilName   string  `json:"civil_name"`
			SocialName  *string `json:"social_name"`
			DisplayName string  `json:"display_name"`
		} `json:"deceased"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Deceased.PersonID != personAID.String() || envelope.Deceased.CivilName != "Nome Civil" ||
		envelope.Deceased.SocialName == nil || *envelope.Deceased.SocialName != social || envelope.Deceased.DisplayName != social ||
		strings.Contains(created.Body.String(), "tenant_id") || strings.Contains(created.Body.String(), `"name"`) {
		t.Fatalf("resposta de deceased = %s", created.Body.String())
	}

	path := "/_dev/deceased/" + envelope.Deceased.ID
	if got := request(http.MethodGet, path, tenantA, ""); got.Code != http.StatusOK {
		t.Fatalf("GET tenant A = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, path, tenantB, ""); got.Code != http.StatusNotFound {
		t.Fatalf("GET tenant B = %d, quer 404: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/deceased", tenantA,
		`{"person_id":"`+personBID.String()+`","death_at":"2026-11-24T12:00:00Z"}`); got.Code != http.StatusNotFound {
		t.Fatalf("pessoa de outro tenant = %d, quer 404: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/deceased", tenantA,
		`{"person_id":"`+companyID.String()+`","death_at":"2026-11-24T12:00:00Z"}`); got.Code != http.StatusConflict {
		t.Fatalf("pessoa jurídica = %d, quer 409: %s", got.Code, got.Body.String())
	}

	if err := appDatabase.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		var personID uuid.UUID
		var certificate, burial string
		return tx.QueryRow(ctx, `SELECT person_id, death_certificate, burial_site FROM party.deceased WHERE id = $1`, uuid.MustParse(envelope.Deceased.ID)).
			Scan(&personID, &certificate, &burial)
	}); err != nil {
		t.Fatalf("persistência de deceased: %v", err)
	}
}
