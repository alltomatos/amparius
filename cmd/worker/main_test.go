package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/observability"
)

func TestSelectHandlerUsesRealDeliveryByDefault(t *testing.T) {
	if _, ok := selectHandler("delivery").(outbox.DeliveryHandler); !ok {
		t.Fatal("handler padrao deveria ser DeliveryHandler")
	}
	if _, ok := selectHandler("refuse").(outbox.RefusingHandler); !ok {
		t.Fatal("handler refuse deveria ser RefusingHandler")
	}
	// Um nome desconhecido nunca cai em noop silencioso: a config ja barra,
	// e o fallback conservador e a recusa explicita.
	if _, ok := selectHandler("inesperado").(outbox.DeliveryHandler); ok {
		t.Fatal("nome desconhecido nao deveria entregar")
	}
}

func TestMetricsHandlerExposesPrometheusAndHealth(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.AddCounter("amparius_outbox_cycles_total", nil, 2)
	handler := metricsHandler(metrics)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status /metrics = %d", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("content-type inesperado: %q", contentType)
	}
	if !strings.Contains(rec.Body.String(), "amparius_outbox_cycles_total 2") {
		t.Fatalf("metrica ausente:\n%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("healthz inesperado: %d %s", rec.Code, rec.Body.String())
	}
}
