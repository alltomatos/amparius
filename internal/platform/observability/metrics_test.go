package observability

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestCorrelationIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := CorrelationIDFromContext(ctx); got != "" {
		t.Fatalf("contexto vazio deveria devolver \"\", obtido %q", got)
	}
	ctx = WithCorrelationID(ctx, "req-abc-123")
	if got := CorrelationIDFromContext(ctx); got != "req-abc-123" {
		t.Fatalf("correlation id perdido: %q", got)
	}
	if got := CorrelationIDFromContext(nil); got != "" {
		t.Fatalf("contexto nulo deveria devolver \"\", obtido %q", got)
	}
}

func TestNewCorrelationIDIsUniqueAndOpaque(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := NewCorrelationID()
		if len(id) != 36 {
			t.Fatalf("id deveria ser UUID canônico, obtido %q", id)
		}
		if strings.ContainsAny(id, "@ ") {
			t.Fatalf("id deveria ser opaco: %q", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("id repetido: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestMetricsExpositionCounterGaugeAndHistogram(t *testing.T) {
	metrics := NewMetrics()
	metrics.AddCounter("amparius_outbox_events_claimed_total", nil, 3)
	metrics.AddCounter("amparius_outbox_events_claimed_total", nil, 2)
	metrics.SetGauge("amparius_outbox_backlog_events", Labels{"tenant_id": "t-1", "status": "pending"}, 7)
	metrics.Observe("amparius_outbox_delivery_duration_seconds", nil, 0.03)
	metrics.Observe("amparius_outbox_delivery_duration_seconds", nil, 0.4)

	var out strings.Builder
	if err := metrics.WritePrometheus(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"amparius_outbox_events_claimed_total 5",
		`amparius_outbox_backlog_events{status="pending",tenant_id="t-1"} 7`,
		`amparius_outbox_delivery_duration_seconds_bucket{le="0.05"} 1`,
		`amparius_outbox_delivery_duration_seconds_bucket{le="0.5"} 2`,
		`amparius_outbox_delivery_duration_seconds_bucket{le="+Inf"} 2`,
		"amparius_outbox_delivery_duration_seconds_count 2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("exposição sem %q:\n%s", want, text)
		}
	}
}

func TestMetricsReplaceGaugesDropsStaleTenants(t *testing.T) {
	metrics := NewMetrics()
	metrics.ReplaceGauges("amparius_outbox_backlog_events", []Sample{
		{Labels: Labels{"tenant_id": "t-1", "status": "pending"}, Value: 4},
		{Labels: Labels{"tenant_id": "t-2", "status": "pending"}, Value: 8},
	})
	metrics.ReplaceGauges("amparius_outbox_backlog_events", []Sample{
		{Labels: Labels{"tenant_id": "t-1", "status": "pending"}, Value: 1},
	})

	var out strings.Builder
	if err := metrics.WritePrometheus(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "t-2") {
		t.Fatalf("gauge de tenant não visitado deveria sumir:\n%s", text)
	}
	if !strings.Contains(text, `tenant_id="t-1"`) || !strings.Contains(text, "} 1") {
		t.Fatalf("gauge atualizado ausente:\n%s", text)
	}
}

func TestMetricsConcurrentWrites(t *testing.T) {
	metrics := NewMetrics()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metrics.AddCounter("amparius_outbox_cycles_total", nil, 1)
		}()
	}
	wg.Wait()
	var out strings.Builder
	if err := metrics.WritePrometheus(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "amparius_outbox_cycles_total 50") {
		t.Fatalf("contador concorrente incorreto:\n%s", out.String())
	}
}

func TestMetricsIgnoresInvalidNamesAndEscapesLabels(t *testing.T) {
	metrics := NewMetrics()
	metrics.AddCounter("nome inválido", nil, 1)
	metrics.SetGauge("amparius_outbox_backlog_events", Labels{"tenant_id": "a\"b\\c\n"}, 2)
	var out strings.Builder
	if err := metrics.WritePrometheus(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "nome inválido") {
		t.Fatalf("nome inválido não deveria ser exposto:\n%s", text)
	}
	if !strings.Contains(text, `tenant_id="a\"b\\c\n"`) {
		t.Fatalf("label não escapado:\n%s", text)
	}
}
