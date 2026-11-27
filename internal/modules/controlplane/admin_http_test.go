package controlplane

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
)

type fakeReports struct {
	err       error
	tenantErr error
	called    int
	tenantID  uuid.UUID
	cursor    string
}

func (f *fakeReports) ListTenants(_ context.Context, cursor string, _ int) (TenantSummaryPage, error) {
	f.called++
	f.cursor = cursor
	if f.err != nil {
		return TenantSummaryPage{}, f.err
	}
	seats := 5
	return TenantSummaryPage{Items: []TenantSummary{{TenantID: uuid.New(), ModulesWithQuota: 2, QuotaTotal: 4, UserSeats: &seats}}, NextCursor: "next"}, nil
}

func (f *fakeReports) GetTenant(_ context.Context, id uuid.UUID) (TenantSummary, error) {
	f.called++
	f.tenantID = id
	if f.tenantErr != nil {
		return TenantSummary{}, f.tenantErr
	}
	return TenantSummary{TenantID: id}, nil
}

func (f *fakeReports) ListLicensePools(_ context.Context, id uuid.UUID) ([]LicensePool, error) {
	f.called++
	return []LicensePool{{TenantID: id, ModuleCode: entitlement.ModuleFuneral, Quota: 3, InUse: 1, Version: 2, UpdatedAt: time.Now()}}, nil
}

func (f *fakeReports) ListAssignments(_ context.Context, id uuid.UUID, _ string, _ int) (PlatformAssignmentPage, error) {
	f.called++
	return PlatformAssignmentPage{Items: []PlatformAssignment{{
		ID: uuid.New(), TenantID: id, CompanyID: uuid.New(), ModuleCode: entitlement.ModuleFuneral,
		Status: "active", ValidFrom: time.Now(), Version: 1, UpdatedAt: time.Now(),
	}}}, nil
}

func (f *fakeReports) ListEvents(_ context.Context, id uuid.UUID, _ string, _ int) (PlatformEventPage, error) {
	f.called++
	return PlatformEventPage{Items: []PlatformEvent{{
		ID: uuid.New(), TenantID: id, CompanyID: uuid.New(), ActorID: uuid.New(), ModuleCode: entitlement.ModuleFuneral,
		Action: "activated", Origin: "admin", At: time.Now(),
	}}}, nil
}

type fakeAdminWriter struct {
	tenantID uuid.UUID
	actor    uuid.UUID
	key      string
	activate entitlement.ActivateInput
	revoke   entitlement.TransitionInput
	quota    int
	version  int64
	err      error
	calls    int
}

func (f *fakeAdminWriter) ActivateIdempotent(ctx context.Context, actor uuid.UUID, key string, in entitlement.ActivateInput) (entitlement.Assignment, error) {
	f.calls++
	f.tenantID, _ = db.TenantFromContext(ctx)
	f.actor, f.key, f.activate = actor, key, in
	if f.err != nil {
		return entitlement.Assignment{}, f.err
	}
	return entitlement.Assignment{ID: uuid.New(), TenantID: f.tenantID, CompanyID: in.CompanyID, ModuleCode: in.ModuleCode,
		Status: entitlement.StatusActive, ValidFrom: time.Now(), Version: 3, UpdatedAt: time.Now()}, nil
}

func (f *fakeAdminWriter) RevokeIdempotent(ctx context.Context, actor uuid.UUID, key string, in entitlement.TransitionInput) (entitlement.Assignment, error) {
	f.calls++
	f.tenantID, _ = db.TenantFromContext(ctx)
	f.actor, f.key, f.revoke = actor, key, in
	if f.err != nil {
		return entitlement.Assignment{}, f.err
	}
	return entitlement.Assignment{ID: uuid.New(), TenantID: f.tenantID, CompanyID: in.CompanyID, ModuleCode: in.ModuleCode,
		Status: entitlement.StatusRevoked, ValidFrom: time.Now(), Version: in.ExpectedVersion + 1, UpdatedAt: time.Now()}, nil
}

func (f *fakeAdminWriter) DeactivateIdempotent(context.Context, uuid.UUID, string, entitlement.TransitionInput) (entitlement.Assignment, error) {
	return entitlement.Assignment{}, errors.New("não usado")
}

func (f *fakeAdminWriter) SetModuleQuotaIdempotent(ctx context.Context, actor uuid.UUID, key, module string, quota int, version int64) (entitlement.ModuleQuota, error) {
	f.calls++
	f.tenantID, _ = db.TenantFromContext(ctx)
	f.actor, f.key, f.quota, f.version = actor, key, quota, version
	if f.err != nil {
		return entitlement.ModuleQuota{}, f.err
	}
	return entitlement.ModuleQuota{ModuleCode: module, Quota: quota, Configured: true, Version: version + 1}, nil
}

type fakePlatformGate struct {
	principal platformauth.PlatformPrincipal
	err       error
	allow     bool
}

func (g fakePlatformGate) Authenticate(context.Context, string) (platformauth.PlatformPrincipal, error) {
	return g.principal, g.err
}

func (g fakePlatformGate) Allows(platformauth.PlatformPrincipal, platformauth.Permission) bool {
	return g.allow
}

type adminFixture struct {
	reports *fakeReports
	writer  *fakeAdminWriter
	logs    *bytes.Buffer
	server  http.Handler
	actor   uuid.UUID
}

func newAdminFixture(t *testing.T, gate platformauth.PlatformGate) adminFixture {
	t.Helper()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	reports, writer := &fakeReports{}, &fakeAdminWriter{}
	handler := NewAdminHandler(reports, writer, logger)
	read, write := platformauth.PermissionPlatformRead, platformauth.PermissionPlatformWrite
	srv := httpserver.New(logger, httpserver.Options{
		PlatformAuthenticator: gate,
		PlatformRoutes: []httpserver.PlatformRoute{
			{Pattern: "GET /admin/v1/tenants", Permission: read, Handler: handler.Audited("tenants.list", handler.Tenants)},
			{Pattern: "GET /admin/v1/tenants/{tenantId}/licenses", Permission: read, Handler: handler.Audited("licenses.read", handler.Licenses)},
			{Pattern: "GET /admin/v1/tenants/{tenantId}/entitlement-events", Permission: read, Handler: handler.Audited("events.read", handler.Events)},
			{Pattern: "PUT /admin/v1/tenants/{tenantId}/companies/{companyId}/entitlements/{module}", Permission: write, Handler: handler.Audited("entitlement.grant", handler.Grant)},
			{Pattern: "POST /admin/v1/tenants/{tenantId}/companies/{companyId}/entitlements/{module}/revoke", Permission: write, Handler: handler.Audited("entitlement.revoke", handler.Revoke)},
			{Pattern: "PUT /admin/v1/tenants/{tenantId}/modules/{module}/quota", Permission: write, Handler: handler.Audited("quota.set", handler.SetQuota)},
		},
	})
	actor := uuid.New()
	return adminFixture{reports: reports, writer: writer, logs: logs, server: srv.Handler(), actor: actor}
}

func adminGate(actor uuid.UUID) fakePlatformGate {
	return fakePlatformGate{
		principal: platformauth.PlatformPrincipal{Issuer: "https://manager.example", Subject: "sub", UserID: actor, Roles: []string{platformauth.RolePlatformAdmin}},
		allow:     true,
	}
}

func (f adminFixture) do(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec
}

func TestAdminRoutesFailClosedWithoutPlatformGate(t *testing.T) {
	f := newAdminFixture(t, nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/admin/v1/tenants"},
		{http.MethodGet, "/admin/v1/tenants/" + uuid.NewString() + "/licenses"},
		{http.MethodPut, "/admin/v1/tenants/" + uuid.NewString() + "/modules/funeral/quota"},
	} {
		rec := f.do(route.method, route.path, `{}`, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status=%d, esperado 503", route.method, route.path, rec.Code)
		}
	}
	if f.reports.called != 0 || f.writer.calls != 0 {
		t.Fatalf("nada deveria ser chamado sem gate: leituras=%d escritas=%d", f.reports.called, f.writer.calls)
	}
}

func TestAdminRoutesRejectUnauthenticatedAndForbidden(t *testing.T) {
	for name, tc := range map[string]struct {
		gate   fakePlatformGate
		status int
	}{
		"sem token":       {fakePlatformGate{err: platformauth.ErrInvalidBearer}, http.StatusUnauthorized},
		"mfa ausente":     {fakePlatformGate{err: platformauth.ErrMFARequired}, http.StatusForbidden},
		"papel de tenant": {fakePlatformGate{principal: platformauth.PlatformPrincipal{Subject: "s", UserID: uuid.New(), Roles: []string{"tenant_owner"}}, allow: false}, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAdminFixture(t, tc.gate)
			rec := f.do(http.MethodGet, "/admin/v1/tenants", "", nil)
			if rec.Code != tc.status {
				t.Fatalf("status=%d, esperado %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if f.reports.called != 0 {
				t.Fatalf("leitura cross-tenant executada sem autorização")
			}
		})
	}
}

func TestAdminReadsAreAuditedAndPaginated(t *testing.T) {
	actor := uuid.New()
	f := newAdminFixture(t, adminGate(actor))
	tenantID := uuid.New()

	rec := f.do(http.MethodGet, "/admin/v1/tenants?limit=10&cursor=abc", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"scope":"platform"`) || !strings.Contains(rec.Body.String(), `"nextCursor":"next"`) {
		t.Fatalf("tenants: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if f.reports.cursor != "abc" {
		t.Fatalf("cursor não repassado: %q", f.reports.cursor)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("resposta de plataforma deve ser no-store")
	}

	rec = f.do(http.MethodGet, "/admin/v1/tenants/"+tenantID.String()+"/licenses", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pools"`) || !strings.Contains(rec.Body.String(), `"assignments"`) {
		t.Fatalf("licenses: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = f.do(http.MethodGet, "/admin/v1/tenants/"+tenantID.String()+"/entitlement-events", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"origin":"admin"`) {
		t.Fatalf("events: status=%d body=%s", rec.Code, rec.Body.String())
	}
	logs := f.logs.String()
	for _, want := range []string{`"event":"platform_audit"`, `"action":"tenants.list"`, `"action":"licenses.read"`,
		`"target_tenant_id":"` + tenantID.String() + `"`, `"actor_id":"` + actor.String() + `"`, `"result":"ok"`} {
		if !strings.Contains(logs, want) {
			t.Fatalf("auditoria sem %s:\n%s", want, logs)
		}
	}
}

func TestAdminReadFailuresAreStructured(t *testing.T) {
	f := newAdminFixture(t, adminGate(uuid.New()))
	tenantID := uuid.NewString()

	f.reports.tenantErr = ErrPlatformTenantNotFound
	rec := f.do(http.MethodGet, "/admin/v1/tenants/"+tenantID+"/licenses", "", nil)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "TENANT_NOT_FOUND") {
		t.Fatalf("404: status=%d body=%s", rec.Code, rec.Body.String())
	}
	f.reports.err = ErrPlatformReportUnavailable
	rec = f.do(http.MethodGet, "/admin/v1/tenants", "", nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "PLATFORM_REPORT_UNAVAILABLE") {
		t.Fatalf("503: status=%d body=%s", rec.Code, rec.Body.String())
	}
	f.reports.err = ErrPlatformInvalidCursor
	rec = f.do(http.MethodGet, "/admin/v1/tenants?cursor=%%", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cursor inválido: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = f.do(http.MethodGet, "/admin/v1/tenants/not-a-uuid/licenses", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tenantId inválido: status=%d", rec.Code)
	}
	if !strings.Contains(f.logs.String(), `"result":"error"`) {
		t.Fatalf("recusas também devem ser auditadas:\n%s", f.logs.String())
	}
}

func TestAdminWritesUseExplicitTenantActorAndAdminOrigin(t *testing.T) {
	actor := uuid.New()
	f := newAdminFixture(t, adminGate(actor))
	tenantID, companyID := uuid.New(), uuid.New()
	base := "/admin/v1/tenants/" + tenantID.String()
	key := map[string]string{"Idempotency-Key": "adm-1"}

	rec := f.do(http.MethodPut, base+"/companies/"+companyID.String()+"/entitlements/funeral", `{"validFrom":"2026-12-01","version":2}`, key)
	if rec.Code != http.StatusOK {
		t.Fatalf("grant: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if f.writer.tenantID != tenantID || f.writer.actor != actor || f.writer.key != "adm-1" ||
		f.writer.activate.Origin != entitlement.OriginAdmin || f.writer.activate.ExpectedVersion != 2 || f.writer.activate.CompanyID != companyID {
		t.Fatalf("grant sem tenant/ator/origem corretos: %+v tenant=%s actor=%s", f.writer.activate, f.writer.tenantID, f.writer.actor)
	}

	rec = f.do(http.MethodPost, base+"/companies/"+companyID.String()+"/entitlements/funeral/revoke", `{"version":3}`, key)
	if rec.Code != http.StatusOK || f.writer.revoke.ExpectedVersion != 3 || f.writer.revoke.Origin != entitlement.OriginAdmin {
		t.Fatalf("revoke: status=%d body=%s input=%+v", rec.Code, rec.Body.String(), f.writer.revoke)
	}

	rec = f.do(http.MethodPut, base+"/modules/funeral/quota", `{"quota":4,"version":1}`, key)
	if rec.Code != http.StatusOK || f.writer.quota != 4 || f.writer.version != 1 || f.writer.tenantID != tenantID {
		t.Fatalf("quota: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(f.logs.String(), `"action":"entitlement.revoke"`) {
		t.Fatalf("escrita sem auditoria:\n%s", f.logs.String())
	}
}

func TestAdminWritesValidateKeyVersionAndMapConflicts(t *testing.T) {
	f := newAdminFixture(t, adminGate(uuid.New()))
	base := "/admin/v1/tenants/" + uuid.NewString()
	company := "/companies/" + uuid.NewString() + "/entitlements/funeral"

	rec := f.do(http.MethodPost, base+company+"/revoke", `{"version":1}`, nil)
	if rec.Code != http.StatusBadRequest || f.writer.calls != 0 {
		t.Fatalf("sem Idempotency-Key: status=%d chamadas=%d", rec.Code, f.writer.calls)
	}
	key := map[string]string{"Idempotency-Key": "adm-2"}
	rec = f.do(http.MethodPost, base+company+"/revoke", `{}`, key)
	if rec.Code != http.StatusBadRequest || f.writer.calls != 0 {
		t.Fatalf("revoke sem version: status=%d chamadas=%d", rec.Code, f.writer.calls)
	}
	rec = f.do(http.MethodPut, base+"/modules/funeral/quota", `{"quota":-1,"version":0}`, key)
	if rec.Code != http.StatusBadRequest || f.writer.calls != 0 {
		t.Fatalf("quota negativa: status=%d chamadas=%d", rec.Code, f.writer.calls)
	}
	rec = f.do(http.MethodPut, base+company, `{"validFrom":"2026-13-45"}`, key)
	if rec.Code != http.StatusBadRequest || f.writer.calls != 0 {
		t.Fatalf("data inválida: status=%d chamadas=%d", rec.Code, f.writer.calls)
	}

	f.writer.err = entitlement.ErrStaleVersion
	rec = f.do(http.MethodPost, base+company+"/revoke", `{"version":1}`, key)
	if rec.Code != http.StatusConflict && rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("versão obsoleta: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
