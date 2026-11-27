package funeral

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
	_ "time/tzdata"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/platform/auth"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AgendaItem struct {
	ID             uuid.UUID  `json:"id"`
	ContractID     uuid.UUID  `json:"contract_id"`
	ServiceOrderID *uuid.UUID `json:"service_order_id,omitempty"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	LocationID     uuid.UUID  `json:"location_id"`
	ResourceID     *uuid.UUID `json:"resource_id,omitempty"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         time.Time  `json:"ends_at"`
	TimeZone       string     `json:"time_zone"`
}

type AgendaReader interface {
	ListAgenda(ctx context.Context, startsAt, endsAt time.Time) ([]AgendaItem, error)
}

func (r *Repository) ListAgenda(ctx context.Context, startsAt, endsAt time.Time) ([]AgendaItem, error) {
	if !startsAt.Before(endsAt) {
		return nil, ErrScheduleInvalid
	}
	tenantID, companyID, err := scheduleScope(ctx, uuid.Nil)
	if err != nil {
		return nil, err
	}
	items := make([]AgendaItem, 0)
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id,contract_id,service_order_id,kind,status,location_id,resource_id,starts_at,ends_at,time_zone
			FROM funeral.schedule
			WHERE tenant_id=$1 AND company_id=$2 AND starts_at < $4 AND ends_at > $3
			ORDER BY starts_at,id`, tenantID, companyID, startsAt, endsAt)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgendaItem
			if err := rows.Scan(&item.ID, &item.ContractID, &item.ServiceOrderID, &item.Kind, &item.Status, &item.LocationID, &item.ResourceID, &item.StartsAt, &item.EndsAt, &item.TimeZone); err != nil {
				return err
			}
			item.StartsAt, item.EndsAt = item.StartsAt.UTC(), item.EndsAt.UTC()
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar agenda da empresa: %w", err)
	}
	return items, nil
}

func AgendaAuthenticatedRoute(service AgendaReader, checker AccessChecker) httpserver.AuthenticatedRoute {
	return httpserver.AuthenticatedRoute{
		Pattern:    "GET /v1/funeral/agenda",
		Permission: auth.PermissionFuneralRead,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			listAgenda(w, r, service, checker)
		}),
	}
}

type agendaWindow struct {
	Date     string    `json:"date"`
	EndDate  string    `json:"end_date"`
	TimeZone string    `json:"time_zone"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

type agendaPending struct {
	ScheduleID uuid.UUID `json:"schedule_id"`
	Kind       string    `json:"kind"`
}

func listAgenda(w http.ResponseWriter, r *http.Request, service AgendaReader, checker AccessChecker) {
	w.Header().Set("Cache-Control", "no-store")
	window, err := parseAgendaWindow(r.URL.RawQuery)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	switch err := checkFuneralAccess(r, checker); {
	case errors.Is(err, entitlement.ErrModuleDisabled):
		writeProductionError(w, r, http.StatusForbidden, "ENTITLEMENT_MODULE_NOT_LICENSED", "módulo funeral bloqueado")
		return
	case err != nil:
		writeProductionError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível")
		return
	}
	if service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "FUNERAL_UNAVAILABLE", "agenda indisponível")
		return
	}
	items, err := service.ListAgenda(r.Context(), window.StartsAt, window.EndsAt)
	if err != nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "FUNERAL_UNAVAILABLE", "agenda indisponível")
		return
	}
	pending := make([]agendaPending, 0)
	for _, item := range items {
		if item.Status == "scheduled" {
			pending = append(pending, agendaPending{ScheduleID: item.ID, Kind: "schedule_confirmation"})
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Window  agendaWindow    `json:"window"`
		Items   []AgendaItem    `json:"items"`
		Pending []agendaPending `json:"pending"`
	}{Window: window, Items: items, Pending: pending})
}

func parseAgendaWindow(rawQuery string) (agendaWindow, error) {
	const dateLayout = "2006-01-02"
	query, err := url.ParseQuery(rawQuery)
	if err != nil || !query.Has("date") || !query.Has("time_zone") {
		return agendaWindow{}, errors.New("informe date e time_zone")
	}
	for key, values := range query {
		if len(values) != 1 || (key != "date" && key != "end_date" && key != "time_zone") {
			return agendaWindow{}, errors.New("parâmetros da agenda inválidos")
		}
	}
	date, err := time.Parse(dateLayout, query.Get("date"))
	if err != nil || date.Year() < 1 {
		return agendaWindow{}, errors.New("date deve estar no formato AAAA-MM-DD")
	}
	end := date.AddDate(0, 0, 1)
	if query.Has("end_date") {
		end, err = time.Parse(dateLayout, query.Get("end_date"))
		if err != nil {
			return agendaWindow{}, errors.New("end_date deve estar no formato AAAA-MM-DD")
		}
	}
	if !end.After(date) || end.After(date.AddDate(0, 0, 31)) || end.Year() > 9999 {
		return agendaWindow{}, errors.New("o intervalo deve ter de 1 a 31 dias civis")
	}
	zone := query.Get("time_zone")
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return agendaWindow{}, errors.New("time_zone deve ser um fuso IANA")
	}
	startsAt, endsAt := civilDayStart(date, location), civilDayStart(end, location)
	if startsAt == nil || endsAt == nil || !startsAt.Before(*endsAt) {
		return agendaWindow{}, errors.New("limite de data civil inexistente no fuso informado")
	}
	return agendaWindow{
		Date: date.Format(dateLayout), EndDate: end.Format(dateLayout), TimeZone: zone,
		StartsAt: *startsAt, EndsAt: *endsAt,
	}, nil
}

// Intersect each offset period with the civil day. This finds the first instant
// even when midnight is repeated or skipped by an offset transition.
func civilDayStart(date time.Time, location *time.Location) *time.Time {
	nextDay := date.AddDate(0, 0, 1)
	for at, until := date.Add(-24*time.Hour), date.Add(24*time.Hour); !at.After(until); {
		local := at.In(location)
		_, offsetSeconds := local.Zone()
		_, periodEnd := local.ZoneBounds()
		offset := time.Duration(offsetSeconds) * time.Second
		first := date.Add(-offset)
		if at.After(first) {
			first = at
		}
		if first.Before(nextDay.Add(-offset)) && (periodEnd.IsZero() || first.Before(periodEnd)) {
			return &first
		}
		if periodEnd.IsZero() || !periodEnd.After(at) {
			break
		}
		at = periodEnd.UTC()
	}
	return nil
}
