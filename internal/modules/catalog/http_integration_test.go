package catalog_test

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

	"github.com/alltomatos/amparius/internal/modules/catalog"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDevelopmentCatalogHTTPIntegration prova criação/consulta, isolamento
// cruzado e rejeições com uma role NOSUPERUSER/NOBYPASSRLS que só tem os
// privilégios mínimos da migration 0014.
func TestDevelopmentCatalogHTTPIntegration(t *testing.T) {
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
	roleName := "amparius_catalog_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	grant("GRANT USAGE ON SCHEMA catalog TO %s")
	grant("GRANT USAGE ON SCHEMA identity TO %s")
	// O trigger catalog_item_validate_domains chama party.assert_domain_value;
	// por isso a role precisa de USAGE em party, já concedido a amparius_app por
	// 0010_party_social_name.sql.
	grant("GRANT USAGE ON SCHEMA party TO %s")
	grant("GRANT SELECT, INSERT ON catalog.catalog_item, catalog.price_list, catalog.price TO %s")
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
	var catalogUsage, identityUsage, partyUsage bool
	var itemSelect, itemInsert, itemUpdate, itemDelete bool
	var listSelect, listInsert, listUpdate, listDelete bool
	var priceSelect, priceInsert, priceUpdate, priceDelete bool
	var domainSelect, domainInsert, uuidExec bool
	if err := appDatabase.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user, rolsuper, rolbypassrls,
			       has_schema_privilege(current_user, 'catalog', 'USAGE'),
			       has_schema_privilege(current_user, 'identity', 'USAGE'),
			       has_schema_privilege(current_user, 'party', 'USAGE'),
			       has_table_privilege(current_user, 'catalog.catalog_item', 'SELECT'),
			       has_table_privilege(current_user, 'catalog.catalog_item', 'INSERT'),
			       has_table_privilege(current_user, 'catalog.catalog_item', 'UPDATE'),
			       has_table_privilege(current_user, 'catalog.catalog_item', 'DELETE'),
			       has_table_privilege(current_user, 'catalog.price_list', 'SELECT'),
			       has_table_privilege(current_user, 'catalog.price_list', 'INSERT'),
			       has_table_privilege(current_user, 'catalog.price_list', 'UPDATE'),
			       has_table_privilege(current_user, 'catalog.price_list', 'DELETE'),
			       has_table_privilege(current_user, 'catalog.price', 'SELECT'),
			       has_table_privilege(current_user, 'catalog.price', 'INSERT'),
			       has_table_privilege(current_user, 'catalog.price', 'UPDATE'),
			       has_table_privilege(current_user, 'catalog.price', 'DELETE'),
			       has_table_privilege(current_user, 'identity.domain_value', 'SELECT'),
			       has_table_privilege(current_user, 'identity.domain_value', 'INSERT'),
			       has_function_privilege(current_user, 'public.uuidv7()', 'EXECUTE')
			FROM pg_roles WHERE rolname = current_user
		`).Scan(&role, &superuser, &bypassRLS, &catalogUsage, &identityUsage, &partyUsage,
			&itemSelect, &itemInsert, &itemUpdate, &itemDelete,
			&listSelect, &listInsert, &listUpdate, &listDelete,
			&priceSelect, &priceInsert, &priceUpdate, &priceDelete,
			&domainSelect, &domainInsert, &uuidExec)
	}); err != nil {
		appDatabase.Close()
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatal(err)
	}
	if role != roleName || superuser || bypassRLS || !catalogUsage || !identityUsage || !partyUsage ||
		!itemSelect || !itemInsert || itemUpdate || itemDelete ||
		!listSelect || !listInsert || listUpdate || listDelete ||
		!priceSelect || !priceInsert || priceUpdate || priceDelete ||
		!domainSelect || domainInsert || !uuidExec {
		appDatabase.Close()
		_ = dropRole(ctx)
		adminPool.Close()
		t.Fatalf("privilégios efetivos não mínimos: role=%s superuser=%v bypass=%v schema=%v/%v/%v item=%v/%v/%v/%v list=%v/%v/%v/%v price=%v/%v/%v/%v domain=%v/%v uuid=%v",
			role, superuser, bypassRLS, catalogUsage, identityUsage, partyUsage,
			itemSelect, itemInsert, itemUpdate, itemDelete,
			listSelect, listInsert, listUpdate, listDelete,
			priceSelect, priceInsert, priceUpdate, priceDelete,
			domainSelect, domainInsert, uuidExec)
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
				"DELETE FROM catalog.price WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM catalog.price_list WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM catalog.catalog_item WHERE tenant_id = ANY($1::uuid[])",
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
			t.Errorf("cleanup administrativo de catálogo: %v", cleanupErr)
		}
		if cleanupErr = dropRole(cleanupCtx); cleanupErr != nil {
			t.Errorf("remover role temporária de catálogo: %v", cleanupErr)
		}
		var remaining int
		if err := adminPool.QueryRow(cleanupCtx,
			`SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, tenantIDs).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup de catálogo: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup deixou %d tenant(s) residuais", remaining)
		}
	})

	setupTx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenantID := range tenantIDs {
		if _, err := setupTx.Exec(ctx, `INSERT INTO identity.tenant (id, name, document, regime, status) VALUES ($1,$2,$3,'economic_group','active')`,
			tenantID, "Catalog "+tenantID.String(), "doc-"+tenantID.String()); err != nil {
			_ = setupTx.Rollback(ctx)
			t.Fatal(err)
		}
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	repository, err := catalog.NewRepository(appDatabase)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger, httpserver.Options{
		DevelopmentMode:    true,
		DevelopmentCatalog: catalog.NewDevelopmentHTTPHandler(repository),
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

	createdItem := request(http.MethodPost, "/_dev/catalog/items", tenantA, `{"kind":"service","name":"Missa","unit":"service"}`)
	if createdItem.Code != http.StatusCreated {
		t.Fatalf("POST item = %d: %s", createdItem.Code, createdItem.Body.String())
	}
	if createdItem.Header().Get("X-Amparius-Development-Only") != "true" ||
		createdItem.Header().Get("Cache-Control") != "no-store" ||
		createdItem.Header().Get("Location") == "" {
		t.Fatalf("headers de proteção = %#v", createdItem.Header())
	}
	itemA := decodeEnvelopeID(t, createdItem.Body.Bytes(), "item")

	if got := request(http.MethodGet, "/_dev/catalog/items/"+itemA, tenantA, ""); got.Code != http.StatusOK {
		t.Fatalf("GET item tenant A = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/_dev/catalog/items/"+itemA, tenantB, ""); got.Code != http.StatusNotFound {
		t.Fatalf("GET item tenant B = %d, quer 404: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/catalog/items", tenantA,
		`{"kind":"invalido","name":"X","unit":"unit"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("kind inválido = %d, quer 400: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/catalog/items", tenantA,
		`{"kind":"service","name":"X","unit":"invalida"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("unit inválida = %d, quer 400: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/catalog/items", tenantA,
		`{"kind":"service","name":"X","unit":"unit","tenant_id":"`+tenantB.String()+`"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("tenant_id no corpo = %d, quer 400: %s", got.Code, got.Body.String())
	}

	createdItemB := request(http.MethodPost, "/_dev/catalog/items", tenantB, `{"kind":"product","name":"Urna","unit":"unit"}`)
	if createdItemB.Code != http.StatusCreated {
		t.Fatalf("POST item tenant B = %d: %s", createdItemB.Code, createdItemB.Body.String())
	}
	itemB := decodeEnvelopeID(t, createdItemB.Body.Bytes(), "item")

	createdList := request(http.MethodPost, "/_dev/catalog/price-lists", tenantA, `{"name":"Tabela 2026","valid_from":"2026-01-01"}`)
	if createdList.Code != http.StatusCreated {
		t.Fatalf("POST price-list = %d: %s", createdList.Code, createdList.Body.String())
	}
	listA := decodeEnvelopeID(t, createdList.Body.Bytes(), "price_list")
	if got := request(http.MethodGet, "/_dev/catalog/price-lists/"+listA, tenantB, ""); got.Code != http.StatusNotFound {
		t.Fatalf("GET price-list tenant B = %d, quer 404", got.Code)
	}

	createdListB := request(http.MethodPost, "/_dev/catalog/price-lists", tenantB, `{"name":"Tabela B","valid_from":"2026-01-01"}`)
	if createdListB.Code != http.StatusCreated {
		t.Fatalf("POST price-list tenant B = %d: %s", createdListB.Code, createdListB.Body.String())
	}
	listB := decodeEnvelopeID(t, createdListB.Body.Bytes(), "price_list")

	createdPrice := request(http.MethodPost, "/_dev/catalog/prices", tenantA,
		`{"item_id":"`+itemA+`","list_id":"`+listA+`","amount_cents":150000,"valid_from":"2026-01-01","valid_to":null}`)
	if createdPrice.Code != http.StatusCreated {
		t.Fatalf("POST price = %d: %s", createdPrice.Code, createdPrice.Body.String())
	}
	var priceEnvelope struct {
		Price struct {
			ID          string  `json:"id"`
			ItemID      string  `json:"item_id"`
			ListID      string  `json:"list_id"`
			AmountCents int64   `json:"amount_cents"`
			ValidFrom   string  `json:"valid_from"`
			ValidTo     *string `json:"valid_to"`
		} `json:"price"`
	}
	if err := json.Unmarshal(createdPrice.Body.Bytes(), &priceEnvelope); err != nil {
		t.Fatal(err)
	}
	if priceEnvelope.Price.AmountCents != 150000 || priceEnvelope.Price.ItemID != itemA ||
		priceEnvelope.Price.ListID != listA || priceEnvelope.Price.ValidFrom != "2026-01-01" ||
		priceEnvelope.Price.ValidTo != nil {
		t.Fatalf("resposta de preço = %s", createdPrice.Body.String())
	}
	priceA := priceEnvelope.Price.ID

	if got := request(http.MethodGet, "/_dev/catalog/prices/"+priceA, tenantA, ""); got.Code != http.StatusOK {
		t.Fatalf("GET price tenant A = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/_dev/catalog/prices/"+priceA, tenantB, ""); got.Code != http.StatusNotFound {
		t.Fatalf("GET price tenant B = %d, quer 404", got.Code)
	}
	if got := request(http.MethodPost, "/_dev/catalog/prices", tenantA,
		`{"item_id":"`+itemB+`","list_id":"`+listA+`","amount_cents":100,"valid_from":"2026-01-01"}`); got.Code != http.StatusNotFound {
		t.Fatalf("item de outro tenant = %d, quer 404: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/catalog/prices", tenantA,
		`{"item_id":"`+itemA+`","list_id":"`+listB+`","amount_cents":100,"valid_from":"2026-01-01"}`); got.Code != http.StatusNotFound {
		t.Fatalf("lista de outro tenant = %d, quer 404: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/_dev/catalog/prices", tenantA,
		`{"item_id":"`+itemA+`","list_id":"`+listA+`","amount_cents":10.5,"valid_from":"2026-01-01"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("amount float = %d, quer 400: %s", got.Code, got.Body.String())
	}

	listsA := request(http.MethodGet, "/_dev/catalog/price-lists", tenantA, "")
	if listsA.Code != http.StatusOK {
		t.Fatalf("GET price-lists = %d: %s", listsA.Code, listsA.Body.String())
	}
	if strings.Contains(listsA.Body.String(), listB) || !strings.Contains(listsA.Body.String(), listA) {
		t.Fatalf("listagem vazou lista de outro tenant: %s", listsA.Body.String())
	}

	if err := appDatabase.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		var amount int64
		var itemID, listID uuid.UUID
		var validTo *string
		return tx.QueryRow(ctx, `SELECT item_id, list_id, (amount * 100)::bigint, valid_to::text FROM catalog.price WHERE id = $1`, uuid.MustParse(priceA)).
			Scan(&itemID, &listID, &amount, &validTo)
	}); err != nil {
		t.Fatalf("persistência de price: %v", err)
	}

	for _, path := range []string{"/_dev/catalog/items", "/_dev/catalog/price-lists", "/_dev/catalog/prices"} {
		if got := request(http.MethodGet, path+"?limit=abc", tenantA, ""); got.Code != http.StatusBadRequest {
			t.Fatalf("%s?limit=abc = %d, quer 400", path, got.Code)
		}
	}
}

func decodeEnvelopeID(t *testing.T, body []byte, key string) string {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	var resource struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(envelope[key], &resource); err != nil || resource.ID == "" {
		t.Fatalf("resposta sem id em %q: %s", key, body)
	}
	return resource.ID
}
