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

type schedulingHTTPFake struct {
	location funeral.Location
	resource funeral.Resource
	schedule funeral.Schedule
	err      error
}

func (f *schedulingHTTPFake) CreateLocation(_ context.Context, input funeral.LocationCreate) (funeral.Location, error) {
	if f.err != nil {
		return funeral.Location{}, f.err
	}
	f.location = funeral.Location{ID: uuid.New(), CompanyID: input.CompanyID, Name: input.Name, Kind: input.Kind, TimeZone: input.TimeZone, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	return f.location, nil
}
func (f *schedulingHTTPFake) ListLocations(context.Context) ([]funeral.Location, error) {
	return []funeral.Location{f.location}, f.err
}
func (f *schedulingHTTPFake) CreateResource(_ context.Context, input funeral.ResourceCreate) (funeral.Resource, error) {
	if f.err != nil {
		return funeral.Resource{}, f.err
	}
	f.resource = funeral.Resource{ID: uuid.New(), CompanyID: input.CompanyID, LocationID: input.LocationID, Name: input.Name, Kind: input.Kind, Active: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	return f.resource, nil
}
func (f *schedulingHTTPFake) ListResources(context.Context, uuid.UUID) ([]funeral.Resource, error) {
	return []funeral.Resource{f.resource}, f.err
}
func (f *schedulingHTTPFake) CreateSchedule(_ context.Context, input funeral.ScheduleCreate) (funeral.Schedule, error) {
	if f.err != nil {
		return funeral.Schedule{}, f.err
	}
	f.schedule = funeral.Schedule{ID: uuid.New(), CompanyID: input.CompanyID, ContractID: input.ContractID, ServiceOrderID: input.ServiceOrderID, Kind: input.Kind, LocationID: input.LocationID, ResourceID: input.ResourceID, StartsAt: input.StartsAt, EndsAt: input.EndsAt, TimeZone: input.TimeZone, Status: funeral.ScheduleScheduled, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	return f.schedule, nil
}
func (f *schedulingHTTPFake) ListSchedules(context.Context, uuid.UUID) ([]funeral.Schedule, error) {
	return []funeral.Schedule{f.schedule}, f.err
}
func (f *schedulingHTTPFake) GetScheduleByID(context.Context, uuid.UUID) (funeral.Schedule, error) {
	return f.schedule, f.err
}
func (f *schedulingHTTPFake) UpdateSchedule(context.Context, funeral.SchedulePatch) (funeral.Schedule, error) {
	return f.schedule, f.err
}
func (f *schedulingHTTPFake) CancelSchedule(context.Context, uuid.UUID, string) (funeral.Schedule, error) {
	if f.err != nil {
		return funeral.Schedule{}, f.err
	}
	f.schedule.Status = funeral.ScheduleCancelled
	return f.schedule, nil
}

func (f *schedulingHTTPFake) ConfirmSchedule(context.Context, uuid.UUID, string) (funeral.Schedule, error) {
	if f.err != nil {
		return funeral.Schedule{}, f.err
	}
	f.schedule.Status = funeral.ScheduleConfirmed
	return f.schedule, nil
}

func (f *schedulingHTTPFake) ConfirmScheduleWithReason(_ context.Context, _ uuid.UUID, _ string, _ string) (funeral.Schedule, error) {
	return f.ConfirmSchedule(context.Background(), uuid.Nil, "")
}

func (f *schedulingHTTPFake) CancelScheduleWithReason(_ context.Context, id uuid.UUID, key, _ string) (funeral.Schedule, error) {
	return f.CancelSchedule(context.Background(), id, key)
}

func scheduleHTTPRequest(handler http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(db.WithCompany(db.WithTenant(req.Context(), uuid.New()), uuid.New()))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDevelopmentScheduleHTTPIsStrictAndConflictSafe(t *testing.T) {
	fake := &schedulingHTTPFake{}
	handler := funeral.NewDevelopmentHTTPHandlerWithScheduling(contractChecker{}, &contractService{}, fake)
	location := scheduleHTTPRequest(handler, http.MethodPost, "/_dev/funeral/locations", `{"name":"Sala 1","kind":"wake_room","time_zone":"America/Fortaleza"}`, "location-1")
	if location.Code != http.StatusCreated || location.Header().Get("Cache-Control") != "no-store" || location.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("local = %d/%s", location.Code, location.Body.String())
	}
	resource := scheduleHTTPRequest(handler, http.MethodPost, "/_dev/funeral/resources", `{"location_id":"`+fake.location.ID.String()+`","name":"Sala 1","kind":"room"}`, "resource-1")
	if resource.Code != http.StatusCreated {
		t.Fatalf("recurso = %d/%s", resource.Code, resource.Body.String())
	}
	contractID := uuid.New()
	strict := scheduleHTTPRequest(handler, http.MethodPost, "/_dev/funeral/schedules", `{"contract_id":"`+contractID.String()+`","kind":"wake","location_id":"`+fake.location.ID.String()+`","starts_at":"2026-11-25T12:00:00-03:00","ends_at":"2026-11-25T14:00:00-03:00","time_zone":"America/Fortaleza","tenant_id":"forbidden"}`, "schedule-1")
	if strict.Code != http.StatusBadRequest || strings.Contains(strict.Body.String(), "tenant_id") {
		t.Fatalf("JSON estrito = %d/%s", strict.Code, strict.Body.String())
	}
	created := scheduleHTTPRequest(handler, http.MethodPost, "/_dev/funeral/schedules", `{"contract_id":"`+contractID.String()+`","kind":"wake","location_id":"`+fake.location.ID.String()+`","resource_id":"`+fake.resource.ID.String()+`","starts_at":"2026-11-25T12:00:00-03:00","ends_at":"2026-11-25T14:00:00-03:00","time_zone":"America/Fortaleza"}`, "schedule-1")
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"time_zone":"America/Fortaleza"`) {
		t.Fatalf("agenda = %d/%s", created.Code, created.Body.String())
	}
	confirmed := scheduleHTTPRequest(handler, http.MethodPost, "/_dev/funeral/schedules/"+fake.schedule.ID.String()+"/confirm", `{"reason":"cliente confirmou"}`, "schedule-confirm")
	if confirmed.Code != http.StatusOK || !strings.Contains(confirmed.Body.String(), `"status":"confirmed"`) {
		t.Fatalf("confirmacao = %d/%s", confirmed.Code, confirmed.Body.String())
	}
	listed := scheduleHTTPRequest(handler, http.MethodGet, "/_dev/funeral/contracts/"+contractID.String()+"/schedules", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"schedules"`) {
		t.Fatalf("listagem por contrato = %d/%s", listed.Code, listed.Body.String())
	}
	fake.err = funeral.ErrScheduleConflict
	conflict := scheduleHTTPRequest(handler, http.MethodPost, "/_dev/funeral/schedules", `{"contract_id":"`+contractID.String()+`","kind":"wake","location_id":"`+fake.location.ID.String()+`","starts_at":"2026-11-25T12:00:00-03:00","ends_at":"2026-11-25T14:00:00-03:00","time_zone":"America/Fortaleza"}`, "schedule-2")
	if conflict.Code != http.StatusConflict || strings.Contains(conflict.Body.String(), "Sala") {
		t.Fatalf("conflito = %d/%s", conflict.Code, conflict.Body.String())
	}
	fake.err = errors.New("falha interna pessoa@example.com")
	failed := scheduleHTTPRequest(handler, http.MethodGet, "/_dev/funeral/schedules/"+uuid.NewString(), "", "")
	if failed.Code != http.StatusInternalServerError || strings.Contains(failed.Body.String(), "pessoa@example.com") {
		t.Fatalf("erro sem PII = %d/%s", failed.Code, failed.Body.String())
	}
}
