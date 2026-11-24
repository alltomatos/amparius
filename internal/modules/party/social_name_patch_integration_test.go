package party_test

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

	"github.com/alltomatos/amparius/internal/modules/party"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestDevelopmentPartiesPatchSocialNameIntegration cobre o PATCH de nome social
// contra PostgreSQL real com papel NOSUPERUSER/NOBYPASSRLS: preservação de nome
// civil/documento/id, avanço de version e updated_at, 404 em tenant errado e
// recusa de pessoa jurídica.
func TestDevelopmentPartiesPatchSocialNameIntegration(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}

	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var role string
	var superuser, bypassRLS bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).
			Scan(&role, &superuser, &bypassRLS)
	}); err != nil {
		t.Fatal(err)
	}
	if superuser || bypassRLS {
		t.Fatalf("teste exige papel sem SUPERUSER/BYPASSRLS; role=%s superuser=%v bypass=%v", role, superuser, bypassRLS)
	}

	repository, err := party.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id, name, document, regime, status) VALUES ($1,$2,$3,$4,$5)`,
				tenantID, "Patch social "+tenantID.String(), "doc-"+tenantID.String(), "economic_group", "active")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger, httpserver.Options{
		DevelopmentMode:    true,
		DevelopmentParties: party.NewDevelopmentHTTPHandler(repository),
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
	type partyResponse struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		CivilName   string  `json:"civil_name"`
		Document    string  `json:"document"`
		SocialName  *string `json:"social_name"`
		DisplayName string  `json:"display_name"`
	}
	decode := func(rec *httptest.ResponseRecorder) partyResponse {
		t.Helper()
		var envelope struct {
			Party partyResponse `json:"party"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("resposta inválida: %v (%s)", err, rec.Body.String())
		}
		return envelope.Party
	}

	created := request(http.MethodPost, "/_dev/parties", tenantA,
		`{"name":"Ana Civil","social_name":"Ana Social","document":"123"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status do cadastro = %d: %s", created.Code, created.Body.String())
	}
	initial := decode(created)

	var partyVersion, personVersion int
	var personUpdated time.Time
	readVersions := func() {
		t.Helper()
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT p.version, pe.version, pe.updated_at
				FROM party.party p
				JOIN party.person pe ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
				WHERE p.id = $1`, initial.ID).Scan(&partyVersion, &personVersion, &personUpdated)
		}); err != nil {
			t.Fatalf("versões antes do PATCH: %v", err)
		}
	}
	readVersions()
	beforePartyVersion, beforePersonVersion, beforeUpdated := partyVersion, personVersion, personUpdated

	updated := request(http.MethodPatch, "/_dev/parties/"+initial.ID, tenantA, `{"social_name":"  Ana Nova  "}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("status do PATCH = %d: %s", updated.Code, updated.Body.String())
	}
	if got := updated.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only = %q", got)
	}
	if got := updated.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	afterPatch := decode(updated)
	if afterPatch.ID != initial.ID || afterPatch.Name != "Ana Civil" || afterPatch.CivilName != "Ana Civil" ||
		afterPatch.Document != "123" || afterPatch.SocialName == nil || *afterPatch.SocialName != "Ana Nova" ||
		afterPatch.DisplayName != "Ana Nova" {
		t.Fatalf("party após PATCH = %#v", afterPatch)
	}

	var storedCivil string
	var storedSocial pgtype.Text
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT p.name, pe.social_name
			FROM party.party p
			JOIN party.person pe ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
			WHERE p.id = $1`, initial.ID).Scan(&storedCivil, &storedSocial)
	}); err != nil {
		t.Fatal(err)
	}
	if storedCivil != "Ana Civil" || !storedSocial.Valid || storedSocial.String != "Ana Nova" {
		t.Fatalf("banco após PATCH = %q/%q", storedCivil, storedSocial.String)
	}

	readVersions()
	if partyVersion != beforePartyVersion+1 || personVersion != beforePersonVersion+1 {
		t.Fatalf("version não avançou: party %d→%d, person %d→%d",
			beforePartyVersion, partyVersion, beforePersonVersion, personVersion)
	}
	if personUpdated.Before(beforeUpdated) {
		t.Fatalf("updated_at retrocedeu: %v → %v", beforeUpdated, personUpdated)
	}

	wrongTenant := request(http.MethodPatch, "/_dev/parties/"+initial.ID, tenantB, `{"social_name":"Invasão"}`)
	if wrongTenant.Code != http.StatusNotFound {
		t.Fatalf("PATCH em tenant errado = %d, quer 404: %s", wrongTenant.Code, wrongTenant.Body.String())
	}
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT pe.social_name
			FROM party.person pe WHERE pe.party_id = $1`, initial.ID).Scan(&storedSocial)
	}); err != nil {
		t.Fatal(err)
	}
	if !storedSocial.Valid || storedSocial.String != "Ana Nova" {
		t.Fatalf("tenant errado alterou o nome social: %#v", storedSocial)
	}

	cleared := request(http.MethodPatch, "/_dev/parties/"+initial.ID, tenantA, `{"social_name":null}`)
	if cleared.Code != http.StatusOK {
		t.Fatalf("status ao limpar = %d: %s", cleared.Code, cleared.Body.String())
	}
	afterClear := decode(cleared)
	if afterClear.SocialName != nil || afterClear.DisplayName != "Ana Civil" || afterClear.Name != "Ana Civil" {
		t.Fatalf("party após limpar = %#v", afterClear)
	}
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT pe.social_name
			FROM party.person pe WHERE pe.party_id = $1`, initial.ID).Scan(&storedSocial)
	}); err != nil {
		t.Fatal(err)
	}
	if storedSocial.Valid {
		t.Fatalf("social_name no banco após limpar = %q, esperado NULL", storedSocial.String)
	}

	companyID := uuid.New()
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO party.party (id, tenant_id, kind, name, status)
			VALUES ($1,$2,'company','ACME LTDA','active')`, companyID, tenantA)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	company := request(http.MethodPatch, "/_dev/parties/"+companyID.String(), tenantA, `{"social_name":"ACME Social"}`)
	if company.Code != http.StatusConflict {
		t.Fatalf("PATCH em pessoa jurídica = %d, quer 409: %s", company.Code, company.Body.String())
	}
	var companyPersons int
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM party.person WHERE party_id = $1`, companyID).Scan(&companyPersons)
	}); err != nil {
		t.Fatal(err)
	}
	if companyPersons != 0 {
		t.Fatalf("PATCH em pessoa jurídica criou %d linha(s) em party.person", companyPersons)
	}
}
