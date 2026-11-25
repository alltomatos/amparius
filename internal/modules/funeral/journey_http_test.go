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

func (s *contractService) CreateFuneralJourney(_ context.Context, input funeral.FuneralJourneyCreate) (funeral.FuneralJourney, error) {
	if s.err != nil {
		return funeral.FuneralJourney{}, s.err
	}
	return funeral.FuneralJourney{
		ID: uuid.New(), ContractID: input.ContractID, CompanyID: uuid.New(), CreatedAt: time.Now().UTC(),
		ServiceOrder: funeral.ServiceOrder{ID: uuid.New(), ContractID: input.ContractID, CompanyID: uuid.New(), Kind: input.ServiceOrderKind, Status: funeral.ServiceOrderOpen},
		Schedule:     funeral.Schedule{ID: uuid.New(), ContractID: input.ContractID, CompanyID: uuid.New(), ServiceOrderID: func() *uuid.UUID { id := uuid.New(); return &id }(), Kind: input.ScheduleKind, LocationID: input.LocationID, StartsAt: input.StartsAt, EndsAt: input.EndsAt, TimeZone: input.TimeZone, Status: funeral.ScheduleScheduled, Version: 1},
	}, nil
}

func TestDevelopmentFuneralJourneyIsStrictAndDoesNotLeakErrors(t *testing.T) {
	service := &contractService{}
	handler := funeral.NewDevelopmentHTTPHandlerWithScheduling(contractChecker{}, service, &schedulingHTTPFake{})
	contractID, locationID := uuid.New(), uuid.New()
	body := `{"service_order":{"kind":"wake","description":"atendimento"},"schedule":{"kind":"wake","location_id":"` + locationID.String() + `","starts_at":"2026-11-25T12:00:00-03:00","ends_at":"2026-11-25T14:00:00-03:00","time_zone":"America/Fortaleza"}}`
	req := httptest.NewRequest(http.MethodPost, "/_dev/funeral/contracts/"+contractID.String()+"/journey", strings.NewReader(body))
	req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), uuid.New()), uuid.New()))
	req.Header.Set("Idempotency-Key", "journey-http-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"journey"`) || rec.Header().Get("X-Amparius-Development-Only") != "true" {
		t.Fatalf("jornada = %d/%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/_dev/funeral/contracts/"+contractID.String()+"/journey", strings.NewReader(`{"service_order":{"kind":"wake","description":"ok"},"schedule":{"kind":"wake","location_id":"`+locationID.String()+`","starts_at":"2026-11-25T12:00:00-03:00","ends_at":"2026-11-25T14:00:00-03:00","time_zone":"America/Fortaleza","tenant_id":"forbidden"}}`))
	req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), uuid.New()), uuid.New()))
	req.Header.Set("Idempotency-Key", "journey-http-2")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "tenant_id") {
		t.Fatalf("JSON estrito = %d/%s", rec.Code, rec.Body.String())
	}

	service.err = errors.New("falha interna pessoa@example.com")
	req = httptest.NewRequest(http.MethodPost, "/_dev/funeral/contracts/"+contractID.String()+"/journey", strings.NewReader(body))
	req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), uuid.New()), uuid.New()))
	req.Header.Set("Idempotency-Key", "journey-http-3")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "pessoa@example.com") {
		t.Fatalf("erro sem PII = %d/%s", rec.Code, rec.Body.String())
	}
}
