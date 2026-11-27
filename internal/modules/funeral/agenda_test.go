package funeral

import (
	"testing"
	"time"
)

func TestAgendaCivilWindow(t *testing.T) {
	for _, tc := range []struct {
		name, query, start string
		duration           time.Duration
	}{
		{"Fortaleza", "date=2026-11-26&time_zone=America/Fortaleza", "2026-11-26T03:00:00Z", 24 * time.Hour},
		{"intervalo", "date=2026-11-26&end_date=2026-11-28&time_zone=America/Fortaleza", "2026-11-26T03:00:00Z", 48 * time.Hour},
		{"meia-noite suprimida", "date=2018-11-04&time_zone=America/Sao_Paulo", "2018-11-04T03:00:00Z", 23 * time.Hour},
		{"dia de 25 horas", "date=2018-02-17&time_zone=America/Sao_Paulo", "2018-02-17T02:00:00Z", 25 * time.Hour},
		{"meia-noite repetida", "date=2020-11-01&time_zone=America/Havana", "2020-11-01T04:00:00Z", 25 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			window, err := parseAgendaWindow(tc.query)
			if err != nil || window.StartsAt.Format(time.RFC3339) != tc.start || window.EndsAt.Sub(window.StartsAt) != tc.duration {
				t.Fatalf("janela=%+v erro=%v", window, err)
			}
		})
	}
	for _, query := range []string{
		"date=2026-11-26", "date=2026-11-26&time_zone=Local", "date=2026-11-26&time_zone=Inexistente",
		"date=2026-02-30&time_zone=UTC", "date=2026-11-26&end_date=2026-11-26&time_zone=UTC",
		"date=2026-11-26&end_date=2026-12-28&time_zone=UTC", "date=2011-12-30&time_zone=Pacific/Apia",
		"date=2026-11-26&time_zone=UTC&time_zone=UTC", "date=2026-11-26&time_zone=UTC&company_id=externa",
	} {
		if _, err := parseAgendaWindow(query); err == nil {
			t.Errorf("janela inválida aceita: %s", query)
		}
	}
}
