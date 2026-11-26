package controlplane

import (
	"context"
	"encoding/json"
	"io"
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
	called       bool
	detail       entitlement.Entitlement
	grantCalled  bool
	revokeCalled bool
	quotaCalled  bool
	auditCalled  bool
	assignment   entitlement.Assignment
}

func (f *fakeEntitlementReader) ListForCompany(context.Context, uuid.UUID, time.Time, string, int) (entitlement.EntitlementPage, error) {
	f.called = true
	return entitlement.EntitlementPage{Items: []entitlement.Entitlement{{
		TenantID: uuid.New(), CompanyID: uuid.New(), ModuleCode: entitlement.ModuleFuneral,
		ModuleName: "Funerária", Kind: "optional", Status: entitlement.StatusActive,
	}}}, nil
}

func (f *fakeEntitlementReader) GetForCompany(context.Context, uuid.UUID, string, time.Time) (entitlement.Entitlement, error) {
	return f.detail, nil
}

func (f *fakeEntitlementReader) ListEventsPage(context.Context, entitlement.EventFilter, string) (entitlement.EventPage, error) {
	f.auditCalled = true
	return entitlement.EventPage{Items: []entitlement.Event{{ID: uuid.New(), TenantID: uuid.New(), CompanyID: uuid.New(), ActorID: uuid.New(), ModuleCode: entitlement.ModuleFuneral, Action: entitlement.ActionActivated, Origin: entitlement.OriginAdmin, At: time.Now().UTC()}}}, nil
}

func (f *fakeEntitlementReader) AssignmentByID(context.Context, uuid.UUID) (entitlement.Assignment, error) {
	return f.assignment, nil
}

func (f *fakeEntitlementReader) ActivateIdempotent(_ context.Context, actorID uuid.UUID, _ string, in entitlement.ActivateInput) (entitlement.Assignment, error) {
	f.grantCalled = true
	return entitlement.Assignment{ID: uuid.New(), TenantID: uuid.New(), CompanyID: in.CompanyID, ModuleCode: in.ModuleCode, Status: entitlement.StatusActive, ValidFrom: time.Now().UTC(), Version: 1, UpdatedAt: time.Now().UTC()}, nil
}

func (f *fakeEntitlementReader) RevokeIdempotent(_ context.Context, _ uuid.UUID, _ string, in entitlement.TransitionInput) (entitlement.Assignment, error) {
	f.revokeCalled = true
	return entitlement.Assignment{ID: uuid.New(), TenantID: uuid.New(), CompanyID: in.CompanyID, ModuleCode: in.ModuleCode, Status: entitlement.StatusRevoked, ValidFrom: time.Now().UTC(), Version: 2, UpdatedAt: time.Now().UTC()}, nil
}

func (f *fakeEntitlementReader) DeactivateIdempotent(_ context.Context, _ uuid.UUID, _ string, in entitlement.TransitionInput) (entitlement.Assignment, error) {
	return entitlement.Assignment{ID: uuid.New(), TenantID: uuid.New(), CompanyID: in.CompanyID, ModuleCode: in.ModuleCode, Status: entitlement.StatusInactive, ValidFrom: time.Now().UTC(), Version: 2, UpdatedAt: time.Now().UTC()}, nil
}

func (f *fakeEntitlementReader) SetModuleQuotaIdempotent(context.Context, uuid.UUID, string, string, int, int64) (entitlement.ModuleQuota, error) {
	f.quotaCalled = true
	return entitlement.ModuleQuota{ModuleCode: entitlement.ModuleFuneral, Quota: 2, Configured: true, Version: 2}, nil
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

func TestCP06GrantRequiresPersistedIdempotencyKey(t *testing.T) {
	tenantID, companyID, userID := uuid.New(), uuid.New(), uuid.New()
	identityReader := &fakeIdentityReader{allowed: true}
	entitlementReader := &fakeEntitlementReader{}
	handler := NewHTTPHandler(identityReader, entitlementReader)
	principal := platformauth.Principal{Subject: "subject", UserID: userID, TenantID: tenantID, CompanyID: companyID, MFAAuthenticated: true}
	path := "/v1/companies/" + companyID.String() + "/entitlements/" + entitlement.ModuleFuneral

	req := authenticatedRequest(http.MethodPut, path, principal, tenantID)
	req.SetPathValue("companyId", companyID.String())
	req.SetPathValue("module", entitlement.ModuleFuneral)
	req.Body = http.NoBody
	rec := httptest.NewRecorder()
	handler.GrantEntitlement(rec, req)
	if rec.Code != http.StatusBadRequest || entitlementReader.grantCalled {
		t.Fatalf("sem chave: status=%d chamado=%v body=%s", rec.Code, entitlementReader.grantCalled, rec.Body.String())
	}

	req = authenticatedRequest(http.MethodPut, path, principal, tenantID)
	req.SetPathValue("companyId", companyID.String())
	req.SetPathValue("module", entitlement.ModuleFuneral)
	req.Header.Set("Idempotency-Key", "cp06-grant-1")
	req.Body = io.NopCloser(strings.NewReader(`{"validFrom":"2026-12-01"}`))
	rec = httptest.NewRecorder()
	handler.GrantEntitlement(rec, req)
	if rec.Code != http.StatusOK || !entitlementReader.grantCalled {
		t.Fatalf("com chave: status=%d chamado=%v body=%s", rec.Code, entitlementReader.grantCalled, rec.Body.String())
	}
}

func TestCP06PatchRequiresVersionAndAuditIsPaginated(t *testing.T) {
	tenantID, companyID, userID := uuid.New(), uuid.New(), uuid.New()
	identityReader := &fakeIdentityReader{allowed: true}
	entitlementReader := &fakeEntitlementReader{}
	handler := NewHTTPHandler(identityReader, entitlementReader)
	principal := platformauth.Principal{Subject: "subject", UserID: userID, TenantID: tenantID, CompanyID: companyID, MFAAuthenticated: true}
	path := "/v1/companies/" + companyID.String() + "/entitlements/" + entitlement.ModuleFuneral
	req := authenticatedRequest(http.MethodPatch, path, principal, tenantID)
	req.SetPathValue("companyId", companyID.String())
	req.SetPathValue("module", entitlement.ModuleFuneral)
	req.Header.Set("Idempotency-Key", "cp06-patch-1")
	req.Body = io.NopCloser(strings.NewReader(`{"status":"revoked"}`))
	rec := httptest.NewRecorder()
	handler.PatchEntitlement(rec, req)
	if rec.Code != http.StatusBadRequest || entitlementReader.revokeCalled {
		t.Fatalf("sem versão: status=%d chamado=%v body=%s", rec.Code, entitlementReader.revokeCalled, rec.Body.String())
	}

	auditPath := path + "/audit"
	req = authenticatedRequest(http.MethodGet, auditPath, principal, tenantID)
	req.SetPathValue("companyId", companyID.String())
	req.SetPathValue("module", entitlement.ModuleFuneral)
	rec = httptest.NewRecorder()
	handler.EntitlementAudit(rec, req)
	if rec.Code != http.StatusOK || !entitlementReader.auditCalled || !strings.Contains(rec.Body.String(), `"nextCursor":null`) {
		t.Fatalf("auditoria: status=%d chamado=%v body=%s", rec.Code, entitlementReader.auditCalled, rec.Body.String())
	}
}
