package outbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/log"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

// scriptedStore simula o repositório para provar o contrato de observabilidade
// sem PostgreSQL: o alvo é log/métrica, não a persistência (coberta pela
// integração).
type scriptedStore struct {
	tenants  []uuid.UUID
	backlog  map[uuid.UUID]outbox.Backlog
	events   map[uuid.UUID][]outbox.Event
	terminal map[uuid.UUID]bool
	failCall int
}

func newScriptedStore() *scriptedStore {
	return &scriptedStore{
		backlog:  map[uuid.UUID]outbox.Backlog{},
		events:   map[uuid.UUID][]outbox.Event{},
		terminal: map[uuid.UUID]bool{},
	}
}

func (s *scriptedStore) PendingTenants(context.Context, int) ([]uuid.UUID, error) {
	return s.tenants, nil
}

func (s *scriptedStore) ReapExpired(context.Context) (int, error) { return 0, nil }

func (s *scriptedStore) Claim(ctx context.Context, _ int, _ time.Duration) ([]outbox.Event, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.events[tenant], nil
}

func (s *scriptedStore) Succeed(ctx context.Context, id uuid.UUID) (outbox.Event, error) {
	if _, err := db.TenantFromContext(ctx); err != nil {
		return outbox.Event{}, err
	}
	return outbox.Event{ID: id, Status: "processed"}, nil
}

func (s *scriptedStore) Fail(ctx context.Context, id uuid.UUID, _ error, _ time.Duration) (outbox.Event, error) {
	if _, err := db.TenantFromContext(ctx); err != nil {
		return outbox.Event{}, err
	}
	s.failCall++
	status := "pending"
	if s.terminal[id] {
		status = "failed"
	}
	return outbox.Event{ID: id, Status: status, Attempts: 1}, nil
}

func (s *scriptedStore) Backlog(ctx context.Context) (outbox.Backlog, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return outbox.Backlog{}, err
	}
	return s.backlog[tenant], nil
}

func decodeLogLines(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()
	lines := []map[string]any{}
	for _, raw := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if raw == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("linha de log não é JSON: %v (%s)", err, raw)
		}
		lines = append(lines, payload)
	}
	return lines
}

func findLog(t *testing.T, lines []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, line := range lines {
		if line["msg"] == msg {
			return line
		}
	}
	t.Fatalf("log %q ausente em %v", msg, lines)
	return nil
}

func TestDispatcherLogsTenantCorrelationAndTraceWithoutPayload(t *testing.T) {
	tenant := uuid.New()
	eventID := uuid.New()
	aggregateID := uuid.New()
	store := newScriptedStore()
	store.tenants = []uuid.UUID{tenant}
	store.backlog[tenant] = outbox.Backlog{Pending: 2, Processing: 1, Failed: 0, OldestPendingAge: 42 * time.Second}
	store.events[tenant] = []outbox.Event{{
		ID: eventID, TenantID: tenant, AggregateType: "funeral.contract", AggregateID: aggregateID,
		EventType: "funeral.contract.created", Version: 1,
		Payload: json.RawMessage(`{"contract_id":"` + aggregateID.String() + `","total_amount_cents":15000}`),
		Status:  "processing", Attempts: 1, MaxAttempts: 3,
	}}

	var buffer bytes.Buffer
	metrics := observability.NewMetrics()
	dispatcher, err := outbox.NewDispatcher(store, noopHandler{}, outbox.DispatcherConfig{
		Batch: 10, MaxTenants: 10, Lease: time.Minute, Interval: time.Minute,
		Logger:  log.New(&buffer, slog.LevelDebug).With("worker_id", "worker-test-1"),
		Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.RunOnce(observability.WithCorrelationID(context.Background(), "req-correlacao-1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 1 || result.Succeeded != 1 {
		t.Fatalf("ciclo inesperado: %+v", result)
	}
	if result.Backlog.Total() != 3 || result.Backlog.OldestPendingAge != 42*time.Second {
		t.Fatalf("backlog agregado inesperado: %+v", result.Backlog)
	}

	lines := decodeLogLines(t, &buffer)
	cycle := findLog(t, lines, "outbox ciclo concluido")
	if cycle["correlation_id"] != "req-correlacao-1" || cycle["worker_id"] != "worker-test-1" {
		t.Fatalf("correlação do ciclo ausente: %v", cycle)
	}
	if cycle["backlog_pending"] != float64(2) || cycle["claimed"] != float64(1) {
		t.Fatalf("contadores do ciclo: %v", cycle)
	}

	delivered := findLog(t, lines, "outbox evento entregue")
	if delivered["tenant_id"] != tenant.String() {
		t.Fatalf("tenant ausente no log do evento: %v", delivered)
	}
	if delivered["correlation_id"] != "req-correlacao-1" {
		t.Fatalf("correlation id ausente no evento: %v", delivered)
	}
	if delivered["trace_id"] != eventID.String() || delivered["event_id"] != eventID.String() {
		t.Fatalf("trace id do evento: %v", delivered)
	}
	if delivered["event_type"] != "funeral.contract.created" {
		t.Fatalf("event_type ausente: %v", delivered)
	}
	rawLogs := buffer.String()
	for _, forbidden := range []string{"total_amount_cents", "15000", "payload"} {
		if strings.Contains(rawLogs, forbidden) {
			t.Fatalf("log vazou payload (%q):\n%s", forbidden, rawLogs)
		}
	}

	var exposition strings.Builder
	if err := metrics.WritePrometheus(&exposition); err != nil {
		t.Fatal(err)
	}
	text := exposition.String()
	for _, want := range []string{
		"amparius_outbox_cycles_total 1",
		"amparius_outbox_events_claimed_total 1",
		"amparius_outbox_events_succeeded_total 1",
		`amparius_outbox_backlog_events{status="pending",tenant_id="` + tenant.String() + `"} 2`,
		`amparius_outbox_backlog_events{status="processing",tenant_id="` + tenant.String() + `"} 1`,
		`amparius_outbox_backlog_oldest_age_seconds{tenant_id="` + tenant.String() + `"} 42`,
		"amparius_outbox_delivery_duration_seconds_count 1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("métrica ausente %q:\n%s", want, text)
		}
	}
}

func TestDispatcherRetryAndFailureLogsRedactSensitiveText(t *testing.T) {
	tenant := uuid.New()
	retryID, failedID := uuid.New(), uuid.New()
	store := newScriptedStore()
	store.tenants = []uuid.UUID{tenant}
	store.terminal[failedID] = true
	store.events[tenant] = []outbox.Event{
		{ID: retryID, TenantID: tenant, EventType: "funeral.contract.created", Version: 1, Status: "processing", Attempts: 2, MaxAttempts: 5},
		{ID: failedID, TenantID: tenant, EventType: "funeral.contract.created", Version: 1, Status: "processing", Attempts: 5, MaxAttempts: 5},
	}

	var buffer bytes.Buffer
	metrics := observability.NewMetrics()
	dispatcher, err := outbox.NewDispatcher(store, errorHandler{err: errors.New("falha com maria@example.com CPF 52998224725 token ABC123SEGREDO")}, outbox.DispatcherConfig{
		Batch: 10, MaxTenants: 10, Lease: time.Minute, Interval: time.Minute,
		Logger:  log.New(&buffer, slog.LevelDebug).With("worker_id", "worker-test-2"),
		Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Retried != 1 || result.Failed != 1 {
		t.Fatalf("retry/falha inesperados: %+v", result)
	}
	logs := buffer.String()
	for _, forbidden := range []string{"maria@example.com", "52998224725", "ABC123SEGREDO"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("log vazou %q:\n%s", forbidden, logs)
		}
	}
	if !strings.Contains(logs, "[redigido]") {
		t.Fatalf("log deveria marcar redação:\n%s", logs)
	}
	lines := decodeLogLines(t, &buffer)
	retry := findLog(t, lines, "outbox evento reagendado")
	if retry["trace_id"] != retryID.String() || retry["tenant_id"] != tenant.String() {
		t.Fatalf("log de retry sem trace/tenant: %v", retry)
	}
	failed := findLog(t, lines, "outbox evento esgotou tentativas")
	if failed["trace_id"] != failedID.String() {
		t.Fatalf("log de falha sem trace: %v", failed)
	}
	var exposition strings.Builder
	if err := metrics.WritePrometheus(&exposition); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"amparius_outbox_events_retried_total 1",
		"amparius_outbox_events_failed_total 1",
	} {
		if !strings.Contains(exposition.String(), want) {
			t.Fatalf("contador ausente %q:\n%s", want, exposition.String())
		}
	}
}

func TestDispatcherBacklogSampleErrorDoesNotStopDelivery(t *testing.T) {
	tenant := uuid.New()
	store := &failingBacklogStore{scriptedStore: *newScriptedStore(), tenant: tenant}
	store.tenants = []uuid.UUID{tenant}
	store.events[tenant] = []outbox.Event{{
		ID: uuid.New(), TenantID: tenant, EventType: "funeral.contract.created", Version: 1,
		Status: "processing", Attempts: 1, MaxAttempts: 3,
	}}
	var buffer bytes.Buffer
	metrics := observability.NewMetrics()
	dispatcher, err := outbox.NewDispatcher(store, noopHandler{}, outbox.DispatcherConfig{
		Batch: 10, MaxTenants: 10, Lease: time.Minute, Interval: time.Minute,
		Logger: log.New(&buffer, slog.LevelDebug), Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("falha de amostragem não deveria derrubar o ciclo: %v", err)
	}
	if result.Succeeded != 1 {
		t.Fatalf("entrega interrompida: %+v", result)
	}
	var exposition strings.Builder
	if err := metrics.WritePrometheus(&exposition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exposition.String(), "amparius_outbox_backlog_sample_errors_total 1") {
		t.Fatalf("erro de amostragem não contabilizado:\n%s", exposition.String())
	}
}

type failingBacklogStore struct {
	scriptedStore
	tenant uuid.UUID
}

func (s *failingBacklogStore) Backlog(context.Context) (outbox.Backlog, error) {
	return outbox.Backlog{}, errors.New("backlog indisponivel")
}

type errorHandler struct{ err error }

func (h errorHandler) Handle(context.Context, outbox.Event) error { return h.err }
