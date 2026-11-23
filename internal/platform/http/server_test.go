package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return New(logger)
}

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
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer %d", rec.Code, http.StatusOK)
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

func TestRecovererDevolve500(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
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
}
