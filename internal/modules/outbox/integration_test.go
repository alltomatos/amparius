package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type environment struct {
	ctx        context.Context
	database   *db.DB
	repository *outbox.Repository
	tenants    []uuid.UUID
	adminDSN   string
}

func newEnvironment(t *testing.T, tenantCount int) *environment {
	t.Helper()
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		database.Close()
		t.Fatal(err)
	}
	var role string
	var bypass bool
	if err := database.WithTenantTx(db.WithTenant(ctx, uuid.New()), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &bypass)
	}); err != nil {
		admin.Close()
		database.Close()
		t.Fatal(err)
	}
	if role != "amparius_app" || bypass {
		admin.Close()
		database.Close()
		t.Fatalf("teste precisa usar amparius_app sem BYPASSRLS; role=%s bypass=%v", role, bypass)
	}
	env := &environment{ctx: ctx, database: database, adminDSN: adminDSN}
	t.Cleanup(func() {
		env.cleanup(t, admin)
		database.Close()
	})
	repository, err := outbox.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	env.repository = repository
	for i := 0; i < tenantCount; i++ {
		tenant := uuid.New()
		env.tenants = append(env.tenants, tenant)
		if err := database.WithTenantTx(db.WithTenant(ctx, tenant), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, tenant, "Outbox "+tenant.String(), "outbox-"+tenant.String())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func (e *environment) cleanup(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	defer admin.Close()
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err == nil {
		defer func() { _ = tx.Rollback(ctx) }()
		for _, statement := range []string{
			"ALTER TABLE outbox.event_attempt DISABLE TRIGGER event_attempt_append_only",
			"ALTER TABLE outbox.delivery DISABLE TRIGGER delivery_append_only",
			"ALTER TABLE outbox.event DISABLE TRIGGER event_history_preserve",
			"DELETE FROM outbox.delivery WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM outbox.event_attempt WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM outbox.event WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
			"ALTER TABLE outbox.event_attempt ENABLE TRIGGER event_attempt_append_only",
			"ALTER TABLE outbox.delivery ENABLE TRIGGER delivery_append_only",
			"ALTER TABLE outbox.event ENABLE TRIGGER event_history_preserve",
		} {
			if err = execCleanup(ctx, tx, statement, e.tenants); err != nil {
				break
			}
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
	}
	if err != nil {
		t.Errorf("cleanup administrativo da outbox: %v", err)
	}
	var remaining int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, e.tenants).Scan(&remaining); err != nil {
		t.Errorf("confirmar cleanup da outbox: %v", err)
	} else if remaining != 0 {
		t.Errorf("cleanup da outbox deixou %d tenant(s)", remaining)
	}
}

func execCleanup(ctx context.Context, tx pgx.Tx, statement string, tenants []uuid.UUID) error {
	if strings.Contains(statement, "$1") {
		_, err := tx.Exec(ctx, statement, tenants)
		return err
	}
	_, err := tx.Exec(ctx, statement)
	return err
}

func (e *environment) tenantContext(t *testing.T, index int) context.Context {
	t.Helper()
	return db.WithTenant(e.ctx, e.tenants[index])
}

// adminPool abre uma conexão administrativa para preparar cenários de teste
// (triggers temporários). O papel de aplicação continua sendo o único usado
// nas operações de negócio, sempre NOSUPERUSER/NOBYPASSRLS.
func (e *environment) adminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin, err := pgxpool.New(e.ctx, e.adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	return admin
}

func (e *environment) appendEvent(t *testing.T, tenantIndex int, key string, maxAttempts int) outbox.Event {
	t.Helper()
	event, err := e.repository.Append(e.tenantContext(t, tenantIndex), outbox.Event{
		AggregateType:  "finance.title",
		AggregateID:    uuid.New(),
		EventType:      "finance.title.created",
		Version:        1,
		Payload:        json.RawMessage(`{"amount":1}`),
		IdempotencyKey: key,
		MaxAttempts:    maxAttempts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestAppendIsIdempotentAndRLSIsolatesTenants(t *testing.T) {
	env := newEnvironment(t, 2)
	ctxA, ctxB := env.tenantContext(t, 0), env.tenantContext(t, 1)
	first := env.appendEvent(t, 0, "order:1", 0)

	replay, err := env.repository.Append(ctxA, outbox.Event{
		AggregateType:  "finance.title",
		AggregateID:    first.AggregateID,
		EventType:      "finance.title.created",
		Version:        1,
		Payload:        json.RawMessage(`{ "amount" : 1 }`),
		IdempotencyKey: "order:1",
	})
	if err != nil || replay.ID != first.ID {
		t.Fatalf("replay idempotente: id=%v err=%v", replay.ID, err)
	}
	if _, err := env.repository.Append(ctxA, outbox.Event{
		AggregateType:  "finance.title",
		AggregateID:    first.AggregateID,
		EventType:      "finance.title.created",
		Version:        1,
		Payload:        json.RawMessage(`{"amount":2}`),
		IdempotencyKey: "order:1",
	}); !errors.Is(err, outbox.ErrIdempotencyConflict) {
		t.Fatalf("chave reutilizada com outro payload deveria conflitar: %v", err)
	}
	stored, err := env.repository.GetByID(ctxA, first.ID)
	if err != nil || stored.Status != "pending" || stored.Attempts != 0 || stored.MaxAttempts != outbox.DefaultMaxAttempts {
		t.Fatalf("evento padrao: %+v err=%v", stored, err)
	}
	if _, err := env.repository.GetByID(ctxB, first.ID); !errors.Is(err, outbox.ErrNotFound) {
		t.Fatalf("RLS deveria esconder evento de outro tenant: %v", err)
	}
	if events, err := env.repository.Claim(ctxB, 10, time.Minute); err != nil || len(events) != 0 {
		t.Fatalf("claim de outro tenant: events=%d err=%v", len(events), err)
	}

	env.appendEvent(t, 1, "order:2", 0)
	tenants, err := env.repository.PendingTenants(env.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsTenant(tenants, env.tenants[0]) || !containsTenant(tenants, env.tenants[1]) {
		t.Fatalf("pending_tenants deveria listar os dois tenants: %v", tenants)
	}

	if claimed, err := env.repository.Claim(ctxA, 10, time.Minute); err != nil || len(claimed) != 1 || claimed[0].ID != first.ID {
		t.Fatalf("claim para gerar trilha: events=%+v err=%v", claimed, err)
	}

	probes := []struct {
		name string
		sql  string
	}{
		{"event_attempt append-only", `UPDATE outbox.event_attempt SET outcome='retry' WHERE tenant_id=$1 AND event_id=$2`},
		{"evento imutavel", `UPDATE outbox.event SET payload='{}'::jsonb WHERE tenant_id=$1 AND id=$2`},
		{"evento preserva historico", `DELETE FROM outbox.event WHERE tenant_id=$1 AND id=$2`},
	}
	for _, probe := range probes {
		err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, probe.sql, env.tenants[0], first.ID)
			return err
		})
		if err == nil {
			t.Fatalf("%s deveria ser bloqueado pelo banco", probe.name)
		}
	}
}

func TestClaimLeaseRetryAndTerminalFailure(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	event := env.appendEvent(t, 0, "order:lease", 0)
	claimed, err := env.repository.Claim(ctxA, 10, 2*time.Second)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim inicial: events=%d err=%v", len(claimed), err)
	}
	if claimed[0].ID != event.ID || claimed[0].Status != "processing" || claimed[0].Attempts != 1 {
		t.Fatalf("claim deveria marcar processing e attempts=1: %+v", claimed[0])
	}
	if again, err := env.repository.Claim(ctxA, 10, 2*time.Second); err != nil || len(again) != 0 {
		t.Fatalf("lease vigente nao deveria reivindicar de novo: events=%d err=%v", len(again), err)
	}
	processed, err := env.repository.Succeed(ctxA, event.ID)
	if err != nil || processed.Status != "processed" || processed.ProcessedAt == nil {
		t.Fatalf("concluir evento: %+v err=%v", processed, err)
	}
	if _, err := env.repository.Succeed(ctxA, event.ID); !errors.Is(err, outbox.ErrStaleEvent) {
		t.Fatalf("segundo Succeed deveria ser stale: %v", err)
	}
	if events, err := env.repository.Claim(ctxA, 10, time.Minute); err != nil || len(events) != 0 {
		t.Fatalf("evento processado nao deveria ser reivindicado: events=%d err=%v", len(events), err)
	}

	flaky := env.appendEvent(t, 0, "order:retry", 2)
	if events, err := env.repository.Claim(ctxA, 10, time.Minute); err != nil || len(events) != 1 || events[0].ID != flaky.ID {
		t.Fatalf("claim do evento flaky: events=%d err=%v", len(events), err)
	}
	failed, err := env.repository.Fail(ctxA, flaky.ID, errors.New("falha sintetica"), 0)
	if err != nil || failed.Status != "pending" || failed.LastError == nil || !strings.Contains(*failed.LastError, "falha sintetica") {
		t.Fatalf("falha deveria reagendar: %+v err=%v", failed, err)
	}
	if events, err := env.repository.Claim(ctxA, 10, time.Minute); err != nil || len(events) != 1 || events[0].Attempts != 2 {
		t.Fatalf("retry imediato deveria reivindicar com attempts=2: events=%+v err=%v", events, err)
	}
	terminal, err := env.repository.Fail(ctxA, flaky.ID, errors.New("falha final"), time.Second)
	if err != nil || terminal.Status != "failed" || terminal.ProcessedAt != nil {
		t.Fatalf("ultima tentativa deveria falhar em definitivo: %+v err=%v", terminal, err)
	}
	if events, err := env.repository.Claim(ctxA, 10, time.Minute); err != nil || len(events) != 0 {
		t.Fatalf("evento failed nao deveria voltar: events=%d err=%v", len(events), err)
	}
	var audit int
	if err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.event_attempt WHERE tenant_id=$1 AND event_id=$2`, env.tenants[0], flaky.ID).Scan(&audit)
	}); err != nil {
		t.Fatal(err)
	}
	if audit != 4 {
		t.Fatalf("trilha deveria ter claimed/retry/claimed/failed, obtido %d", audit)
	}

	abandoned := env.appendEvent(t, 0, "order:abandoned", 1)
	if events, err := env.repository.Claim(ctxA, 10, time.Millisecond); err != nil || len(events) != 1 || events[0].ID != abandoned.ID {
		t.Fatalf("claim do evento abandonado: events=%+v err=%v", events, err)
	}
	time.Sleep(20 * time.Millisecond)
	reaped, err := env.repository.ReapExpired(ctxA)
	if err != nil || reaped != 1 {
		t.Fatalf("ReapExpired: reaped=%d err=%v", reaped, err)
	}
	reapedEvent, err := env.repository.GetByID(ctxA, abandoned.ID)
	if err != nil || reapedEvent.Status != "failed" || reapedEvent.LastError == nil {
		t.Fatalf("lease expirado deveria falhar em definitivo: %+v err=%v", reapedEvent, err)
	}
}

type scriptedHandler struct {
	t        *testing.T
	attempts map[string]int
}

func (h *scriptedHandler) Handle(ctx context.Context, event outbox.Event) error {
	h.t.Helper()
	tenant, err := db.TenantFromContext(ctx)
	if err != nil || tenant != event.TenantID {
		return errors.New("handler recebeu tenant divergente")
	}
	h.attempts[event.IdempotencyKey]++
	switch event.IdempotencyKey {
	case "fluxo:falha-uma-vez":
		if h.attempts[event.IdempotencyKey] == 1 {
			return errors.New("falha transitoria sintetica")
		}
	case "fluxo:panic":
		panic("panic sintetico")
	}
	return nil
}

func TestDispatcherClaimsDeliversAndRetries(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	handler := &scriptedHandler{t: t, attempts: map[string]int{}}
	dispatcher, err := outbox.NewDispatcher(env.repository, handler, outbox.DispatcherConfig{
		Batch:      10,
		MaxTenants: 10,
		Lease:      time.Minute,
		Interval:   time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := env.appendEvent(t, 0, "fluxo:ok", 0)
	flaky := env.appendEvent(t, 0, "fluxo:falha-uma-vez", 3)
	result, err := dispatcher.RunOnce(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Tenants != 1 || result.Claimed != 2 || result.Succeeded != 1 || result.Retried != 1 {
		t.Fatalf("primeiro ciclo inesperado: %+v", result)
	}
	okEvent, err := env.repository.GetByID(ctxA, ok.ID)
	if err != nil || okEvent.Status != "processed" {
		t.Fatalf("evento ok deveria estar processado: %+v err=%v", okEvent, err)
	}
	flakyEvent, err := env.repository.GetByID(ctxA, flaky.ID)
	if err != nil || flakyEvent.Status != "pending" || flakyEvent.Attempts != 1 || flakyEvent.LastError == nil {
		t.Fatalf("evento flaky deveria estar pending com erro: %+v err=%v", flakyEvent, err)
	}
	again, err := dispatcher.RunOnce(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.Tenants != 0 || again.Claimed != 0 {
		t.Fatalf("backoff deveria esconder o evento flaky neste ciclo: %+v", again)
	}

	panicked := env.appendEvent(t, 0, "fluxo:panic", 1)
	result, err = dispatcher.RunOnce(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 1 || result.Failed != 1 {
		t.Fatalf("panic deveria falhar em definitivo: %+v", result)
	}
	panicEvent, err := env.repository.GetByID(ctxA, panicked.ID)
	if err != nil || panicEvent.Status != "failed" || panicEvent.LastError == nil || !strings.Contains(*panicEvent.LastError, "panic") {
		t.Fatalf("evento com panic: %+v err=%v", panicEvent, err)
	}
}

func containsTenant(tenants []uuid.UUID, target uuid.UUID) bool {
	for _, tenant := range tenants {
		if tenant == target {
			return true
		}
	}
	return false
}
