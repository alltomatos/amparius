package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/readiness"
	"github.com/google/uuid"
)

type serverGate struct {
	principal auth.Principal
}

func (g serverGate) Authenticate(_ context.Context, value string) (auth.Principal, error) {
	if value != "Bearer server-test-token" {
		return auth.Principal{}, auth.ErrInvalidBearer
	}
	return g.principal, nil
}

func (serverGate) Allows(auth.Principal, auth.Permission) bool { return true }

func newTestServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return New(logger)
}

type testReadinessDependency struct {
	name string
	err  error
}

func (d testReadinessDependency) Name() string                { return d.name }
func (d testReadinessDependency) Check(context.Context) error { return d.err }

func TestHealthzResponde200(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer %d", rec.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo não é JSON válido: %v (%s)", err, rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Fatalf("status do corpo = %q, quer %q", body["status"], "ok")
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestReadyzResponde200(t *testing.T) {
	srv := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), Options{
		Readiness: readiness.New([]readiness.Dependency{
			testReadinessDependency{name: "postgres"},
		}, time.Second),
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer %d", rec.Code, http.StatusOK)
	}
}

func TestReadyzResponde503QuandoDependenciaFalha(t *testing.T) {
	srv := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), Options{
		Readiness: readiness.New([]readiness.Dependency{
			testReadinessDependency{name: "redis", err: errors.New("falha simulada")},
		}, time.Second),
	})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quer %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), `"status":"not_ready"`) {
		t.Fatalf("corpo sem status not_ready: %s", rec.Body.String())
	}
}

func TestHealthzRejeitaMetodoNaoPermitido(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, quer %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestRotaDesconhecidaResponde404(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/nao-existe", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quer %d", rec.Code, http.StatusNotFound)
	}
}

func TestRequestIDGeradoQuandoAusente(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	id := rec.Header().Get("X-Request-Id")
	if len(id) < 8 {
		t.Fatalf("X-Request-Id ausente ou curto: %q", id)
	}
}

func TestRequestIDRecebidoEPreservado(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "abc123-def456")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != "abc123-def456" {
		t.Fatalf("X-Request-Id = %q, quer %q", got, "abc123-def456")
	}
}

func TestTraceIDGeradoEPropagadoSemPII(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Trace-Id"); got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("trace-id propagado = %q", got)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := rec.Header().Get("X-Trace-Id"); len(got) != 32 || got == strings.Repeat("0", 32) {
		t.Fatalf("trace-id gerado inválido = %q", got)
	}
}

func TestRecovererDevolve500(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("falha simulada")
	})
	handler := chain(panicking, requestID, accessLog(logger), recoverer(logger))

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, quer %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(logs.String(), "falha simulada") || strings.Contains(logs.String(), `"path"`) {
		t.Fatalf("panic ou caminho bruto entrou no log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"panic_type":"string"`) {
		t.Fatalf("tipo do panic ausente no log: %s", logs.String())
	}
}

func TestAuthenticatedRouteRequiresGateAndComposesScope(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	srv := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), Options{
		Authenticator: serverGate{principal: auth.Principal{
			Issuer: "https://issuer.test", Subject: "subject-1", TenantID: tenantID, CompanyID: companyID, MFAAuthenticated: true,
		}},
		AuthenticatedRoutes: []AuthenticatedRoute{{
			Pattern:    "GET /api/v1/context",
			Permission: auth.PermissionIdentityRead,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got, err := db.TenantFromContext(r.Context()); err != nil || got != tenantID {
					t.Errorf("tenant = %s/%v", got, err)
				}
				if got, err := db.CompanyFromContext(r.Context()); err != nil || got != companyID {
					t.Errorf("company = %s/%v", got, err)
				}
				w.WriteHeader(http.StatusNoContent)
			}),
		}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/context", nil)
	req.Header.Set("Authorization", "Bearer server-test-token")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status autenticado = %d, esperado 204", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/context", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status sem credencial = %d, esperado 401", rec.Code)
	}

	// A ausência de uma implementação OIDC não abre a rota: o registro falha fechado.
	srv = New(slog.New(slog.NewJSONHandler(io.Discard, nil)), Options{
		AuthenticatedRoutes: []AuthenticatedRoute{{
			Pattern: "GET /api/v1/unconfigured", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("rota sem autenticação não deveria alcançar o handler")
			}), Permission: auth.PermissionIdentityRead,
		}},
	})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/unconfigured", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status sem gate = %d, esperado 503", rec.Code)
	}
}
