package funeral_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestFuneralScheduleRealPostgresConflictIdempotencyIsolationAndCleanup(t *testing.T) {
	fixture := newAtomicityFixture(t)
	repository, err := funeral.NewRepository(fixture.database, fixture.events)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := repository.CreateContract(fixture.ctx(), fixture.input("schedule-contract"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(fixture.ctx(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE funeral.contract SET status='active', updated_at=now() WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, fixture.tenant, fixture.company, contract.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	location, err := repository.CreateLocation(fixture.ctx(), funeral.LocationCreate{Name: "Sala principal", Kind: "wake_room", TimeZone: "America/Fortaleza", IdempotencyKey: "schedule-location"})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := repository.CreateResource(fixture.ctx(), funeral.ResourceCreate{LocationID: location.ID, Name: "Sala principal", Kind: "room", IdempotencyKey: "schedule-resource"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	schedule, err := repository.CreateSchedule(fixture.ctx(), funeral.ScheduleCreate{ContractID: contract.ID, Kind: funeral.ScheduleKindWake, LocationID: location.ID, ResourceID: &resource.ID, StartsAt: start, EndsAt: end, TimeZone: "America/Fortaleza", IdempotencyKey: "schedule-create"})
	if err != nil {
		t.Fatalf("criar agenda: %v", err)
	}
	replay, err := repository.CreateSchedule(fixture.ctx(), funeral.ScheduleCreate{ContractID: contract.ID, Kind: funeral.ScheduleKindWake, LocationID: location.ID, ResourceID: &resource.ID, StartsAt: start, EndsAt: end, TimeZone: "America/Fortaleza", IdempotencyKey: "schedule-create"})
	if err != nil || replay.ID != schedule.ID {
		t.Fatalf("replay agenda: %s/%v", replay.ID, err)
	}
	adjacent, err := repository.CreateSchedule(fixture.ctx(), funeral.ScheduleCreate{ContractID: contract.ID, Kind: funeral.ScheduleKindBurial, LocationID: location.ID, ResourceID: &resource.ID, StartsAt: end, EndsAt: end.Add(time.Hour), TimeZone: "America/Fortaleza", IdempotencyKey: "schedule-adjacent"})
	if err != nil {
		t.Fatalf("intervalo adjacente [fim,inicio) deveria ser aceito: %v", err)
	}
	if adjacent.StartsAt.UTC() != end {
		t.Fatalf("inicio adjacente = %s", adjacent.StartsAt)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := repository.CreateSchedule(fixture.ctx(), funeral.ScheduleCreate{ContractID: contract.ID, Kind: funeral.ScheduleKindWake, LocationID: location.ID, ResourceID: &resource.ID, StartsAt: start.Add(4 * time.Hour), EndsAt: start.Add(6 * time.Hour), TimeZone: "America/Fortaleza", IdempotencyKey: "schedule-race-" + string(rune('a'+i))})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, funeral.ErrScheduleConflict) {
			conflicts++
		} else {
			t.Fatalf("erro inesperado na corrida: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("corrida = sucessos %d, conflitos %d", successes, conflicts)
	}

	confirmed, err := repository.ConfirmScheduleWithReason(fixture.ctx(), schedule.ID, "schedule-confirm", "cliente confirmou")
	if err != nil || confirmed.Status != funeral.ScheduleConfirmed || len(confirmed.StatusHistory) != 2 || confirmed.StatusHistory[1].Reason == nil || *confirmed.StatusHistory[1].Reason != "cliente confirmou" {
		t.Fatalf("confirmacao = %#v/%v", confirmed, err)
	}
	confirmedReplay, err := repository.ConfirmScheduleWithReason(fixture.ctx(), schedule.ID, "schedule-confirm", "cliente confirmou")
	if err != nil || confirmedReplay.Version != confirmed.Version {
		t.Fatalf("replay confirmacao = %#v/%v", confirmedReplay, err)
	}
	cancelResults := make(chan struct {
		schedule funeral.Schedule
		err      error
	}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			item, callErr := repository.CancelScheduleWithReason(fixture.ctx(), schedule.ID, "schedule-cancel", "cliente cancelou")
			cancelResults <- struct {
				schedule funeral.Schedule
				err      error
			}{item, callErr}
		}()
	}
	cancelled := <-cancelResults
	cancelledReplay := <-cancelResults
	for _, result := range []struct {
		schedule funeral.Schedule
		err      error
	}{cancelled, cancelledReplay} {
		if result.err != nil || result.schedule.Status != funeral.ScheduleCancelled || len(result.schedule.StatusHistory) != 3 || result.schedule.StatusHistory[2].Reason == nil || *result.schedule.StatusHistory[2].Reason != "cliente cancelou" {
			t.Fatalf("cancelamento concorrente = %#v/%v", result.schedule, result.err)
		}
	}
	if cancelled.schedule.ID != cancelledReplay.schedule.ID || cancelled.schedule.Version != cancelledReplay.schedule.Version {
		t.Fatalf("replays concorrentes divergiram: %#v / %#v", cancelled.schedule, cancelledReplay.schedule)
	}
	cancelReplay, err := repository.CancelScheduleWithReason(fixture.ctx(), schedule.ID, "schedule-cancel", "cliente cancelou")
	if err != nil || cancelReplay.Version != cancelled.schedule.Version {
		t.Fatalf("replay cancelamento = %#v/%v", cancelReplay, err)
	}
	foreignCtx := db.WithCompany(db.WithTenant(context.Background(), uuid.New()), uuid.New())
	if _, err := repository.GetScheduleByID(foreignCtx, schedule.ID); !errors.Is(err, funeral.ErrScheduleNotFound) {
		t.Fatalf("tenant estrangeiro deveria ocultar agenda: %v", err)
	}
	var payload string
	if err := fixture.database.WithTenantTx(fixture.ctx(), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT payload::text FROM outbox.event WHERE tenant_id=$1 AND aggregate_id=$2 AND event_type='funeral.schedule.created'`, fixture.tenant, schedule.ID).Scan(&payload)
	}); err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal([]byte(payload), &keys); err != nil {
		t.Fatal(err)
	}
	for key := range keys {
		switch key {
		case "schedule_id", "contract_id", "company_id", "kind", "location_id", "resource_id", "starts_at", "ends_at", "time_zone", "status":
		default:
			t.Fatalf("payload de agenda contem chave inesperada %q: %s", key, payload)
		}
	}
	if strings.Contains(payload, "Sala principal") {
		t.Fatalf("payload de agenda contem nome de local: %s", payload)
	}
	var eventTypes map[string]int
	eventTypes = make(map[string]int)
	if err := fixture.database.WithTenantTx(fixture.ctx(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT event_type,event_version,payload::text FROM outbox.event WHERE tenant_id=$1 AND aggregate_id=$2 AND event_type IN ('funeral.schedule.confirmed','funeral.schedule.cancelled')`, fixture.tenant, schedule.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var eventType, eventPayload string
			var version int
			if err := rows.Scan(&eventType, &version, &eventPayload); err != nil {
				return err
			}
			if version != 1 || strings.Contains(eventPayload, "cliente") || strings.Contains(eventPayload, "reason") {
				t.Fatalf("evento de transicao invalido: %s v%d %s", eventType, version, eventPayload)
			}
			eventTypes[eventType]++
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if eventTypes["funeral.schedule.confirmed"] != 1 || eventTypes["funeral.schedule.cancelled"] != 1 {
		t.Fatalf("eventos de transicao = %#v", eventTypes)
	}
	if err := fixture.database.WithTenantTx(fixture.ctx(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM funeral.schedule_status_history WHERE tenant_id=$1 AND schedule_id=$2`, fixture.tenant, schedule.ID)
		return err
	}); err == nil {
		t.Fatal("historico de agenda deveria ser append-only")
	}
	if got := fixture.count(t, `SELECT count(*) FROM funeral.schedule_status_history WHERE tenant_id=$1`, fixture.tenant); got != 5 {
		t.Fatalf("historico da agenda = %d, esperado 5", got)
	}
}

func TestFuneralJourneyRealPostgresIsAtomicIdempotentAndTenantScoped(t *testing.T) {
	fixture := newAtomicityFixture(t)
	repository, err := funeral.NewRepository(fixture.database, fixture.events)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := repository.CreateContract(fixture.ctx(), fixture.input("journey-contract"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.WithTenantTx(fixture.ctx(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE funeral.contract SET status='active', updated_at=now() WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, fixture.tenant, fixture.company, contract.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	location, err := repository.CreateLocation(fixture.ctx(), funeral.LocationCreate{Name: "Sala jornada", Kind: "wake_room", TimeZone: "America/Fortaleza", IdempotencyKey: "journey-location"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	input := funeral.FuneralJourneyCreate{
		ContractID: contract.ID, ServiceOrderKind: "wake", ServiceOrderDescription: "operacao interna",
		ScheduleKind: funeral.ScheduleKindWake, LocationID: location.ID, StartsAt: start,
		EndsAt: start.Add(2 * time.Hour), TimeZone: "America/Fortaleza", IdempotencyKey: "journey-create",
	}
	journey, err := repository.CreateFuneralJourney(fixture.ctx(), input)
	if err != nil {
		t.Fatal(err)
	}
	if journey.ServiceOrder.ContractID != contract.ID || journey.Schedule.ContractID != contract.ID || journey.Schedule.ServiceOrderID == nil || *journey.Schedule.ServiceOrderID != journey.ServiceOrder.ID {
		t.Fatalf("vinculos da jornada divergentes: %#v", journey)
	}
	replay, err := repository.CreateFuneralJourney(fixture.ctx(), input)
	if err != nil || replay.ID != journey.ID || replay.ServiceOrder.ID != journey.ServiceOrder.ID || replay.Schedule.ID != journey.Schedule.ID {
		t.Fatalf("replay da jornada = %#v/%v", replay, err)
	}
	if got := fixture.count(t, `SELECT count(*) FROM funeral.journey_mutation WHERE tenant_id=$1`, fixture.tenant); got != 1 {
		t.Fatalf("registros de jornada = %d", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM funeral.service_order WHERE tenant_id=$1 AND contract_id=$2`, fixture.tenant, contract.ID); got != 1 {
		t.Fatalf("ordens da jornada = %d", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM funeral.schedule WHERE tenant_id=$1 AND contract_id=$2`, fixture.tenant, contract.ID); got != 1 {
		t.Fatalf("agendas da jornada = %d", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1 AND event_type IN ('funeral.journey.created','funeral.service_order.created','funeral.schedule.created')`, fixture.tenant); got != 3 {
		t.Fatalf("eventos da jornada = %d", got)
	}
	foreignCtx := db.WithCompany(db.WithTenant(context.Background(), uuid.New()), fixture.company)
	if got := fixture.countWithContext(t, foreignCtx, `SELECT count(*) FROM funeral.journey_mutation WHERE id=$1`, journey.ID); got != 0 {
		t.Fatalf("RLS expôs jornada a outro tenant: %d", got)
	}

	failingRepository, err := funeral.NewRepository(fixture.database, failingAppender{delegate: fixture.events, cause: errors.New("falha controlada")})
	if err != nil {
		t.Fatal(err)
	}
	input.IdempotencyKey = "journey-rollback"
	if _, err := failingRepository.CreateFuneralJourney(fixture.ctx(), input); err == nil {
		t.Fatal("falha do outbox deveria abortar a jornada")
	}
	if got := fixture.count(t, `SELECT count(*) FROM funeral.journey_mutation WHERE tenant_id=$1 AND idempotency_key=$2`, fixture.tenant, input.IdempotencyKey); got != 0 {
		t.Fatalf("jornada abortada persistiu mutation: %d", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM funeral.service_order WHERE tenant_id=$1 AND idempotency_key LIKE $2`, fixture.tenant, "funeral.journey:"+input.IdempotencyKey+":%"); got != 0 {
		t.Fatalf("jornada abortada persistiu OS: %d", got)
	}
}
