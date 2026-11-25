package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

type developmentResolver struct {
	belongs bool
	err     error
}

func (r developmentResolver) CompanyBelongsToTenant(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return r.belongs, r.err
}

func TestDevelopmentTenantCompanyContextComposesTrustedScope(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	var got context.Context
	handler := DevelopmentTenantCompanyContext(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Context()
	}), developmentResolver{belongs: true})
	req := httptest.NewRequest(http.MethodGet, "/_dev/funeral/access", nil)
	req.Header.Set(DevelopmentTenantHeader, tenantID.String())
	req.Header.Set(DevelopmentCompanyHeader, companyID.String())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", rec.Code)
	}
	if got == nil {
		t.Fatal("handler downstream não recebeu contexto")
	}
	if value, err := db.TenantFromContext(got); err != nil || value != tenantID {
		t.Fatalf("tenant no contexto = %s/%v", value, err)
	}
	if value, err := db.CompanyFromContext(got); err != nil || value != companyID {
		t.Fatalf("empresa no contexto = %s/%v", value, err)
	}
}

func TestDevelopmentTenantCompanyContextDoesNotAcceptForeignCompany(t *testing.T) {
	handler := DevelopmentTenantCompanyContext(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("escopo estrangeiro não deveria alcançar o endpoint")
	}), developmentResolver{})
	req := httptest.NewRequest(http.MethodGet, "/_dev/funeral/access", nil)
	req.Header.Set(DevelopmentTenantHeader, uuid.NewString())
	req.Header.Set(DevelopmentCompanyHeader, uuid.NewString())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperado 404", rec.Code)
	}
}

func TestDevelopmentTenantCompanyContextRejectsMissingHeaders(t *testing.T) {
	handler := DevelopmentTenantCompanyContext(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("escopo inválido não deveria alcançar o endpoint")
	}), developmentResolver{belongs: true})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_dev/funeral/access", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado 400", rec.Code)
	}
}
