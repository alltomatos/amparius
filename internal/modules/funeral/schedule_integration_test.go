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

	cancelled, err := repository.CancelSchedule(fixture.ctx(), schedule.ID, "schedule-cancel")
	if err != nil || cancelled.Status != funeral.ScheduleCancelled || len(cancelled.StatusHistory) != 2 {
		t.Fatalf("cancelamento = %#v/%v", cancelled, err)
	}
	cancelReplay, err := repository.CancelSchedule(fixture.ctx(), schedule.ID, "schedule-cancel")
	if err != nil || cancelReplay.Version != cancelled.Version {
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
	if err := fixture.database.WithTenantTx(fixture.ctx(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM funeral.schedule_status_history WHERE tenant_id=$1 AND schedule_id=$2`, fixture.tenant, schedule.ID)
		return err
	}); err == nil {
		t.Fatal("historico de agenda deveria ser append-only")
	}
	if got := fixture.count(t, `SELECT count(*) FROM funeral.schedule_status_history WHERE tenant_id=$1`, fixture.tenant); got != 4 {
		t.Fatalf("historico da agenda = %d, esperado 4", got)
	}
}
