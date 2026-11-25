package funeral_test

import (
	"context"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestFuneralServiceOrderJourneyIdempotencyAndIsolation(t *testing.T) {
	fixture := newAtomicityFixture(t)
	repository, err := funeral.NewRepository(fixture.database, fixture.events)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := repository.CreateContract(fixture.ctx(), fixture.input("service-order-contract"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(fixture.ctx(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE funeral.contract SET status='active', updated_at=now() WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, fixture.tenant, fixture.company, contract.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	order, err := repository.CreateServiceOrder(fixture.ctx(), funeral.ServiceOrderCreate{ContractID: contract.ID, Kind: "funeral_service", Description: "atendimento", IdempotencyKey: "service-order-create"})
	if err != nil {
		t.Fatalf("abrir OS: %v", err)
	}
	if order.Status != funeral.ServiceOrderOpen || len(order.StatusHistory) != 1 {
		t.Fatalf("OS inicial = %#v", order)
	}
	replay, err := repository.CreateServiceOrder(fixture.ctx(), funeral.ServiceOrderCreate{ContractID: contract.ID, Kind: "funeral_service", Description: "atendimento", IdempotencyKey: "service-order-create"})
	if err != nil || replay.ID != order.ID {
		t.Fatalf("replay = %s/%v", replay.ID, err)
	}
	listed, err := repository.ListServiceOrders(fixture.ctx(), contract.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != order.ID {
		t.Fatalf("listagem da OS = %#v/%v", listed, err)
	}
	started, err := repository.UpdateServiceOrder(fixture.ctx(), funeral.ServiceOrderPatch{ID: order.ID, Status: funeral.ServiceOrderInProgress, IdempotencyKey: "service-order-start"})
	if err != nil || started.Status != funeral.ServiceOrderInProgress || len(started.StatusHistory) != 2 {
		t.Fatalf("inicio da OS = %#v/%v", started, err)
	}

	description := "atendimento atualizado"
	updated, err := repository.UpdateServiceOrder(fixture.ctx(), funeral.ServiceOrderPatch{ID: order.ID, Description: &description, IdempotencyKey: "service-order-update"})
	if err != nil || updated.Description != description || updated.Version != 3 {
		t.Fatalf("update = %#v/%v", updated, err)
	}
	updatedReplay, err := repository.UpdateServiceOrder(fixture.ctx(), funeral.ServiceOrderPatch{ID: order.ID, Description: &description, IdempotencyKey: "service-order-update"})
	if err != nil || updatedReplay.Version != updated.Version {
		t.Fatalf("replay update = %#v/%v", updatedReplay, err)
	}
	completed, err := repository.CompleteServiceOrder(fixture.ctx(), order.ID, "service-order-complete")
	if err != nil || completed.Status != funeral.ServiceOrderCompleted || completed.CompletedAt == nil || len(completed.StatusHistory) != 3 {
		t.Fatalf("conclusao = %#v/%v", completed, err)
	}
	completedReplay, err := repository.CompleteServiceOrder(fixture.ctx(), order.ID, "service-order-complete")
	if err != nil || completedReplay.Version != completed.Version {
		t.Fatalf("replay conclusao = %#v/%v", completedReplay, err)
	}

	if got := fixture.count(t, `SELECT count(*) FROM funeral.service_order WHERE tenant_id=$1`, fixture.tenant); got != 1 {
		t.Fatalf("OS duplicada: %d", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM funeral.service_order_status_history WHERE tenant_id=$1`, fixture.tenant); got != 3 {
		t.Fatalf("historico = %d", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1 AND aggregate_id=$2`, fixture.tenant, order.ID); got != 4 {
		t.Fatalf("eventos da OS = %d", got)
	}

	foreignCtx := db.WithCompany(db.WithTenant(context.Background(), fixture.tenant), uuid.New())
	if _, err := repository.GetServiceOrderByID(foreignCtx, order.ID); err != funeral.ErrServiceOrderNotFound {
		t.Fatalf("empresa diferente deveria ocultar OS: %v", err)
	}
}
