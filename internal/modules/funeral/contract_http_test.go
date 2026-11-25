package funeral_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

type contractChecker struct{ err error }

func (c contractChecker) Check(context.Context, string, time.Time) error { return c.err }

type contractService struct {
	created funeral.Contract
	got     funeral.Contract
	changed funeral.Contract
	err     error
}

func (s *contractService) CreateContract(_ context.Context, input funeral.ContractCreate) (funeral.Contract, error) {
	if s.err != nil {
		return funeral.Contract{}, s.err
	}
	s.created = funeral.Contract{ID: uuid.New(), CompanyID: input.CompanyID, CustomerPartyID: input.CustomerPartyID, DeceasedID: input.DeceasedID, Status: funeral.ContractDraft, TotalAmountCents: 15000, Items: []funeral.ContractItem{{ID: uuid.New(), CatalogItemID: input.Items[0].CatalogItemID, Description: "Urna", Unit: "unit", Quantity: 1, UnitAmountCents: 15000, TotalAmountCents: 15000}}}
	return s.created, nil
}

func (s *contractService) GetContractByID(context.Context, uuid.UUID) (funeral.Contract, error) {
	if s.err != nil {
		return funeral.Contract{}, s.err
	}
	return s.got, nil
}

func (s *contractService) ChangeContractStatus(_ context.Context, _ uuid.UUID, status string, _ *string) (funeral.Contract, error) {
	if s.err != nil {
		return funeral.Contract{}, s.err
	}
	s.changed.Status = status
	return s.changed, nil
}

func funeralRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), uuid.New()), uuid.New()))
	req.Header.Set("Idempotency-Key", "contract-http-001")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDevelopmentContractCreateStrictAndResponse(t *testing.T) {
	service := &contractService{}
	handler := funeral.NewDevelopmentHTTPHandler(contractChecker{}, service)
	customer, deceased, item := uuid.New(), uuid.New(), uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/_dev/funeral/contracts", strings.NewReader(`{"customer_party_id":"`+customer.String()+`","deceased_id":"`+deceased.String()+`","family_link_ids":[],"items":[{"catalog_item_id":"`+item.String()+`","quantity":1,"unit_amount_cents":15000}]}`))
	company := uuid.New()
	req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), uuid.New()), company))
	req.Header.Set("Idempotency-Key", "contract-http-001")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status/cache-control = %d/%q, body=%s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	var response struct {
		Contract struct {
			CompanyID string `json:"company_id"`
			Total     int64  `json:"total_amount_cents"`
		} `json:"contract"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Contract.CompanyID != company.String() || response.Contract.Total != 15000 {
		t.Fatalf("resposta = %#v", response)
	}
	if service.created.CompanyID != company {
		t.Fatalf("empresa do contexto não propagada: %s", service.created.CompanyID)
	}
}

func TestDevelopmentContractRejectsUnknownFieldsAndMissingIdempotency(t *testing.T) {
	handler := funeral.NewDevelopmentHTTPHandler(contractChecker{}, &contractService{})
	for name, body := range map[string]string{
		"unknown": `{"customer_party_id":"` + uuid.NewString() + `","deceased_id":"` + uuid.NewString() + `","items":[],"tenant_id":"nao"}`,
		"missing": `{"customer_party_id":"` + uuid.NewString() + `","deceased_id":"` + uuid.NewString() + `","items":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/_dev/funeral/contracts", strings.NewReader(body))
			req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), uuid.New()), uuid.New()))
			if name == "unknown" {
				req.Header.Set("Idempotency-Key", "strict-001")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "tenant_id") {
				t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDevelopmentContractStatusAndErrors(t *testing.T) {
	service := &contractService{err: funeral.ErrIdempotencyConflict}
	handler := funeral.NewDevelopmentHTTPHandler(contractChecker{}, service)
	rec := funeralRequest(handler, http.MethodPatch, "/_dev/funeral/contracts/"+uuid.NewString()+"/status", `{"status":"active"}`)
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "não") && strings.Contains(rec.Body.String(), "cliente") {
		t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
	}
	service.err = errors.New("falha interna com PII pessoa@example.com")
	rec = funeralRequest(handler, http.MethodGet, "/_dev/funeral/contracts/"+uuid.NewString(), "")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "pessoa@example.com") {
		t.Fatalf("erro exposto = %d/%s", rec.Code, rec.Body.String())
	}
}
