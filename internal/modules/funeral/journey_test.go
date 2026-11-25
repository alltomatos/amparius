package funeral

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateFuneralJourneyRequiresActiveContractAndExplicitSchedule(t *testing.T) {
	base := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)
	valid := FuneralJourneyCreate{
		ContractID: uuid.New(), ServiceOrderKind: "wake", ServiceOrderDescription: "operacao",
		ScheduleKind: ScheduleKindWake, LocationID: uuid.New(), StartsAt: base,
		EndsAt: base.Add(2 * time.Hour), TimeZone: "America/Fortaleza", IdempotencyKey: "journey-1",
	}
	if err := validateFuneralJourney(valid); err != nil {
		t.Fatalf("jornada valida: %v", err)
	}
	for name, input := range map[string]FuneralJourneyCreate{
		"sem contrato":                func() FuneralJourneyCreate { v := valid; v.ContractID = uuid.Nil; return v }(),
		"tipo de agenda desconhecido": func() FuneralJourneyCreate { v := valid; v.ScheduleKind = "unknown"; return v }(),
		"timezone invalido":           func() FuneralJourneyCreate { v := valid; v.TimeZone = "-03"; return v }(),
		"fim antes do inicio":         func() FuneralJourneyCreate { v := valid; v.EndsAt = v.StartsAt; return v }(),
		"chave ausente":               func() FuneralJourneyCreate { v := valid; v.IdempotencyKey = ""; return v }(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateFuneralJourney(input); err != ErrJourneyInvalid {
				t.Fatalf("erro = %v, esperado ErrJourneyInvalid", err)
			}
		})
	}
}

func TestFuneralJourneyHashChangesWithOpaqueReferences(t *testing.T) {
	input := FuneralJourneyCreate{
		ContractID: uuid.New(), ServiceOrderKind: "burial", ScheduleKind: ScheduleKindBurial,
		LocationID: uuid.New(), StartsAt: time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC),
		EndsAt: time.Date(2026, 11, 25, 16, 0, 0, 0, time.UTC), TimeZone: "America/Fortaleza", IdempotencyKey: "journey-2",
	}
	first, err := funeralJourneyHash(input)
	if err != nil {
		t.Fatal(err)
	}
	input.LocationID = uuid.New()
	second, err := funeralJourneyHash(input)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("referencia opaca alterada deveria alterar o request hash")
	}
}
