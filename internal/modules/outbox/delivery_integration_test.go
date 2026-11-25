package outbox_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestSucceedRecordsDeliveryOnceInSameTransaction prova o efeito mínimo da
// OUT-01B: a confirmação de entrega é gravada na MESMA transação do status
// 'processed', de forma idempotente por (tenant_id, event_id). Replay ou
// Succeed repetido não duplica o efeito.
func TestSucceedRecordsDeliveryOnceInSameTransaction(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	event := env.appendEvent(t, 0, "delivery:"+uuid.NewString(), 3)
	claimed, err := env.repository.Claim(ctxA, 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: events=%d err=%v", len(claimed), err)
	}
	if _, err := env.repository.Succeed(ctxA, event.ID); err != nil {
		t.Fatal(err)
	}
	deliveries := env.countDeliveries(t, ctxA, event.ID)
	if deliveries != 1 {
		t.Fatalf("Succeed deveria gravar 1 entrega, obtido %d", deliveries)
	}
	// Segundo Succeed é stale (evento já processed) e não cria outra entrega.
	if _, err := env.repository.Succeed(ctxA, event.ID); err == nil {
		t.Fatal("segundo Succeed deveria falhar")
	}
	if deliveries = env.countDeliveries(t, ctxA, event.ID); deliveries != 1 {
		t.Fatalf("replay de Succeed duplicou entrega: %d", deliveries)
	}
	// A linha de entrega carrega só referências opacas: sem payload, sem erro
	// persistido, sem valor de texto livre que possa virar PII.
	var columns int
	if err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT count(*) FROM information_schema.columns
WHERE table_schema='outbox' AND table_name='delivery'
  AND column_name IN ('payload','last_error','notes','name','email','document')`,
		).Scan(&columns)
	}); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatalf("delivery não deveria ter coluna de payload/PII: %d", columns)
	}
}

// TestDeliveryRespectsRLSIsolation prova que a entrega também fica isolada por
// tenant com o papel amparius_app (NOSUPERUSER/NOBYPASSRLS).
func TestDeliveryRespectsRLSIsolation(t *testing.T) {
	env := newEnvironment(t, 2)
	ctxA, ctxB := env.tenantContext(t, 0), env.tenantContext(t, 1)
	event := env.appendEvent(t, 0, "delivery:rls:"+uuid.NewString(), 3)
	if _, err := env.repository.Claim(ctxA, 10, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := env.repository.Succeed(ctxA, event.ID); err != nil {
		t.Fatal(err)
	}
	if got := env.countDeliveries(t, ctxA, event.ID); got != 1 {
		t.Fatalf("tenant A deveria ver 1 entrega, obtido %d", got)
	}
	var visible int
	if err := env.database.WithTenantTx(ctxB, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.delivery WHERE event_id=$1`, event.ID).Scan(&visible)
	}); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("RLS vazou entrega para tenant B: %d", visible)
	}
	// O papel de aplicação não pode apagar a trilha de entrega: falta o grant
	// de DELETE e, mesmo com grant futuro, o trigger append-only barra.
	err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM outbox.delivery WHERE tenant_id=$1 AND event_id=$2`, env.tenants[0], event.ID)
		return err
	})
	if err == nil {
		t.Fatal("DELETE da entrega deveria ser bloqueado")
	}
	if !strings.Contains(err.Error(), "permission denied") && !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("barreira inesperada no DELETE da entrega: %v", err)
	}
}

func (e *environment) countDeliveries(t *testing.T, ctx context.Context, eventID uuid.UUID) int {
	t.Helper()
	var count int
	if err := e.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.delivery WHERE event_id=$1`, eventID).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
