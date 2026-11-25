package funeral

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateScheduleRequiresExplicitIANAZoneAndHalfOpenInterval(t *testing.T) {
	base := time.Date(2026, 11, 25, 15, 0, 0, 0, time.FixedZone("-03", -3*60*60))
	valid := ScheduleCreate{CompanyID: uuid.New(), ContractID: uuid.New(), LocationID: uuid.New(), Kind: ScheduleKindWake, StartsAt: base, EndsAt: base.Add(2 * time.Hour), TimeZone: "America/Fortaleza", IdempotencyKey: "schedule-1"}
	if err := validateScheduleCreate(valid); err != nil {
		t.Fatalf("intervalo valido: %v", err)
	}
	for name, input := range map[string]ScheduleCreate{
		"sem timezone":  func() ScheduleCreate { v := valid; v.TimeZone = ""; return v }(),
		"timezone fixa": func() ScheduleCreate { v := valid; v.TimeZone = "-03"; return v }(),
		"fim igual":     func() ScheduleCreate { v := valid; v.EndsAt = v.StartsAt; return v }(),
		"fim anterior":  func() ScheduleCreate { v := valid; v.EndsAt = v.StartsAt.Add(-time.Minute); return v }(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateScheduleCreate(input); err == nil {
				t.Fatal("esperava rejeicao")
			}
		})
	}
}

func TestScheduleKindsAreStable(t *testing.T) {
	if ScheduleKindWake != "wake" || ScheduleKindBurial != "burial" {
		t.Fatalf("kinds alterados: %q/%q", ScheduleKindWake, ScheduleKindBurial)
	}
}
