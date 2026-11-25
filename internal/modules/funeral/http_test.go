package funeral_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

type fakeChecker struct{ err error }

func (f fakeChecker) Check(context.Context, string, time.Time) error { return f.err }

func request(handler http.Handler, method string, ctx context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/_dev/funeral/access", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestFuneralEndpointAllowsLicensedCompany(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	rec := request(funeral.NewDevelopmentHTTPHandler(fakeChecker{}), http.MethodGet,
		db.WithCompany(db.WithTenant(context.Background(), tenantID), companyID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["access"] != "granted" {
		t.Fatalf("resposta = %v", body)
	}
}

func TestFuneralEndpointBlocksDisabledModuleBeforeDomain(t *testing.T) {
	rec := request(funeral.NewDevelopmentHTTPHandler(fakeChecker{err: entitlement.ErrModuleDisabled}), http.MethodGet, context.Background())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, esperado 403", rec.Code)
	}
	if got := rec.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("header dev-only = %q", got)
	}
}

func TestFuneralEndpointDistinguishesControlPlaneFailure(t *testing.T) {
	rec := request(funeral.NewDevelopmentHTTPHandler(fakeChecker{err: entitlement.ErrEntitlementUnavailable}), http.MethodGet, context.Background())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, esperado 503", rec.Code)
	}
}

func TestFuneralEndpointRejectsMethodsAndUnknownRoutes(t *testing.T) {
	handler := funeral.NewDevelopmentHTTPHandler(fakeChecker{})
	if rec := request(handler, http.MethodPost, context.Background()); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, esperado 405", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/_dev/funeral/other", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rota desconhecida status = %d, esperado 404", rec.Code)
	}
}
