package outbox_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestSucceedRollsBackWhenDeliveryInsertFails prova a exigência de atomicidade
// do parecer do Arquiteto de Protocolo: se a escrita da entrega falha, o
// status 'processed' NÃO pode commitar. Um defeito aqui deixaria o evento
// processado sem efeito e sem retentativa.
func TestSucceedRollsBackWhenDeliveryInsertFails(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	event := env.appendEvent(t, 0, "atomicidade:"+uuid.NewString(), 3)
	if _, err := env.repository.Claim(ctxA, 10, time.Minute); err != nil {
		t.Fatal(err)
	}

	// Sabota a entrega apenas nesta transação: trigger que rejeita INSERT em
	// outbox.delivery. O admin cria e remove o trigger; o papel app continua
	// NOSUPERUSER/NOBYPASSRLS. Succeed deve falhar e fazer rollback completo.
	// O trigger é global (DDL), então este teste NÃO pode conviver com outros
	// testes de entrega no mesmo banco. A remoção acontece em defer, com o pool
	// ainda aberto, e falha o teste se não conseguir limpar — nunca deixa resíduo.
	admin := env.adminPool(t)
	_, err := admin.Exec(context.Background(), `
DROP TRIGGER IF EXISTS delivery_force_failure ON outbox.delivery;
DROP FUNCTION IF EXISTS outbox.force_delivery_failure();
CREATE FUNCTION outbox.force_delivery_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	RAISE EXCEPTION 'falha sintetica no insert de delivery';
END;
$$;
CREATE TRIGGER delivery_force_failure BEFORE INSERT ON outbox.delivery
	FOR EACH ROW EXECUTE FUNCTION outbox.force_delivery_failure();`)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	defer func() {
		ctx := context.Background()
		if _, err := admin.Exec(ctx, `DROP TRIGGER IF EXISTS delivery_force_failure ON outbox.delivery`); err != nil {
			t.Errorf("remover trigger temporario de delivery: %v", err)
		}
		if _, err := admin.Exec(ctx, `DROP FUNCTION IF EXISTS outbox.force_delivery_failure()`); err != nil {
			t.Errorf("remover funcao temporaria de delivery: %v", err)
		}
		admin.Close()
	}()

	_, err = env.repository.Succeed(ctxA, event.ID)
	if err == nil {
		t.Fatal("Succeed deveria falhar quando a entrega é rejeitada")
	}
	if !strings.Contains(err.Error(), "falha sintetica") {
		t.Fatalf("erro inesperado: %v", err)
	}

	stored, err := env.repository.GetByID(ctxA, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "processing" {
		t.Fatalf("rollback deveria manter status=processing, obtido %q", stored.Status)
	}
	var deliveries, succeeded int
	if err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM outbox.delivery WHERE tenant_id=$1 AND event_id=$2`, env.tenants[0], event.ID).Scan(&deliveries); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.event_attempt WHERE tenant_id=$1 AND event_id=$2 AND outcome='succeeded'`, env.tenants[0], event.ID).Scan(&succeeded)
	}); err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 || succeeded != 0 {
		t.Fatalf("rollback deixou efeito parcial: deliveries=%d succeeded_audit=%d", deliveries, succeeded)
	}
	_ = time.Minute
}
