package funeral_test

import (
	"context"
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

type serviceOrderHTTPFake struct {
	created funeral.ServiceOrder
	updated funeral.ServiceOrder
	err     error
}

func (f *serviceOrderHTTPFake) CreateServiceOrder(_ context.Context, input funeral.ServiceOrderCreate) (funeral.ServiceOrder, error) {
	if f.err != nil {
		return funeral.ServiceOrder{}, f.err
	}
	f.created = funeral.ServiceOrder{ID: uuid.New(), CompanyID: input.CompanyID, ContractID: input.ContractID, Kind: input.Kind, Description: input.Description, Status: funeral.ServiceOrderOpen, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), StatusHistory: []funeral.ServiceOrderStatusChange{{ID: uuid.New(), ToStatus: funeral.ServiceOrderOpen, ChangedAt: time.Now().UTC()}}}
	return f.created, nil
}
func (f *serviceOrderHTTPFake) CreateContract(context.Context, funeral.ContractCreate) (funeral.Contract, error) {
	return funeral.Contract{}, f.err
}
func (f *serviceOrderHTTPFake) GetContractByID(context.Context, uuid.UUID) (funeral.Contract, error) {
	return funeral.Contract{}, f.err
}
func (f *serviceOrderHTTPFake) ChangeContractStatus(context.Context, uuid.UUID, string, *string) (funeral.Contract, error) {
	return funeral.Contract{}, f.err
}
func (f *serviceOrderHTTPFake) ListServiceOrders(context.Context, uuid.UUID) ([]funeral.ServiceOrder, error) {
	return []funeral.ServiceOrder{f.created}, f.err
}
func (f *serviceOrderHTTPFake) GetServiceOrderByID(context.Context, uuid.UUID) (funeral.ServiceOrder, error) {
	return f.updated, f.err
}
func (f *serviceOrderHTTPFake) UpdateServiceOrder(_ context.Context, input funeral.ServiceOrderPatch) (funeral.ServiceOrder, error) {
	if f.err != nil {
		return funeral.ServiceOrder{}, f.err
	}
	f.updated = f.created
	f.updated.ID = input.ID
	if input.Status != "" {
		f.updated.Status = input.Status
	}
	return f.updated, nil
}
func (f *serviceOrderHTTPFake) CompleteServiceOrder(_ context.Context, _ uuid.UUID, _ string) (funeral.ServiceOrder, error) {
	if f.err != nil {
		return funeral.ServiceOrder{}, f.err
	}
	f.updated = f.created
	f.updated.Status = funeral.ServiceOrderCompleted
	return f.updated, nil
}

func serviceOrderRequest(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), uuid.New()), uuid.New()))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDevelopmentServiceOrderJourneyRoutesAndStrictJSON(t *testing.T) {
	fake := &serviceOrderHTTPFake{}
	handler := funeral.NewDevelopmentHTTPHandler(contractChecker{}, fake)
	contractID, orderID := uuid.New(), uuid.New()
	created := serviceOrderRequest(handler, http.MethodPost, "/_dev/funeral/contracts/"+contractID.String()+"/service-orders", `{"kind":"funeral_service","description":"atendimento","scheduled_for":"2026-11-25T12:00:00-03:00"}`, "os-create-1")
	if created.Code != http.StatusCreated || created.Header().Get("X-Amparius-Development-Only") != "true" || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("criação = %d/%s", created.Code, created.Body.String())
	}
	if fake.created.ContractID != contractID {
		t.Fatalf("contrato não propagado: %s", fake.created.ContractID)
	}
	started := serviceOrderRequest(handler, http.MethodPatch, "/_dev/funeral/service-orders/"+orderID.String()+"/status", `{"status":"in_progress"}`, "os-start-1")
	if started.Code != http.StatusOK || !strings.Contains(started.Body.String(), `"status":"in_progress"`) {
		t.Fatalf("avanço de estado = %d/%s", started.Code, started.Body.String())
	}
	unknown := serviceOrderRequest(handler, http.MethodPost, "/_dev/funeral/contracts/"+contractID.String()+"/service-orders", `{"kind":"funeral_service","tenant_id":"forbidden"}`, "os-create-2")
	if unknown.Code != http.StatusBadRequest || strings.Contains(unknown.Body.String(), "tenant_id") {
		t.Fatalf("JSON desconhecido = %d/%s", unknown.Code, unknown.Body.String())
	}
	complete := serviceOrderRequest(handler, http.MethodPost, "/_dev/funeral/service-orders/"+orderID.String()+"/complete", `{}`, "os-complete-1")
	if complete.Code != http.StatusOK || !strings.Contains(complete.Body.String(), `"status":"completed"`) {
		t.Fatalf("conclusão = %d/%s", complete.Code, complete.Body.String())
	}
}

func TestDevelopmentServiceOrderErrorsDoNotLeakPII(t *testing.T) {
	fake := &serviceOrderHTTPFake{err: errors.New("falha interna pessoa@example.com")}
	handler := funeral.NewDevelopmentHTTPHandler(contractChecker{}, fake)
	result := serviceOrderRequest(handler, http.MethodGet, "/_dev/funeral/service-orders/"+uuid.NewString(), "", "")
	if result.Code != http.StatusInternalServerError || strings.Contains(result.Body.String(), "pessoa@example.com") {
		t.Fatalf("erro = %d/%s", result.Code, result.Body.String())
	}
}
