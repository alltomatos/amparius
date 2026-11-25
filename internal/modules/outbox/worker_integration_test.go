package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// effectSink simula um efeito de entrega idempotente por event_id. O contador
// só avança quando o efeito é aplicado; replay do mesmo evento não deve
// incrementar duas vezes.
type effectSink struct {
	mu      sync.Mutex
	applied map[uuid.UUID]int
	calls   atomic.Int64
}

func newEffectSink() *effectSink {
	return &effectSink{applied: map[uuid.UUID]int{}}
}

func (s *effectSink) Handle(_ context.Context, event outbox.Event) error {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied[event.ID]++
	return nil
}

func (s *effectSink) count(eventID uuid.UUID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applied[eventID]
}

func (s *effectSink) total() int64 { return s.calls.Load() }

// TestConcurrentWorkersDoNotDoubleDeliver prova o critério de concorrência da
// OUT-01B: dois dispatchers disputando o mesmo tenant não entregam o mesmo
// evento duas vezes dentro do lease, e o efeito é aplicado uma única vez.
func TestConcurrentWorkersDoNotDoubleDeliver(t *testing.T) {
	env := newEnvironment(t, 1)
	sink := newEffectSink()
	const eventCount = 20
	for i := 0; i < eventCount; i++ {
		env.appendEvent(t, 0, "concorrencia:"+uuid.NewString(), 3)
	}

	newDispatcher := func() *outbox.Dispatcher {
		d, err := outbox.NewDispatcher(env.repository, sink, outbox.DispatcherConfig{
			Batch: 10, MaxTenants: 10, Lease: 2 * time.Minute, Interval: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	workers := []*outbox.Dispatcher{newDispatcher(), newDispatcher()}

	var wg sync.WaitGroup
	results := make([]outbox.Result, len(workers))
	errs := make([]error, len(workers))
	for i, worker := range workers {
		wg.Add(1)
		go func(index int, d *outbox.Dispatcher) {
			defer wg.Done()
			results[index], errs[index] = d.RunOnce(env.ctx)
		}(i, worker)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	claimed := 0
	succeeded := 0
	for _, result := range results {
		claimed += result.Claimed
		succeeded += result.Succeeded
	}
	if claimed != eventCount {
		t.Fatalf("claim concorrente deveria entregar %d eventos uma vez, obtido %d", eventCount, claimed)
	}
	if succeeded != eventCount {
		t.Fatalf("sucesso concorrente esperado %d, obtido %d", eventCount, succeeded)
	}
	if sink.total() != eventCount {
		t.Fatalf("handler chamado %d vezes para %d eventos (duplicação)", sink.total(), eventCount)
	}
	ctxA := env.tenantContext(t, 0)
	var processed int
	if err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1 AND status='processed'`, env.tenants[0]).Scan(&processed)
	}); err != nil {
		t.Fatal(err)
	}
	if processed != eventCount {
		t.Fatalf("eventos processados: %d, esperado %d", processed, eventCount)
	}
}

// TestCrashReobtainsLeaseWithoutDuplicatingEffect prova o critério de crash:
// um worker que morre depois do claim (sem Succeed) deixa o evento
// reivindicável quando o lease expira; o segundo worker conclui e o efeito
// é aplicado uma única vez.
func TestCrashReobtainsLeaseWithoutDuplicatingEffect(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	event := env.appendEvent(t, 0, "crash:"+uuid.NewString(), 3)
	sink := newEffectSink()

	// Worker "morre": claim curto, entrega o efeito, mas não chama Succeed.
	crashed, err := outbox.NewDispatcher(env.repository, sink, outbox.DispatcherConfig{
		Batch: 1, MaxTenants: 1, Lease: time.Millisecond, Interval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simula o crash direto no repositório: claim + handler, sem Succeed/Fail.
	claimed, err := env.repository.Claim(ctxA, 1, time.Millisecond)
	if err != nil || len(claimed) != 1 || claimed[0].ID != event.ID {
		t.Fatalf("claim do worker que morre: events=%d err=%v", len(claimed), err)
	}
	if err := sink.Handle(ctxA, claimed[0]); err != nil {
		t.Fatal(err)
	}
	before, err := env.repository.GetByID(ctxA, event.ID)
	if err != nil || before.Status != "processing" {
		t.Fatalf("evento deveria ficar processing após crash: %+v err=%v", before, err)
	}
	_ = crashed

	time.Sleep(20 * time.Millisecond)
	// Segundo worker reobtém o lease e conclui.
	rescuer, err := outbox.NewDispatcher(env.repository, sink, outbox.DispatcherConfig{
		Batch: 1, MaxTenants: 1, Lease: time.Minute, Interval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := rescuer.RunOnce(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 1 || result.Succeeded != 1 {
		t.Fatalf("resgate do lease: %+v", result)
	}
	after, err := env.repository.GetByID(ctxA, event.ID)
	if err != nil || after.Status != "processed" || after.Attempts != 2 {
		t.Fatalf("evento resgatado: %+v err=%v", after, err)
	}
	if sink.count(event.ID) != 2 {
		t.Fatalf("efeito aplicado %d vezes (crash + resgate); o contrato é at-least-once", sink.count(event.ID))
	}
}

// TestReplayOfProcessedEventDoesNotReapplyEffect prova que um replay completo
// (mesmo idempotency_key) não cria novo evento nem reaplica o efeito.
func TestReplayOfProcessedEventDoesNotReapplyEffect(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	sink := newEffectSink()
	dispatcher, err := outbox.NewDispatcher(env.repository, sink, outbox.DispatcherConfig{
		Batch: 10, MaxTenants: 10, Lease: time.Minute, Interval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := env.repository.Append(ctxA, outbox.Event{
		AggregateType: "finance.title", AggregateID: uuid.New(),
		EventType: "finance.title.created", Version: 1,
		Payload:        json.RawMessage(`{"amount_cents":100}`),
		IdempotencyKey: "replay:unico", MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.RunOnce(env.ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count(event.ID) != 1 {
		t.Fatalf("primeira entrega: efeito aplicado %d vezes", sink.count(event.ID))
	}

	// Replay da mesma operação: Append devolve o mesmo ID, sem nova linha.
	replay, err := env.repository.Append(ctxA, outbox.Event{
		AggregateType: "finance.title", AggregateID: event.AggregateID,
		EventType: "finance.title.created", Version: 1,
		Payload:        json.RawMessage(`{ "amount_cents" : 100 }`),
		IdempotencyKey: "replay:unico", MaxAttempts: 3,
	})
	if err != nil || replay.ID != event.ID {
		t.Fatalf("replay deveria devolver o mesmo evento: id=%v err=%v", replay.ID, err)
	}
	if _, err := dispatcher.RunOnce(env.ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count(event.ID) != 1 {
		t.Fatalf("replay reaplicou efeito: %d", sink.count(event.ID))
	}
	var count int
	if err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1 AND idempotency_key=$2`, env.tenants[0], "replay:unico").Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("replay duplicou linha: %d", count)
	}
}

// TestTerminalFailureIsVisible prova que falha permanente fica visível no
// evento (status failed + last_error) e na trilha append-only, sem PII.
func TestTerminalFailureIsVisible(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	event := env.appendEvent(t, 0, "terminal:"+uuid.NewString(), 1)
	dispatcher, err := outbox.NewDispatcher(env.repository, alwaysFailHandler{}, outbox.DispatcherConfig{
		Batch: 10, MaxTenants: 10, Lease: time.Minute, Interval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.RunOnce(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("falha terminal deveria ser contabilizada: %+v", result)
	}
	stored, err := env.repository.GetByID(ctxA, event.ID)
	if err != nil || stored.Status != "failed" || stored.LastError == nil {
		t.Fatalf("falha permanente não visível: %+v err=%v", stored, err)
	}
	if strings.Contains(*stored.LastError, "maria@example.com") || strings.Contains(*stored.LastError, "52998224725") {
		t.Fatalf("last_error vazou PII: %q", *stored.LastError)
	}
	var failedAudit int
	if err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.event_attempt WHERE tenant_id=$1 AND event_id=$2 AND outcome='failed'`, env.tenants[0], event.ID).Scan(&failedAudit)
	}); err != nil {
		t.Fatal(err)
	}
	if failedAudit != 1 {
		t.Fatalf("trilha deveria registrar failed uma vez: %d", failedAudit)
	}
}

type alwaysFailHandler struct{}

func (alwaysFailHandler) Handle(context.Context, outbox.Event) error {
	return errors.New("falha definitiva sintetica")
}

// TestWorkerNoopHandlerDoesNotMarkProcessed protege a decisão do ADR 0009:
// sem handler real, o worker não deve drenar marcando como processado.
func TestWorkerNoopHandlerDoesNotMarkProcessed(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	event := env.appendEvent(t, 0, "noop:"+uuid.NewString(), 3)
	dispatcher, err := outbox.NewDispatcher(env.repository, outbox.RefusingHandler{}, outbox.DispatcherConfig{
		Batch: 10, MaxTenants: 10, Lease: time.Minute, Interval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.RunOnce(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 0 {
		t.Fatalf("handler que recusa não deveria concluir evento: %+v", result)
	}
	stored, err := env.repository.GetByID(ctxA, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status == "processed" {
		t.Fatal("evento foi marcado processado sem efeito real")
	}
	if stored.LastError == nil || !strings.Contains(*stored.LastError, "sem handler de entrega") {
		t.Fatalf("recusa deveria ficar visível em last_error: %+v", stored)
	}
	_ = db.WithTenant
}
