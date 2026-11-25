package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

type companyResolver struct {
	belongs bool
	err     error
	tenant  uuid.UUID
	company uuid.UUID
}

type subjectResolver struct {
	record SubjectRecord
	err    error
}

func (r subjectResolver) ResolveSubject(context.Context, uuid.UUID, string) (SubjectRecord, error) {
	return r.record, r.err
}

func (r *companyResolver) CompanyBelongsToTenant(_ context.Context, tenantID, companyID uuid.UUID) (bool, error) {
	r.tenant, r.company = tenantID, companyID
	return r.belongs, r.err
}

func testService(t *testing.T, role string, mfa bool, belongs bool) (*Service, *companyResolver, uuid.UUID, uuid.UUID) {
	t.Helper()
	tenantID, companyID := uuid.New(), uuid.New()
	resolver := &companyResolver{belongs: belongs}
	policy, err := DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		OIDCVerifierFunc(func(_ context.Context, token string) (OIDCIdentity, error) {
			if token != "opaque-test-token" {
				return OIDCIdentity{}, errors.New("token rejeitado")
			}
			return OIDCIdentity{Issuer: "https://issuer.test", Subject: "subject-1", TenantID: tenantID, CompanyID: companyID}, nil
		}),
		MFAVerifierFunc(func(context.Context, OIDCIdentity) (bool, error) { return mfa, nil }),
		subjectResolver{record: SubjectRecord{Roles: []string{role}, CompanyIDs: []uuid.UUID{companyID}, MFAEnabled: mfa}},
		resolver, policy, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, resolver, tenantID, companyID
}

func TestRequireComposesAuthenticatedTenantCompanyAndPrincipal(t *testing.T) {
	service, resolver, tenantID, companyID := testService(t, "operator", true, true)
	var got Principal
	handler := Require(service, PermissionPartyWrite, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		got, err = PrincipalFromContext(r.Context())
		if err != nil {
			t.Errorf("principal ausente: %v", err)
		}
		if value, err := db.TenantFromContext(r.Context()); err != nil || value != tenantID {
			t.Errorf("tenant = %s/%v", value, err)
		}
		if value, err := db.CompanyFromContext(r.Context()); err != nil || value != companyID {
			t.Errorf("company = %s/%v", value, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/parties", nil)
	req.Header.Set("Authorization", "Bearer opaque-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, esperado 204", rec.Code)
	}
	if got.Subject != "subject-1" || !got.MFAAuthenticated {
		t.Fatalf("principal = %#v", got)
	}
	if resolver.tenant != tenantID || resolver.company != companyID {
		t.Fatalf("resolver recebeu escopo incorreto: %s/%s", resolver.tenant, resolver.company)
	}
}

func TestRequireRejectsMissingOrMalformedBearer(t *testing.T) {
	service, _, _, _ := testService(t, "operator", true, true)
	for _, authorization := range []string{"", "Basic secret", "Bearer", "Bearer a b"} {
		t.Run(authorization, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/parties", nil)
			req.Header.Set("Authorization", authorization)
			rec := httptest.NewRecorder()
			Require(service, PermissionPartyRead, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("rota protegida alcançada sem bearer válido")
			})).ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, esperado 401", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("WWW-Authenticate ausente")
			}
		})
	}
}

func TestRequireRejectsMFAAndForeignCompany(t *testing.T) {
	for name, tc := range map[string]struct {
		mfa     bool
		belongs bool
		code    string
	}{
		"mfa obrigatório":        {mfa: false, belongs: true, code: "MFA_REQUIRED"},
		"empresa fora do tenant": {mfa: true, belongs: false, code: "SCOPE_FORBIDDEN"},
	} {
		t.Run(name, func(t *testing.T) {
			service, _, _, _ := testService(t, "operator", tc.mfa, tc.belongs)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/parties", nil)
			req.Header.Set("Authorization", "Bearer opaque-test-token")
			rec := httptest.NewRecorder()
			Require(service, PermissionPartyRead, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("rota alcançada com autenticação inválida")
			})).ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("resposta = %d/%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRequireRejectsPermissionAndContextConflicts(t *testing.T) {
	service, _, tenantID, _ := testService(t, "auditor", true, true)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/finance/payments", nil)
	req.Header.Set("Authorization", "Bearer opaque-test-token")
	rec := httptest.NewRecorder()
	Require(service, PermissionFinanceWrite, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("auditor não deveria escrever financeiro")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "PERMISSION_DENIED") {
		t.Fatalf("RBAC = %d/%s", rec.Code, rec.Body.String())
	}

	service, _, _, _ = testService(t, "operator", true, true)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/parties", nil).WithContext(db.WithTenant(req.Context(), uuid.New()))
	req.Header.Set("Authorization", "Bearer opaque-test-token")
	rec = httptest.NewRecorder()
	Require(service, PermissionPartyRead, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("contexto conflitante não deveria alcançar a rota")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "SCOPE_FORBIDDEN") {
		t.Fatalf("conflito de tenant = %d/%s (tenant esperado %s)", rec.Code, rec.Body.String(), tenantID)
	}
}

func TestRequireDoesNotLogTokenOrSubject(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	service, _, _, _ := testService(t, "operator", true, true)
	// O token rejeitado contém valores que nunca podem aparecer em observabilidade.
	service.oidc = OIDCVerifierFunc(func(context.Context, string) (OIDCIdentity, error) {
		return OIDCIdentity{}, ErrInvalidOIDCToken
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/parties", nil)
	req.Header.Set("Authorization", "Bearer secret-email@example.com")
	rec := httptest.NewRecorder()
	Require(service, PermissionPartyRead, logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("rota não deveria alcançar o handler")
	})).ServeHTTP(rec, req)
	if strings.Contains(logs.String(), "secret-email@example.com") || strings.Contains(logs.String(), "rejeitado") {
		t.Fatalf("log vazou credencial/erro do provedor: %s", logs.String())
	}
}

func TestNewServiceFailsClosedWhenDependencyIsMissing(t *testing.T) {
	policy, err := DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(nil, MFAVerifierFunc(func(context.Context, OIDCIdentity) (bool, error) { return true, nil }), subjectResolver{}, &companyResolver{belongs: true}, policy, nil); !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("erro = %v, esperado ErrAuthenticationUnavailable", err)
	}
}
