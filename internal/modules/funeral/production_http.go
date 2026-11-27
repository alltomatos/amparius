package funeral

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/auth"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

type productionHandler struct{ service JourneyService }

// AuthenticatedRoutes expõe a jornada funeral online (contrato → OS → agenda)
// em rotas autenticadas. O escopo de tenant/empresa vem do middleware, nunca do
// corpo ou de cabeçalhos. A atomicidade, a idempotência e a publicação dos
// eventos são do repositório (CreateFuneralJourney) e do outbox.
func AuthenticatedRoutes(service JourneyService) []httpserver.AuthenticatedRoute {
	h := productionHandler{service: service}
	return []httpserver.AuthenticatedRoute{
		{Pattern: "POST /v1/funeral/contracts/{contractId}/journey", Permission: auth.PermissionFuneralWrite, Handler: h.available(h.createJourney)},
	}
}

func (h productionHandler) available(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if h.service == nil {
			writeProductionError(w, r, http.StatusServiceUnavailable, "FUNERAL_UNAVAILABLE", "funerária indisponível")
			return
		}
		next(w, r)
	})
}

func (h productionHandler) createJourney(w http.ResponseWriter, r *http.Request) {
	contractID, err := uuid.Parse(strings.TrimSpace(r.PathValue("contractId")))
	if err != nil || contractID == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "INVALID_CONTRACT_ID", "id do contrato inválido")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 160 {
		writeProductionError(w, r, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key inválida")
		return
	}
	payload, err := decodeContractBody[funeralJourneyHTTPPayload](w, r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "INVALID_JSON", "JSON inválido")
		return
	}
	locationID, err := parseUUID(payload.Schedule.LocationID)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "INVALID_LOCATION_ID", "id do local inválido")
		return
	}
	startsAt, err := parseScheduleTime(payload.Schedule.StartsAt)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "INVALID_JOURNEY", "dados da jornada inválidos")
		return
	}
	endsAt, err := parseScheduleTime(payload.Schedule.EndsAt)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "INVALID_JOURNEY", "dados da jornada inválidos")
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
			writeProductionError(w, r, http.StatusBadRequest, "INVALID_JOURNEY", "dados da jornada inválidos")
			return
		}
		value = value.UTC()
		input.ServiceOrderScheduledFor = &value
	}
	if payload.Schedule.ResourceID != nil {
		resourceID, parseErr := parseUUID(*payload.Schedule.ResourceID)
		if parseErr != nil {
			writeProductionError(w, r, http.StatusBadRequest, "INVALID_RESOURCE_ID", "id do recurso inválido")
			return
		}
		input.ResourceID = &resourceID
	}
	journey, err := h.service.CreateFuneralJourney(r.Context(), input)
	if err != nil {
		writeFuneralJourneyProductionError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/funeral/contracts/"+contractID.String()+"/journey")
	writeJSON(w, http.StatusCreated, funeralJourneyResponse(journey))
}

func writeFuneralJourneyProductionError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "não foi possível criar a jornada funerária"
	switch {
	case errors.Is(err, ErrContractNotFound), errors.Is(err, ErrJourneyLocation), errors.Is(err, ErrJourneyResource):
		status, code, message = http.StatusNotFound, "JOURNEY_REFERENCE_NOT_FOUND", "referência da jornada não encontrada"
	case errors.Is(err, ErrJourneyInvalid), errors.Is(err, ErrJourneyContractState):
		status, code, message = http.StatusBadRequest, "INVALID_JOURNEY", "dados da jornada inválidos"
	case errors.Is(err, ErrJourneyIdempotency):
		status, code, message = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key já utilizada com dados diferentes"
	case errors.Is(err, ErrScheduleConflict):
		status, code, message = http.StatusConflict, "SCHEDULE_CONFLICT", "intervalo da agenda indisponível"
	case errors.Is(err, ErrOutboxUnavailable):
		status, code, message = http.StatusServiceUnavailable, "OUTBOX_UNAVAILABLE", "outbox indisponível"
	}
	writeProductionError(w, r, status, code, message)
}

func writeProductionError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"code":    code,
		"message": message,
		"details": map[string]any{},
		"traceId": observability.TraceIDFromContext(r.Context()),
	})
}
