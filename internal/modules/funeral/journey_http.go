package funeral

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

type funeralJourneyHTTPPayload struct {
	ServiceOrder funeralJourneyServiceOrderHTTPPayload `json:"service_order"`
	Schedule     funeralJourneyScheduleHTTPPayload     `json:"schedule"`
}

type funeralJourneyServiceOrderHTTPPayload struct {
	Kind         string  `json:"kind"`
	Description  string  `json:"description"`
	ScheduledFor *string `json:"scheduled_for"`
}

type funeralJourneyScheduleHTTPPayload struct {
	Kind       string  `json:"kind"`
	LocationID string  `json:"location_id"`
	ResourceID *string `json:"resource_id"`
	StartsAt   string  `json:"starts_at"`
	EndsAt     string  `json:"ends_at"`
	TimeZone   string  `json:"time_zone"`
}

func serveFuneralJourneyRoute(w http.ResponseWriter, r *http.Request, service JourneyService, contractID uuid.UUID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 160 {
		writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key invalida")
		return
	}
	payload, err := decodeContractBody[funeralJourneyHTTPPayload](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "JSON invalido")
		return
	}
	locationID, err := parseUUID(payload.Schedule.LocationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_LOCATION_ID", "id do local invalido")
		return
	}
	startsAt, err := parseScheduleTime(payload.Schedule.StartsAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JOURNEY", "dados da jornada invalidos")
		return
	}
	endsAt, err := parseScheduleTime(payload.Schedule.EndsAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JOURNEY", "dados da jornada invalidos")
		return
	}
	input := FuneralJourneyCreate{
		ContractID:              contractID,
		ServiceOrderKind:        strings.TrimSpace(payload.ServiceOrder.Kind),
		ServiceOrderDescription: payload.ServiceOrder.Description,
		ScheduleKind:            strings.TrimSpace(payload.Schedule.Kind),
		LocationID:              locationID,
		StartsAt:                startsAt,
		EndsAt:                  endsAt,
		TimeZone:                strings.TrimSpace(payload.Schedule.TimeZone),
		IdempotencyKey:          key,
	}
	if payload.ServiceOrder.ScheduledFor != nil {
		value, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(*payload.ServiceOrder.ScheduledFor))
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "INVALID_JOURNEY", "dados da jornada invalidos")
			return
		}
		value = value.UTC()
		input.ServiceOrderScheduledFor = &value
	}
	if payload.Schedule.ResourceID != nil {
		resourceID, parseErr := parseUUID(*payload.Schedule.ResourceID)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "INVALID_RESOURCE_ID", "id do recurso invalido")
			return
		}
		input.ResourceID = &resourceID
	}
	journey, err := service.CreateFuneralJourney(r.Context(), input)
	if err != nil {
		writeFuneralJourneyError(w, err)
		return
	}
	w.Header().Set("Location", "/_dev/funeral/contracts/"+contractID.String()+"/journey")
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "journey": funeralJourneyResponse(journey)})
}

func funeralJourneyResponse(journey FuneralJourney) map[string]any {
	return map[string]any{
		"id":            journey.ID.String(),
		"company_id":    journey.CompanyID.String(),
		"contract_id":   journey.ContractID.String(),
		"service_order": serviceOrderResponse(journey.ServiceOrder),
		"schedule":      scheduleResponse(journey.Schedule),
		"created_at":    journey.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func writeFuneralJourneyError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "nao foi possivel criar a jornada funeraria"
	switch {
	case errors.Is(err, ErrContractNotFound), errors.Is(err, ErrJourneyLocation), errors.Is(err, ErrJourneyResource):
		status, code, message = http.StatusNotFound, "JOURNEY_REFERENCE_NOT_FOUND", "referencia da jornada nao encontrada"
	case errors.Is(err, ErrJourneyInvalid), errors.Is(err, ErrJourneyContractState), errors.Is(err, db.ErrTenantRequired), errors.Is(err, db.ErrCompanyRequired), errors.Is(err, ErrTenantMismatch):
		status, code, message = http.StatusBadRequest, "INVALID_JOURNEY", "dados da jornada invalidos"
	case errors.Is(err, ErrJourneyIdempotency):
		status, code, message = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key ja utilizada com dados diferentes"
	case errors.Is(err, ErrScheduleConflict):
		status, code, message = http.StatusConflict, "SCHEDULE_CONFLICT", "intervalo da agenda indisponivel"
	case errors.Is(err, ErrOutboxUnavailable):
		status, code, message = http.StatusServiceUnavailable, "OUTBOX_UNAVAILABLE", "outbox indisponivel"
	}
	writeError(w, status, code, message)
}
