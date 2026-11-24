package catalog_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/catalog"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
)

type fakeService struct {
	createdItem      catalog.CatalogItem
	itemErr          error
	listedItems      []catalog.CatalogItem
	listedItemLimit  int
	createdPriceList catalog.PriceList
	priceListErr     error
	listedLists      []catalog.PriceList
	listedListLimit  int
	createdPrice     catalog.Price
	priceErr         error
	listedPrices     []catalog.Price
	listedPriceLimit int
}

func (f *fakeService) CreateItem(_ context.Context, item catalog.CatalogItem) (catalog.CatalogItem, error) {
	if f.itemErr != nil {
		return catalog.CatalogItem{}, f.itemErr
	}
	f.createdItem = item
	item.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000001")
	return item, nil
}

func (f *fakeService) GetItemByID(_ context.Context, _ uuid.UUID) (catalog.CatalogItem, error) {
	if f.itemErr != nil {
		return catalog.CatalogItem{}, f.itemErr
	}
	return catalog.CatalogItem{ID: uuid.MustParse("018f0000-0000-7000-8000-000000000001"), Kind: "service", Name: "Item", Unit: "unit", Active: true}, nil
}

func (f *fakeService) ListItems(_ context.Context, limit int) ([]catalog.CatalogItem, error) {
	f.listedItemLimit = limit
	return f.listedItems, nil
}

func (f *fakeService) CreatePriceList(_ context.Context, list catalog.PriceList) (catalog.PriceList, error) {
	if f.priceListErr != nil {
		return catalog.PriceList{}, f.priceListErr
	}
	f.createdPriceList = list
	list.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000002")
	return list, nil
}

func (f *fakeService) GetPriceListByID(_ context.Context, _ uuid.UUID) (catalog.PriceList, error) {
	if f.priceListErr != nil {
		return catalog.PriceList{}, f.priceListErr
	}
	return catalog.PriceList{ID: uuid.MustParse("018f0000-0000-7000-8000-000000000002"), Name: "Lista", ValidFrom: "2026-01-01"}, nil
}

func (f *fakeService) ListPriceLists(_ context.Context, limit int) ([]catalog.PriceList, error) {
	f.listedListLimit = limit
	return f.listedLists, nil
}

func (f *fakeService) CreatePrice(_ context.Context, price catalog.Price) (catalog.Price, error) {
	if f.priceErr != nil {
		return catalog.Price{}, f.priceErr
	}
	f.createdPrice = price
	price.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000003")
	return price, nil
}

func (f *fakeService) GetPriceByID(_ context.Context, _ uuid.UUID) (catalog.Price, error) {
	if f.priceErr != nil {
		return catalog.Price{}, f.priceErr
	}
	return catalog.Price{
		ID:          uuid.MustParse("018f0000-0000-7000-8000-000000000003"),
		ItemID:      uuid.MustParse("018f0000-0000-7000-8000-000000000001"),
		ListID:      uuid.MustParse("018f0000-0000-7000-8000-000000000002"),
		AmountCents: 12345,
		ValidFrom:   "2026-01-01",
	}, nil
}

func (f *fakeService) ListPrices(_ context.Context, limit int) ([]catalog.Price, error) {
	f.listedPriceLimit = limit
	return f.listedPrices, nil
}

func newCatalogHandler(service catalog.DevelopmentService) http.Handler {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpserver.New(logger, httpserver.Options{
		DevelopmentMode:    true,
		DevelopmentCatalog: catalog.NewDevelopmentHTTPHandler(service),
	}).Handler()
}

func catalogRequest(handler http.Handler, method, path, tenant, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenant != "" {
		req.Header.Set("X-Amparius-Development-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDevelopmentCatalogRequiresValidTenantHeader(t *testing.T) {
	handler := newCatalogHandler(&fakeService{})
	for _, tenant := range []string{"", "nao-uuid", uuid.Nil.String()} {
		rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/items", tenant, `{"kind":"service","name":"X","unit":"unit"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("tenant %q = %d, quer 400", tenant, rec.Code)
		}
		if rec.Header().Get("X-Amparius-Development-Only") != "true" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("headers de proteção ausentes: %#v", rec.Header())
		}
	}
}

func TestDevelopmentCatalogCreateItem(t *testing.T) {
	service := &fakeService{}
	handler := newCatalogHandler(service)
	tenant := uuid.New()
	rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/items", tenant.String(),
		`{"kind":"service","name":"  Missa  ","unit":"service"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/_dev/catalog/items/018f0000-0000-7000-8000-000000000001" {
		t.Fatalf("Location = %q", loc)
	}
	if service.createdItem.Name != "Missa" || service.createdItem.Kind != "service" || !service.createdItem.Active {
		t.Fatalf("item persistido = %#v", service.createdItem)
	}
	var body struct {
		Status string              `json:"status"`
		Item   catalogItemResponse `json:"item"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Item.ID == "" || body.Item.Active != true {
		t.Fatalf("resposta = %s", rec.Body.String())
	}
}

type catalogItemResponse struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Unit   string `json:"unit"`
	Active bool   `json:"active"`
}

func TestDevelopmentCatalogRejectsUnknownAndTenantFields(t *testing.T) {
	handler := newCatalogHandler(&fakeService{})
	tenant := uuid.New().String()
	cases := []string{
		`{"kind":"service","name":"X","unit":"unit","tenant_id":"` + uuid.NewString() + `"}`,
		`{"kind":"service","name":"X","unit":"unit","extra":1}`,
	}
	for _, body := range cases {
		rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/items", tenant, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("corpo %s = %d, quer 400: %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestDevelopmentCatalogRejectsInvalidDomain(t *testing.T) {
	tenant := uuid.New().String()
	for _, serviceErr := range []error{catalog.ErrInvalidKind, catalog.ErrInvalidUnit} {
		handler := newCatalogHandler(&fakeService{itemErr: serviceErr})
		rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/items", tenant,
			`{"kind":"service","name":"X","unit":"unit"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%v = %d, quer 400: %s", serviceErr, rec.Code, rec.Body.String())
		}
	}
}

func TestDevelopmentCatalogGetItemNotFound(t *testing.T) {
	handler := newCatalogHandler(&fakeService{itemErr: catalog.ErrNotFound})
	rec := catalogRequest(handler, http.MethodGet, "/_dev/catalog/items/"+uuid.NewString(), uuid.NewString(), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quer 404: %s", rec.Code, rec.Body.String())
	}
}

func TestDevelopmentCatalogListLimit(t *testing.T) {
	service := &fakeService{}
	handler := newCatalogHandler(service)
	tenant := uuid.New().String()

	rec := catalogRequest(handler, http.MethodGet, "/_dev/catalog/items?limit=25", tenant, "")
	if rec.Code != http.StatusOK || service.listedItemLimit != 25 {
		t.Fatalf("limite = %d, status = %d", service.listedItemLimit, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("lista vazia deve serializar como array: %s", rec.Body.String())
	}
	if rec := catalogRequest(handler, http.MethodGet, "/_dev/catalog/items?limit=0", tenant, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=0 = %d, quer 400", rec.Code)
	}
	if rec := catalogRequest(handler, http.MethodGet, "/_dev/catalog/items?limit=abc", tenant, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=abc = %d, quer 400", rec.Code)
	}
}

func TestDevelopmentCatalogPriceAmountStrict(t *testing.T) {
	handler := newCatalogHandler(&fakeService{})
	tenant := uuid.New().String()
	itemID, listID := uuid.NewString(), uuid.NewString()
	base := func(amount string) string {
		return `{"item_id":"` + itemID + `","list_id":"` + listID + `","amount_cents":` + amount + `,"valid_from":"2026-01-01"}`
	}
	for _, amount := range []string{`12.5`, `"100"`, `1e3`, `-1`, `null`} {
		rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/prices", tenant, base(amount))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("amount_cents=%s = %d, quer 400: %s", amount, rec.Code, rec.Body.String())
		}
	}
	rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/prices", tenant, base(`12345`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("inteiro válido = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDevelopmentCatalogPriceValidity(t *testing.T) {
	handler := newCatalogHandler(&fakeService{})
	tenant := uuid.New().String()
	itemID, listID := uuid.NewString(), uuid.NewString()
	body := `{"item_id":"` + itemID + `","list_id":"` + listID + `","amount_cents":100,"valid_from":"2026-02-01","valid_to":"2026-01-01"}`
	if rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/prices", tenant, body); rec.Code != http.StatusBadRequest {
		t.Fatalf("vigência invertida = %d, quer 400: %s", rec.Code, rec.Body.String())
	}
	dateBody := `{"item_id":"` + itemID + `","list_id":"` + listID + `","amount_cents":100,"valid_from":"01/02/2026"}`
	if rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/prices", tenant, dateBody); rec.Code != http.StatusBadRequest {
		t.Fatalf("data fora do ISO = %d, quer 400", rec.Code)
	}
}

func TestDevelopmentCatalogPriceReferenceNotFound(t *testing.T) {
	tenant := uuid.New().String()
	for _, serviceErr := range []error{catalog.ErrItemNotFound, catalog.ErrPriceListNotFound} {
		handler := newCatalogHandler(&fakeService{priceErr: serviceErr})
		rec := catalogRequest(handler, http.MethodPost, "/_dev/catalog/prices", tenant,
			`{"item_id":"`+uuid.NewString()+`","list_id":"`+uuid.NewString()+`","amount_cents":100,"valid_from":"2026-01-01"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%v = %d, quer 404: %s", serviceErr, rec.Code, rec.Body.String())
		}
	}
}

func TestDevelopmentCatalogRoutesAbsentWithoutDevelopmentMode(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger).Handler()
	for _, path := range []string{"/_dev/catalog/items", "/_dev/catalog/price-lists", "/_dev/catalog/prices"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Amparius-Development-Tenant-ID", uuid.NewString())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s sem opt-in = %d, quer 404", path, rec.Code)
		}
	}
}
