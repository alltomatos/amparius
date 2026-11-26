package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/config"
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

// TestBuildHandlerSobreviveCelulaIndisponivelSemDevESemControlPlane prova a
// correção da regressão da #72: com dev e control plane desabilitados,
// buildHandler (o mesmo caminho usado por run(), exercido aqui pelo entry
// point normal do cmd/api, sem porta real nem sinal de SO) não pode falhar
// o bootstrap só porque o PostgreSQL da célula está inacessível — esse era
// o comportamento antes da #72 introduzir /v1/parties, e tem que continuar
// valendo. /healthz sobe 200, /readyz cai 503 (checker lento, no request) e
// /v1/parties cai 503 fail-closed porque o Authenticator continua nil
// (ADR 0038 pendente), não porque o repositório de party tenha falhado.
func TestBuildHandlerSobreviveCelulaIndisponivelSemDevESemControlPlane(t *testing.T) {
	cfg := config.Config{
		Env:                 "production",
		EnableDevOnboarding: false,
		ControlPlaneDSN:     "",
		PostgresDSN:         "postgres://amparius:amparius@127.0.0.1:1/amparius?sslmode=disable&connect_timeout=1",
		RedisAddr:           "127.0.0.1:1",
		RabbitMQURL:         "amqp://amparius:amparius@127.0.0.1:1/",
		ReadinessTimeout:    500 * time.Millisecond,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	handler, closeHandler, err := buildHandler(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("buildHandler falhou com PostgreSQL da célula inacessível: %v", err)
	}
	defer closeHandler()

	healthRecord := httptest.NewRecorder()
	handler.ServeHTTP(healthRecord, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if healthRecord.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, esperado 200 mesmo com a célula inacessível", healthRecord.Code)
	}

	readyRecord := httptest.NewRecorder()
	handler.ServeHTTP(readyRecord, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyRecord.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d, esperado 503 com a célula inacessível", readyRecord.Code)
	}

	partiesRecord := httptest.NewRecorder()
	handler.ServeHTTP(partiesRecord, httptest.NewRequest(http.MethodGet, "/v1/parties?q=ana", nil))
	if partiesRecord.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /v1/parties = %d, esperado 503 fail-closed sem autenticador", partiesRecord.Code)
	}
	if !strings.Contains(partiesRecord.Body.String(), `"code":"AUTHENTICATION_UNAVAILABLE"`) {
		t.Fatalf("GET /v1/parties sem AUTHENTICATION_UNAVAILABLE: %s", partiesRecord.Body.String())
	}

	postRecord := httptest.NewRecorder()
	handler.ServeHTTP(postRecord, httptest.NewRequest(http.MethodPost, "/v1/parties", strings.NewReader(`{"name":"Ana"}`)))
	if postRecord.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /v1/parties = %d, esperado 503 fail-closed sem autenticador", postRecord.Code)
	}
	if !strings.Contains(postRecord.Body.String(), `"code":"AUTHENTICATION_UNAVAILABLE"`) {
		t.Fatalf("POST /v1/parties sem AUTHENTICATION_UNAVAILABLE: %s", postRecord.Body.String())
	}

	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/v1/deceased"},
		{http.MethodGet, "/v1/deceased/id"},
		{http.MethodPost, "/v1/deceased/id/family-links"},
		{http.MethodGet, "/v1/family-links/id"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"AUTHENTICATION_UNAVAILABLE"`) {
			t.Fatalf("%s %s = %d %s, esperado 503 AUTHENTICATION_UNAVAILABLE", route.method, route.path, rec.Code, rec.Body)
		}
	}
}
