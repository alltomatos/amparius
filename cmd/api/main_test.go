package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/platform/auth"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
)

func TestCP06SemBootstrapOIDCMFAFalhaFechado(t *testing.T) {
	called := false
	handler := httpserver.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), httpserver.Options{
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{{
			Pattern:    "GET /v1/context",
			Permission: auth.PermissionContextRead,
			Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}),
		}},
	}).Handler()

	record := httptest.NewRecorder()
	handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/v1/context", nil))

	if called {
		t.Fatal("rota CP-06 alcançada sem bootstrap OIDC/MFA")
	}
	if record.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, esperado 503", record.Code)
	}
	if !strings.Contains(record.Body.String(), `"code":"AUTHENTICATION_UNAVAILABLE"`) {
		t.Fatalf("código de erro ausente: %s", record.Body.String())
	}
}
