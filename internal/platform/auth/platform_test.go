package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func platformService(t *testing.T, identity PlatformIdentity, verifyErr error) *PlatformService {
	t.Helper()
	policy, err := DefaultPlatformRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPlatformService(PlatformVerifierFunc(func(context.Context, string) (PlatformIdentity, error) {
		return identity, verifyErr
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func validPlatformIdentity() PlatformIdentity {
	return PlatformIdentity{Issuer: "https://manager.example", Subject: "adm", UserID: uuid.New(), Roles: []string{RolePlatformAdmin}, MFA: true}
}

func TestPlatformServiceRequiresBearerMFAAndIdentity(t *testing.T) {
	ctx := context.Background()

	if _, err := platformService(t, validPlatformIdentity(), nil).Authenticate(ctx, ""); !errors.Is(err, ErrInvalidBearer) {
		t.Fatalf("sem bearer: %v", err)
	}
	noMFA := validPlatformIdentity()
	noMFA.MFA = false
	if _, err := platformService(t, noMFA, nil).Authenticate(ctx, "Bearer x"); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("sem MFA: %v", err)
	}
	noUser := validPlatformIdentity()
	noUser.UserID = uuid.Nil
	if _, err := platformService(t, noUser, nil).Authenticate(ctx, "Bearer x"); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("sem usuário: %v", err)
	}
	verifyErr := ErrInvalidOIDCToken
	if _, err := platformService(t, PlatformIdentity{}, verifyErr).Authenticate(ctx, "Bearer x"); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("token inválido: %v", err)
	}
	principal, err := platformService(t, validPlatformIdentity(), nil).Authenticate(ctx, "Bearer x")
	if err != nil || principal.UserID == uuid.Nil {
		t.Fatalf("platform_admin com MFA deveria autenticar: %v", err)
	}
}

func TestPlatformPolicyIsDisjointFromTenantRoles(t *testing.T) {
	service := platformService(t, validPlatformIdentity(), nil)
	admin := PlatformPrincipal{Roles: []string{RolePlatformAdmin}}
	if !service.Allows(admin, PermissionPlatformRead) || !service.Allows(admin, PermissionPlatformWrite) {
		t.Fatal("platform_admin deve ler e escrever")
	}
	if service.Allows(admin, Permission("finance:read")) {
		t.Fatal("platform_admin não deve receber permissões de tenant")
	}
	for _, role := range []string{"tenant_owner", "owner", "admin", "finance", "operator"} {
		if service.Allows(PlatformPrincipal{Roles: []string{role}}, PermissionPlatformRead) {
			t.Fatalf("papel de tenant %q não pode acessar plataforma", role)
		}
	}
	if _, err := NewPlatformService(nil, RolePolicy{}); err == nil {
		t.Fatal("serviço sem verificador deve falhar")
	}
}

func TestRequirePlatformStatuses(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(discard{}, nil))
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if _, err := PlatformPrincipalFromContext(r.Context()); err != nil {
			t.Fatalf("principal ausente no contexto: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	do := func(gate PlatformGate, permission Permission, authorization string) *httptest.ResponseRecorder {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/admin/v1/tenants", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		RequirePlatform(gate, permission, logger)(next).ServeHTTP(rec, req)
		return rec
	}

	if rec := do(nil, PermissionPlatformRead, "Bearer x"); rec.Code != http.StatusServiceUnavailable || reached {
		t.Fatalf("gate nil: status=%d", rec.Code)
	}
	good := platformService(t, validPlatformIdentity(), nil)
	if rec := do(good, "", "Bearer x"); rec.Code != http.StatusForbidden || reached {
		t.Fatalf("permissão vazia: status=%d", rec.Code)
	}
	if rec := do(good, PermissionPlatformRead, ""); rec.Code != http.StatusUnauthorized || reached {
		t.Fatalf("sem bearer: status=%d", rec.Code)
	}
	noMFA := validPlatformIdentity()
	noMFA.MFA = false
	if rec := do(platformService(t, noMFA, nil), PermissionPlatformRead, "Bearer x"); rec.Code != http.StatusForbidden || reached {
		t.Fatalf("sem MFA: status=%d", rec.Code)
	}
	tenantRole := validPlatformIdentity()
	tenantRole.Roles = []string{"tenant_owner"}
	if rec := do(platformService(t, tenantRole, nil), PermissionPlatformRead, "Bearer x"); rec.Code != http.StatusForbidden || reached {
		t.Fatalf("papel de tenant: status=%d", rec.Code)
	}
	if rec := do(good, PermissionPlatformWrite, "Bearer x"); rec.Code != http.StatusNoContent || !reached {
		t.Fatalf("platform_admin: status=%d", rec.Code)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
