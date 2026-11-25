package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/modules/identity"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

type fakeIdentityReader struct {
	tenant     identity.Tenant
	companies  identity.CompanyPage
	current    identity.Company
	allowed    bool
	listTenant uuid.UUID
}

func (f *fakeIdentityReader) GetTenant(context.Context) (identity.Tenant, error) {
	return f.tenant, nil
}
func (f *fakeIdentityReader) GetUserCompany(context.Context, uuid.UUID, uuid.UUID) (identity.Company, error) {
	return f.current, nil
}
func (f *fakeIdentityReader) ListUserCompanies(ctx context.Context, _ uuid.UUID, _ string, _ int) (identity.CompanyPage, error) {
	f.listTenant, _ = db.TenantFromContext(ctx)
	return f.companies, nil
}
func (f *fakeIdentityReader) UserCanAccessCompany(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.allowed, nil
}

type fakeEntitlementReader struct {
	called bool
}

func (f *fakeEntitlementReader) ListForCompany(context.Context, uuid.UUID, time.Time, string, int) (entitlement.EntitlementPage, error) {
	f.called = true
	return entitlement.EntitlementPage{Items: []entitlement.Entitlement{{
		TenantID: uuid.New(), CompanyID: uuid.New(), ModuleCode: entitlement.ModuleFuneral,
		ModuleName: "Funerária", Kind: "optional", Status: entitlement.StatusActive,
	}}}, nil
}

func authenticatedRequest(method, path string, principal platformauth.Principal, tenantID uuid.UUID) *http.Request {
	ctx := platformauth.WithPrincipal(context.Background(), principal)
	ctx = db.WithTenant(ctx, tenantID)
	ctx = db.WithCompany(ctx, principal.CompanyID)
	return httptest.NewRequestWithContext(ctx, method, path, nil)
}

func TestCompaniesUsesAuthenticatedTenantAndMasksDocument(t *testing.T) {
	tenantID, companyID, userID := uuid.New(), uuid.New(), uuid.New()
	identityReader := &fakeIdentityReader{
		tenant: identity.Tenant{ID: tenantID, Name: "Grupo", Status: "active"},
		companies: identity.CompanyPage{Items: []identity.Company{{
			ID: companyID, TenantID: tenantID, Document: "12345678000199", Name: "Funerária", Kind: "funeral", Status: "active",
			CreatedAt: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
		}}, NextCursor: "opaque-next"},
	}
	handler := NewHTTPHandler(identityReader, nil)
	principal := platformauth.Principal{Subject: "subject", UserID: userID, TenantID: tenantID, CompanyID: companyID, MFAAuthenticated: true}
	req := authenticatedRequest(http.MethodGet, "/v1/companies", principal, tenantID)
	req.Header.Set("X-Tenant-Id", uuid.NewString())
	rec := httptest.NewRecorder()
	handler.Companies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if identityReader.listTenant != tenantID {
		t.Fatalf("tenant usado = %s, esperado %s", identityReader.listTenant, tenantID)
	}
	var response struct {
		Items []struct {
			CNPJ string `json:"cnpj"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Items[0].CNPJ != "**********0199" || response.NextCursor != "opaque-next" {
		t.Fatalf("resposta = %+v", response)
	}
}

func TestEntitlementsRejectCompanyOutsideUserScopeWithoutCallingControlPlane(t *testing.T) {
	tenantID, companyID, userID := uuid.New(), uuid.New(), uuid.New()
	identityReader := &fakeIdentityReader{allowed: false}
	entitlementReader := &fakeEntitlementReader{}
	handler := NewHTTPHandler(identityReader, entitlementReader)
	principal := platformauth.Principal{Subject: "subject", UserID: userID, TenantID: tenantID, CompanyID: companyID, MFAAuthenticated: true}
	ctx := authenticatedRequest(http.MethodGet, "/v1/companies/"+uuid.NewString()+"/entitlements", principal, tenantID).Context()
	ctx = observability.WithTraceID(ctx, "0123456789abcdef0123456789abcdef")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/companies/"+uuid.NewString()+"/entitlements", nil)
	req.SetPathValue("companyId", uuid.NewString())
	rec := httptest.NewRecorder()
	handler.Entitlements(rec, req)

	if rec.Code != http.StatusForbidden || entitlementReader.called {
		t.Fatalf("status=%d called=%v body=%s", rec.Code, entitlementReader.called, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != "TENANT_MISMATCH" || response["traceId"] != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("erro estruturado = %+v", response)
	}
}

func TestContextReturnsOnlyTokenBoundCompany(t *testing.T) {
	tenantID, companyID, userID := uuid.New(), uuid.New(), uuid.New()
	identityReader := &fakeIdentityReader{
		tenant:  identity.Tenant{ID: tenantID, Name: "Grupo", Status: "active"},
		current: identity.Company{ID: companyID, TenantID: tenantID, Document: "12345678000199", Name: "Funerária", Status: "active", CreatedAt: time.Now()},
	}
	handler := NewHTTPHandler(identityReader, nil)
	principal := platformauth.Principal{Subject: "subject", UserID: userID, TenantID: tenantID, CompanyID: companyID, MFAAuthenticated: true}
	req := authenticatedRequest(http.MethodGet, "/v1/context", principal, tenantID)
	rec := httptest.NewRecorder()
	handler.Context(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), companyID.String()) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
