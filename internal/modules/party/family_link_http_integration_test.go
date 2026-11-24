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

// TestDevelopmentFamilyLinkHTTPIntegration proves create/read, name resolution,
// domain validation and tenant isolation with a role that cannot bypass RLS.
func TestDevelopmentFamilyLinkHTTPIntegration(t *testing.T) {
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
	roleName := "amparius_family_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	grant := func(statement string) {
		t.Helper()
		if _, err := adminPool.Exec(ctx, fmt.Sprintf(statement, roleName)); err != nil {
			_ = dropRole(ctx)
			adminPool.Close()
			t.Fatal(err)
		}
	}
	grant("GRANT USAGE ON SCHEMA party TO %s")
	grant("GRANT USAGE ON SCHEMA identity TO %s")
	grant("GRANT SELECT ON party.party, party.person, party.deceased TO %s")
	grant("GRANT SELECT, INSERT ON party.family_link TO %s")
	grant("GRANT SELECT ON identity.domain_value TO %s")
	grant("GRANT EXECUTE ON FUNCTION public.uuidv7() TO %s")

	baseURL.User = url.UserPassword(roleName, rolePassword)
	appDatabase, err := db.New(ctx, baseURL.String())
	if err != nil {
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatal(err)
	}

	var role string
	var superuser, bypassRLS bool
	var schemaUsage, identityUsage bool
	var partySelect, partyInsert bool
	var personSelect, personInsert bool
	var deceasedSelect, deceasedInsert bool
	var linkSelect, linkInsert, linkUpdate, linkDelete bool
	var domainSelect bool
	var uuidExec bool
	if err := appDatabase.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user, rolsuper, rolbypassrls,
			       has_schema_privilege(current_user, 'party', 'USAGE'),
			       has_schema_privilege(current_user, 'identity', 'USAGE'),
			       has_table_privilege(current_user, 'party.party', 'SELECT'),
			       has_table_privilege(current_user, 'party.party', 'INSERT'),
			       has_table_privilege(current_user, 'party.person', 'SELECT'),
			       has_table_privilege(current_user, 'party.person', 'INSERT'),
			       has_table_privilege(current_user, 'party.deceased', 'SELECT'),
			       has_table_privilege(current_user, 'party.deceased', 'INSERT'),
			       has_table_privilege(current_user, 'party.family_link', 'SELECT'),
			       has_table_privilege(current_user, 'party.family_link', 'INSERT'),
			       has_table_privilege(current_user, 'party.family_link', 'UPDATE'),
			       has_table_privilege(current_user, 'party.family_link', 'DELETE'),
			       has_table_privilege(current_user, 'identity.domain_value', 'SELECT'),
			       has_function_privilege(current_user, 'public.uuidv7()', 'EXECUTE')
			FROM pg_roles WHERE rolname = current_user
		`).Scan(&role, &superuser, &bypassRLS, &schemaUsage, &identityUsage,
			&partySelect, &partyInsert, &personSelect, &personInsert,
			&deceasedSelect, &deceasedInsert,
			&linkSelect, &linkInsert, &linkUpdate, &linkDelete,
			&domainSelect, &uuidExec)
	}); err != nil {
		appDatabase.Close()
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatal(err)
	}
	if role != roleName || superuser || bypassRLS || !schemaUsage || !identityUsage ||
		!partySelect || partyInsert || !personSelect || personInsert ||
		!deceasedSelect || deceasedInsert ||
		!linkSelect || !linkInsert || linkUpdate || linkDelete || !domainSelect || !uuidExec {
		appDatabase.Close()
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatalf("privilégios efetivos não mínimos: role=%s superuser=%v bypass=%v schema=%v/%v party=%v/%v person=%v/%v deceased=%v/%v link=%v/%v/%v/%v domain=%v uuid=%v",
			role, superuser, bypassRLS, schemaUsage, identityUsage,
			partySelect, partyInsert, personSelect, personInsert,
			deceasedSelect, deceasedInsert, linkSelect, linkInsert, linkUpdate, linkDelete, domainSelect, uuidExec)
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
			t.Errorf("cleanup administrativo de family_link: %v", cleanupErr)
		}
		if cleanupErr = dropRole(cleanupCtx); cleanupErr != nil {
			t.Errorf("remover role temporária de family_link: %v", cleanupErr)
		}
		var remaining int
		if err := adminPool.QueryRow(cleanupCtx,
			`SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, tenantIDs).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup de family_link: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup deixou %d tenant(s) residuais", remaining)
		}
	})

	deceasedPersonA, relativeA, companyA := uuid.New(), uuid.New(), uuid.New()
	deceasedPersonB, relativeB := uuid.New(), uuid.New()
	setupTx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenantID := range tenantIDs {
		if _, err := setupTx.Exec(ctx, `INSERT INTO identity.tenant (id, name, document, regime, status) VALUES ($1,$2,$3,'economic_group','active')`,
			tenantID, "Family "+tenantID.String(), "doc-"+tenantID.String()); err != nil {
			_ = setupTx.Rollback(ctx)
			t.Fatal(err)
		}
	}
	if _, err := setupTx.Exec(ctx, `INSERT INTO party.party (id, tenant_id, kind, name, status) VALUES
		($1,$2,'person','Maria Falecida','active'),
		($3,$2,'person','João Familiar','active'),
		($4,$2,'company','Empresa','active'),
		($5,$6,'person','Pedro Falecido','active'),
		($7,$6,'person','Ana Familiar','active')`,
		deceasedPersonA, tenantA, relativeA, companyA, deceasedPersonB, tenantB, relativeB); err != nil {
		_ = setupTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := setupTx.Exec(ctx, `INSERT INTO party.person (tenant_id, party_id, social_name) VALUES
		($1,$2,NULL), ($1,$3,'João Social'), ($4,$5,NULL), ($4,$6,NULL)`,
		tenantA, deceasedPersonA, relativeA, tenantB, deceasedPersonB, relativeB); err != nil {
		_ = setupTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := setupTx.Exec(ctx, `INSERT INTO party.deceased (id, tenant_id, person_id, death_at) VALUES
		($1,$2,$3,'2026-11-20T09:00:00-03:00'), ($4,$5,$6,'2026-11-21T09:00:00-03:00')`,
		uuid.New(), tenantA, deceasedPersonA, uuid.New(), tenantB, deceasedPersonB); err != nil {
		_ = setupTx.Rollback(ctx)
		t.Fatal(err)
	}
	var deceasedAID uuid.UUID
	if err := setupTx.QueryRow(ctx, `SELECT id FROM party.deceased WHERE tenant_id = $1 AND person_id = $2`, tenantA, deceasedPersonA).Scan(&deceasedAID); err != nil {
		_ = setupTx.Rollback(ctx)
		t.Fatal(err)
	}
	var deceasedBID uuid.UUID
	if err := setupTx.QueryRow(ctx, `SELECT id FROM party.deceased WHERE tenant_id = $1 AND person_id = $2`, tenantB, deceasedPersonB).Scan(&deceasedBID); err != nil {
		_ = setupTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var partyCountA int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM party.party WHERE tenant_id = $1`, tenantA).Scan(&partyCountA); err != nil {
		t.Fatal(err)
	}

	repository, err := party.NewRepository(appDatabase)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger, httpserver.Options{
		DevelopmentMode:        true,
		DevelopmentFamilyLinks: party.NewDevelopmentFamilyLinkHTTPHandler(repository),
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

	created := request(http.MethodPost, "/_dev/family-links", tenantA,
		`{"deceased_id":"`+deceasedAID.String()+`","person_id":"`+relativeA.String()+`","relation":"child"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST status = %d: %s", created.Code, created.Body.String())
	}
	if created.Header().Get("X-Amparius-Development-Only") != "true" ||
		created.Header().Get("Cache-Control") != "no-store" ||
		created.Header().Get("Location") == "" {
		t.Fatalf("headers de proteção = %#v", created.Header())
	}
	var envelope struct {
		Status     string `json:"status"`
		FamilyLink struct {
			ID       string `json:"id"`
			Relation string `json:"relation"`
			Deceased struct {
				ID          string  `json:"id"`
				CivilName   string  `json:"civil_name"`
				SocialName  *string `json:"social_name"`
				DisplayName string  `json:"display_name"`
			} `json:"deceased"`
			Person struct {
				ID          string  `json:"id"`
				CivilName   string  `json:"civil_name"`
				SocialName  *string `json:"social_name"`
				DisplayName string  `json:"display_name"`
			} `json:"person"`
		} `json:"family_link"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	link := envelope.FamilyLink
	if link.Relation != "child" || link.Deceased.ID != deceasedPersonA.String() || link.Person.ID != relativeA.String() ||
		link.Deceased.CivilName != "Maria Falecida" || link.Deceased.DisplayName != "Maria Falecida" || link.Deceased.SocialName != nil ||
		link.Person.CivilName != "João Familiar" || link.Person.SocialName == nil || *link.Person.SocialName != "João Social" ||
		link.Person.DisplayName != "João Social" {
		t.Fatalf("resposta de family_link = %s", created.Body.String())
	}
	if strings.Contains(created.Body.String(), "tenant_id") || strings.Contains(created.Body.String(), `"name"`) {
		t.Fatalf("resposta vazou tenant ou duplicou nome: %s", created.Body.String())
	}

	path := "/_dev/family-links/" + link.ID
	if got := request(http.MethodGet, path, tenantA, ""); got.Code != http.StatusOK {
		t.Fatalf("GET tenant A = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, path, tenantB, ""); got.Code != http.StatusNotFound {
		t.Fatalf("GET tenant B = %d, quer 404: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/family-links", tenantA,
		`{"deceased_id":"`+deceasedAID.String()+`","person_id":"`+relativeB.String()+`","relation":"child"}`); got.Code != http.StatusNotFound {
		t.Fatalf("familiar de outro tenant = %d, quer 404: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/family-links", tenantA,
		`{"deceased_id":"`+deceasedBID.String()+`","person_id":"`+relativeA.String()+`","relation":"child"}`); got.Code != http.StatusNotFound {
		t.Fatalf("falecido de outro tenant = %d, quer 404: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/family-links", tenantA,
		`{"deceased_id":"`+deceasedAID.String()+`","person_id":"`+companyA.String()+`","relation":"child"}`); got.Code != http.StatusConflict {
		t.Fatalf("pessoa jurídica = %d, quer 409: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/family-links", tenantA,
		`{"deceased_id":"`+deceasedAID.String()+`","person_id":"`+relativeA.String()+`","relation":"invalida"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("relação inválida = %d, quer 400: %s", got.Code, got.Body.String())
	}

	if err := appDatabase.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		var deceasedID, personID uuid.UUID
		var relation string
		return tx.QueryRow(ctx, `SELECT deceased_id, person_id, relation FROM party.family_link WHERE id = $1`, uuid.MustParse(link.ID)).
			Scan(&deceasedID, &personID, &relation)
	}); err != nil {
		t.Fatalf("persistência de family_link: %v", err)
	}

	var partyCountAfter int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM party.party WHERE tenant_id = $1`, tenantA).Scan(&partyCountAfter); err != nil {
		t.Fatal(err)
	}
	if partyCountAfter != partyCountA {
		t.Fatalf("family link criou party implicitamente: antes=%d depois=%d", partyCountA, partyCountAfter)
	}
	var nameColumns int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'party' AND table_name = 'family_link'
		  AND column_name IN ('name', 'civil_name', 'social_name', 'display_name')`).Scan(&nameColumns); err != nil {
		t.Fatal(err)
	}
	if nameColumns != 0 {
		t.Fatalf("party.family_link guardou nome em %d coluna(s)", nameColumns)
	}
}
