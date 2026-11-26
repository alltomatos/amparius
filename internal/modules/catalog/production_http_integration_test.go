package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/catalog"
	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOperationalCatalogPostgres(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN são necessários")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		tx, err := admin.Begin(cleanupCtx)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = tx.Rollback(cleanupCtx) }()
		for _, statement := range []string{
			"DELETE FROM catalog.price WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM catalog.price_list WHERE tenant_id = ANY($1::uuid[])",
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
	})

	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var role string
		var bypass, itemRLS, listRLS, priceRLS bool
		err := tx.QueryRow(ctx, `SELECT current_user, rolsuper OR rolbypassrls,
			row_security_active('catalog.catalog_item'),
			row_security_active('catalog.price_list'),
			row_security_active('catalog.price')
			FROM pg_roles WHERE rolname = current_user`).Scan(&role, &bypass, &itemRLS, &listRLS, &priceRLS)
		if err != nil {
			return err
		}
		if bypass || !itemRLS || !listRLS || !priceRLS {
			return fmt.Errorf("papel %s sem isolamento efetivo nas três tabelas", role)
		}
		t.Logf("PostgreSQL: papel %s NOSUPERUSER/NOBYPASSRLS e RLS ativo nas três tabelas", role)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	repo, err := catalog.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	onboard := func(role string) auth.OIDCIdentity {
		t.Helper()
		result, err := identities.Onboard(ctx, identity.DevelopmentRequest("cat74-"+uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, result.TenantID)
		subject := "catalog-" + uuid.NewString()
		if _, err := admin.Exec(ctx, `UPDATE identity.app_user
			SET auth_subject=$1, status='active', mfa_enabled=true WHERE id=$2`, subject, result.OwnerUserID); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `INSERT INTO identity.user_company (tenant_id, user_id, company_id)
			VALUES ($1,$2,$3)`, result.TenantID, result.OwnerUserID, result.CompanyID); err != nil {
			t.Fatal(err)
		}
		if role == "auditor" {
			if _, err := admin.Exec(ctx, `UPDATE identity.role SET code='auditor'
				WHERE tenant_id=$1 AND code='owner'`, result.TenantID); err != nil {
				t.Fatal(err)
			}
		}
		return auth.OIDCIdentity{Issuer: "catalog-test-issuer", Subject: subject, TenantID: result.TenantID, CompanyID: result.CompanyID}
	}
	ownerA := onboard("owner")
	ownerB := onboard("owner")
	auditor := onboard("auditor")
	tokens := map[string]auth.OIDCIdentity{"owner-a": ownerA, "owner-b": ownerB, "auditor": auditor}
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
		AuthenticatedRoutes: catalog.AuthenticatedRoutes(repo),
	}).Handler()
	const items, lists, prices, offers = "/v1/catalog/items", "/v1/catalog/price-lists", "/v1/catalog/prices", "/v1/catalog/offers"
	const serviceBody = `{"kind":"service","name":" Serviço sintético ","unit":"service"}`
	const listBody = `{"name":"Tabela sintética","valid_from":"2026-11-10"}`
	request := func(t *testing.T, h http.Handler, method, path, token, body string, status int, code string) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-Amparius-Development-Tenant-ID", ownerB.TenantID.String())
		req.Header.Set("X-Tenant-ID", ownerB.TenantID.String())
		req.Header.Set("X-Company-ID", ownerB.CompanyID.String())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != status || rec.Header().Get("Cache-Control") != "no-store" ||
			(code != "" && !strings.Contains(rec.Body.String(), `"code":"`+code+`"`)) {
			t.Fatalf("%s %s: status=%d body=%s, esperado %d %s", method, path, rec.Code, rec.Body, status, code)
		}
		return rec.Body.Bytes()
	}

	t.Run("autenticação e permissões", func(t *testing.T) {
		unavailable := httpserver.New(logger, httpserver.Options{
			AuthenticatedRoutes: catalog.AuthenticatedRoutes(repo),
		}).Handler()
		noCatalog := httpserver.New(logger, httpserver.Options{
			Authenticator: gate, AuthenticatedRoutes: catalog.AuthenticatedRoutes(nil),
		}).Handler()
		for _, path := range []string{items, lists, prices, offers} {
			method := http.MethodPost
			if path == offers {
				method = http.MethodGet
			}
			request(t, handler, method, path, "", "", 401, "AUTHENTICATION_REQUIRED")
			request(t, handler, method, path, "invalid-token", "", 401, "AUTHENTICATION_REQUIRED")
			request(t, handler, method, path, "invalid-scope", "", 403, "SCOPE_FORBIDDEN")
			request(t, unavailable, method, path, "owner-a", "", 503, "AUTHENTICATION_UNAVAILABLE")
			request(t, noCatalog, method, path, "owner-a", "", 503, "CATALOG_UNAVAILABLE")
			if method == http.MethodPost {
				request(t, handler, method, path, "auditor", "", 403, "PERMISSION_DENIED")
			}
		}
	})

	decodeID := func(t *testing.T, body []byte) uuid.UUID {
		t.Helper()
		var result struct{ ID uuid.UUID }
		if err := json.Unmarshal(body, &result); err != nil || result.ID == uuid.Nil {
			t.Fatalf("resposta sem id: %s (%v)", body, err)
		}
		return result.ID
	}
	create := func(t *testing.T, path, token, body string) uuid.UUID {
		t.Helper()
		return decodeID(t, request(t, handler, http.MethodPost, path, token, body, 201, ""))
	}
	itemA := create(t, items, "owner-a", serviceBody)
	listA := create(t, lists, "owner-a", listBody)
	itemB := create(t, items, "owner-b", serviceBody)
	listB := create(t, lists, "owner-b", listBody)
	priceBody := func(itemID, listID uuid.UUID, amount, start, end string) string {
		return fmt.Sprintf(`{"item_id":"%s","list_id":"%s","amount_cents":%s,"valid_from":"%s","valid_to":%s}`,
			itemID, listID, amount, start, end)
	}
	startA := "2026-11-01"
	priceA := create(t, prices, "owner-a", priceBody(itemA, listA, "999999999999999999", startA, `"2026-11-20"`))
	nextA := create(t, prices, "owner-a", priceBody(itemA, listA, "0", "2026-11-21", "null"))
	priceB := create(t, prices, "owner-b", priceBody(itemB, listB, "123", startA, "null"))
	offerPath := func(itemID, listID uuid.UUID, on string) string {
		return fmt.Sprintf("%s?item_id=%s&list_id=%s&on=%s", offers, itemID, listID, on)
	}
	checkOffer := func(t *testing.T, on string, expected uuid.UUID, cents int64, validTo *string) {
		t.Helper()
		body := request(t, handler, http.MethodGet, offerPath(itemA, listA, on), "owner-a", "", 200, "")
		var result struct {
			Item struct {
				ID   uuid.UUID
				Name string
			}
			List struct {
				ID        uuid.UUID
				ValidFrom string `json:"valid_from"`
			} `json:"price_list"`
			Price struct {
				ID          uuid.UUID
				AmountCents int64   `json:"amount_cents"`
				ValidTo     *string `json:"valid_to"`
			}
		}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if result.Item.ID != itemA || result.Item.Name != "Serviço sintético" ||
			result.List.ID != listA || result.List.ValidFrom != "2026-11-10" ||
			result.Price.ID != expected || result.Price.AmountCents != cents ||
			(result.Price.ValidTo == nil) != (validTo == nil) ||
			(validTo != nil && *result.Price.ValidTo != *validTo) {
			t.Fatalf("oferta inesperada: %s", body)
		}
	}

	t.Run("vigência e valor persistido", func(t *testing.T) {
		request(t, handler, http.MethodGet, offerPath(itemA, listA, "2026-11-09"), "owner-a", "", 404, "OFFER_NOT_FOUND")
		end := "2026-11-20"
		checkOffer(t, "2026-11-10", priceA, 999_999_999_999_999_999, &end)
		checkOffer(t, "2026-11-20", priceA, 999_999_999_999_999_999, &end)
		checkOffer(t, "2026-11-21", nextA, 0, nil)
		checkOffer(t, "2027-01-01", nextA, 0, nil)

		// O mesmo contrato Go que a #76 consumirá recebe apenas referências e data.
		ctxA := db.WithTenant(ctx, ownerA.TenantID)
		resolved, err := repo.ResolveServiceOffer(ctxA, itemA, listA, "2026-11-21")
		if err != nil || resolved.Price.ID != nextA || resolved.Price.AmountCents != 0 {
			t.Fatalf("resolução direta = %+v, %v", resolved, err)
		}
		for _, pair := range [][2]uuid.UUID{{uuid.New(), listA}, {itemB, listB}, {itemA, listB}} {
			if _, err := repo.ResolveServiceOffer(ctxA, pair[0], pair[1], "2026-11-21"); !errors.Is(err, catalog.ErrOfferNotFound) {
				t.Fatalf("resolução direta de oferta ausente ou isolada: %v", err)
			}
		}
	})

	t.Run("datas de preço e serviços inelegíveis", func(t *testing.T) {
		day := "2026-11-15"
		for _, item := range []struct{ body, kind string }{
			{body: serviceBody, kind: "normal"},
			{body: `{"kind":"service","name":"Inativo sintético","unit":"service","active":false}`, kind: "inativo"},
			{body: serviceBody, kind: "excluído"},
		} {
			id := create(t, items, "owner-a", item.body)
			create(t, prices, "owner-a", priceBody(id, listA, "15", day, `"2026-11-15"`))
			if item.kind == "excluído" {
				if _, err := admin.Exec(ctx, "UPDATE catalog.catalog_item SET deleted_at=now() WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			}
			if item.kind == "normal" {
				request(t, handler, http.MethodGet, offerPath(id, listA, "2026-11-14"), "owner-a", "", 404, "OFFER_NOT_FOUND")
				request(t, handler, http.MethodGet, offerPath(id, listA, day), "owner-a", "", 200, "")
				request(t, handler, http.MethodGet, offerPath(id, listA, "2026-11-16"), "owner-a", "", 404, "OFFER_NOT_FOUND")
				if _, err := repo.ResolveServiceOffer(db.WithTenant(ctx, ownerA.TenantID), id, listA, "2026-11-16"); !errors.Is(err, catalog.ErrOfferNotFound) {
					t.Fatalf("resolução direta do preço vencido: %v", err)
				}
			} else {
				request(t, handler, http.MethodGet, offerPath(id, listA, day), "owner-a", "", 404, "OFFER_NOT_FOUND")
			}
		}
		product, err := repo.CreateItem(db.WithTenant(ctx, ownerA.TenantID), catalog.CatalogItem{
			Kind: catalog.KindProduct, Name: "Produto legado sintético", Unit: catalog.UnitUnit, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		create(t, prices, "owner-a", priceBody(product.ID, listA, "15", day, "null"))
		request(t, handler, http.MethodGet, offerPath(product.ID, listA, day), "owner-a", "", 404, "OFFER_NOT_FOUND")
	})

	t.Run("isolamento das três tabelas", func(t *testing.T) {
		request(t, handler, http.MethodGet, offerPath(itemB, listB, "2026-11-20"), "owner-a", "", 404, "OFFER_NOT_FOUND")
		request(t, handler, http.MethodGet, offerPath(itemA, listA, "2026-11-20"), "owner-b", "", 404, "OFFER_NOT_FOUND")
		request(t, handler, http.MethodGet, offerPath(itemA, listA, "2026-11-20"), "auditor", "", 404, "OFFER_NOT_FOUND")
		for _, pair := range [][2]uuid.UUID{{itemB, listA}, {itemA, listB}} {
			request(t, handler, http.MethodPost, prices, "owner-a",
				priceBody(pair[0], pair[1], "123", startA, "null"), 404, "CATALOG_REFERENCE_NOT_FOUND")
		}
		if err := database.WithTenantTx(db.WithTenant(ctx, ownerA.TenantID), func(ctx context.Context, tx pgx.Tx) error {
			var item, list, price bool
			err := tx.QueryRow(ctx, `SELECT
				EXISTS(SELECT 1 FROM catalog.catalog_item WHERE id=$1),
				EXISTS(SELECT 1 FROM catalog.price_list WHERE id=$2),
				EXISTS(SELECT 1 FROM catalog.price WHERE id=$3)`, itemB, listB, priceB).Scan(&item, &list, &price)
			if err == nil && (item || list || price) {
				return fmt.Errorf("RLS expôs item=%t tabela=%t preço=%t", item, list, price)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("entradas inválidas e substituição de preço", func(t *testing.T) {
		for _, tc := range []struct{ path, body string }{
			{items, strings.TrimSuffix(serviceBody, "}") + `,"tenant_id":"` + ownerB.TenantID.String() + `"}`},
			{items, strings.Replace(serviceBody, `"service"`, `"product"`, 1)},
			{items, strings.Replace(serviceBody, `"unit":"service"`, `"unit":"invalid"`, 1)},
			{items, strings.Replace(serviceBody, " Serviço sintético ", strings.Repeat("a", 201), 1)},
			{items, serviceBody + strings.Repeat(" ", 16<<10)},
			{lists, `{"name":"Tabela","valid_from":"0000-01-01"}`},
			{lists, `{"name":"Tabela","valid_from":"2026-02-30"}`},
			{prices, priceBody(itemA, listA, "1000000000000000000", startA, "null")},
			{prices, priceBody(itemA, listA, "1.00", startA, "null")},
			{prices, priceBody(itemA, listA, "-1", startA, "null")},
			{prices, priceBody(itemA, listA, "1", startA, `"2026-10-31"`)},
		} {
			request(t, handler, http.MethodPost, tc.path, "owner-a", tc.body, 400, "VALIDATION_ERROR")
		}
		for _, path := range []string{
			offerPath(itemA, listA, "2026-11-20") + "&amount_cents=1",
			offerPath(itemA, listA, "2026-11-20") + "&on=2026-11-21",
			offerPath(itemA, listA, "2026-11-1"),
			offerPath(itemA, listA, "0000-01-01"),
			offers,
		} {
			request(t, handler, http.MethodGet, path, "owner-a", "", 400, "VALIDATION_ERROR")
		}
	})

	t.Run("sobreposição no limite inclusivo", func(t *testing.T) {
		create(t, prices, "owner-a", priceBody(itemA, listA, "5", "2026-11-20", `"2026-11-20"`))
		request(t, handler, http.MethodGet, offerPath(itemA, listA, "2026-11-20"), "owner-a", "", 409, "OFFER_AMBIGUOUS")
		if _, err := repo.ResolveServiceOffer(db.WithTenant(ctx, ownerA.TenantID), itemA, listA, "2026-11-20"); !errors.Is(err, catalog.ErrOfferAmbiguous) {
			t.Fatalf("resolução direta ambígua: %v", err)
		}
		checkOffer(t, "2026-11-21", nextA, 0, nil)
	})
}
