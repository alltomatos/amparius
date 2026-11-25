package funeral

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type locationHTTPPayload struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	TimeZone string `json:"time_zone"`
}

type resourceHTTPPayload struct {
	LocationID string `json:"location_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
}

type scheduleCreateHTTPPayload struct {
	ContractID     string  `json:"contract_id"`
	ServiceOrderID *string `json:"service_order_id"`
	Kind           string  `json:"kind"`
	LocationID     string  `json:"location_id"`
	ResourceID     *string `json:"resource_id"`
	StartsAt       string  `json:"starts_at"`
	EndsAt         string  `json:"ends_at"`
	TimeZone       string  `json:"time_zone"`
}

type schedulePatchHTTPPayload struct {
	LocationID string          `json:"location_id"`
	ResourceID json.RawMessage `json:"resource_id"`
	StartsAt   *string         `json:"starts_at"`
	EndsAt     *string         `json:"ends_at"`
	TimeZone   *string         `json:"time_zone"`
}

func serveSchedulingRoute(w http.ResponseWriter, r *http.Request, service SchedulingService) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/_dev/funeral/locations":
		serveLocationsRoute(w, r, service)
	case path == "/_dev/funeral/resources":
		serveResourcesRoute(w, r, service)
	case path == "/_dev/funeral/schedules":
		serveSchedulesRoute(w, r, service)
	case strings.HasPrefix(path, "/_dev/funeral/contracts/") && strings.HasSuffix(path, "/schedules"):
		contractText := strings.TrimSuffix(strings.TrimPrefix(path, "/_dev/funeral/contracts/"), "/schedules")
		contractID, err := parseUUID(contractText)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_CONTRACT_ID", "id do contrato invalido")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
			return
		}
		schedules, err := service.ListSchedules(r.Context(), contractID)
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "schedules": scheduleResponses(schedules)})
	case strings.HasPrefix(path, "/_dev/funeral/schedules/"):
		serveScheduleByIDRoute(w, r, service, path)
	default:
		writeError(w, http.StatusNotFound, "FUNERAL_ROUTE_NOT_FOUND", "rota nao encontrada")
	}
}

func serveLocationsRoute(w http.ResponseWriter, r *http.Request, service SchedulingService) {
	switch r.Method {
	case http.MethodGet:
		locations, err := service.ListLocations(r.Context())
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		response := make([]map[string]any, 0, len(locations))
		for _, location := range locations {
			response = append(response, locationResponse(location))
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "locations": response})
	case http.MethodPost:
		key, ok := scheduleIdempotencyKey(w, r)
		if !ok {
			return
		}
		payload, err := decodeScheduleBody[locationHTTPPayload](w, r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_JSON", "JSON invalido")
			return
		}
		location, err := service.CreateLocation(r.Context(), LocationCreate{Name: strings.TrimSpace(payload.Name), Kind: strings.TrimSpace(payload.Kind), TimeZone: strings.TrimSpace(payload.TimeZone), IdempotencyKey: key})
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		w.Header().Set("Location", "/_dev/funeral/locations/"+location.ID.String())
		writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "location": locationResponse(location)})
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
	}
}

func serveResourcesRoute(w http.ResponseWriter, r *http.Request, service SchedulingService) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
		return
	}
	if r.Method == http.MethodGet {
		locationID, err := parseUUID(r.URL.Query().Get("location_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_LOCATION_ID", "id do local invalido")
			return
		}
		resources, err := service.ListResources(r.Context(), locationID)
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		response := make([]map[string]any, 0, len(resources))
		for _, resource := range resources {
			response = append(response, resourceResponse(resource))
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "resources": response})
		return
	}
	key, ok := scheduleIdempotencyKey(w, r)
	if !ok {
		return
	}
	payload, err := decodeScheduleBody[resourceHTTPPayload](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "JSON invalido")
		return
	}
	locationID, err := parseUUID(payload.LocationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_LOCATION_ID", "id do local invalido")
		return
	}
	resource, err := service.CreateResource(r.Context(), ResourceCreate{LocationID: locationID, Name: strings.TrimSpace(payload.Name), Kind: strings.TrimSpace(payload.Kind), IdempotencyKey: key})
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	w.Header().Set("Location", "/_dev/funeral/resources/"+resource.ID.String())
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "resource": resourceResponse(resource)})
}

func serveSchedulesRoute(w http.ResponseWriter, r *http.Request, service SchedulingService) {
	switch r.Method {
	case http.MethodGet:
		contractID, err := parseUUID(r.URL.Query().Get("contract_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_CONTRACT_ID", "id do contrato invalido")
			return
		}
		schedules, err := service.ListSchedules(r.Context(), contractID)
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "schedules": scheduleResponses(schedules)})
	case http.MethodPost:
		createScheduleHTTP(w, r, service)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
	}
}

func createScheduleHTTP(w http.ResponseWriter, r *http.Request, service SchedulingService) {
	key, ok := scheduleIdempotencyKey(w, r)
	if !ok {
		return
	}
	payload, err := decodeScheduleBody[scheduleCreateHTTPPayload](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "JSON invalido")
		return
	}
	contractID, err := parseUUID(payload.ContractID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_CONTRACT_ID", "id do contrato invalido")
		return
	}
	locationID, err := parseUUID(payload.LocationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_LOCATION_ID", "id do local invalido")
		return
	}
	startsAt, err := parseScheduleTime(payload.StartsAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SCHEDULE", "intervalo da agenda invalido")
		return
	}
	endsAt, err := parseScheduleTime(payload.EndsAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SCHEDULE", "intervalo da agenda invalido")
		return
	}
	input := ScheduleCreate{ContractID: contractID, Kind: strings.TrimSpace(payload.Kind), LocationID: locationID, StartsAt: startsAt, EndsAt: endsAt, TimeZone: strings.TrimSpace(payload.TimeZone), IdempotencyKey: key}
	if payload.ServiceOrderID != nil {
		id, parseErr := parseUUID(*payload.ServiceOrderID)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "INVALID_SERVICE_ORDER_ID", "id da ordem de servico invalido")
			return
		}
		input.ServiceOrderID = &id
	}
	if payload.ResourceID != nil {
		id, parseErr := parseUUID(*payload.ResourceID)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "INVALID_RESOURCE_ID", "id do recurso invalido")
			return
		}
		input.ResourceID = &id
	}
	schedule, err := service.CreateSchedule(r.Context(), input)
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	w.Header().Set("Location", "/_dev/funeral/schedules/"+schedule.ID.String())
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "schedule": scheduleResponse(schedule)})
}

func serveScheduleByIDRoute(w http.ResponseWriter, r *http.Request, service SchedulingService, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/_dev/funeral/schedules/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 || (len(parts) == 2 && parts[1] != "cancel") {
		writeError(w, http.StatusNotFound, "FUNERAL_ROUTE_NOT_FOUND", "rota nao encontrada")
		return
	}
	id, err := parseUUID(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SCHEDULE_ID", "id da agenda invalido")
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
			return
		}
		key, ok := scheduleIdempotencyKey(w, r)
		if !ok {
			return
		}
		schedule, err := service.CancelSchedule(r.Context(), id, key)
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "schedule": scheduleResponse(schedule)})
		return
	}
	switch r.Method {
	case http.MethodGet:
		schedule, err := service.GetScheduleByID(r.Context(), id)
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "schedule": scheduleResponse(schedule)})
	case http.MethodPatch:
		key, ok := scheduleIdempotencyKey(w, r)
		if !ok {
			return
		}
		payload, err := decodeScheduleBody[schedulePatchHTTPPayload](w, r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_JSON", "JSON invalido")
			return
		}
		patch := SchedulePatch{ID: id, IdempotencyKey: key, TimeZone: payload.TimeZone, StartsAt: parseOptionalScheduleTime(w, payload.StartsAt), EndsAt: parseOptionalScheduleTime(w, payload.EndsAt)}
		if (payload.StartsAt != nil && patch.StartsAt == nil) || (payload.EndsAt != nil && patch.EndsAt == nil) {
			return
		}
		if payload.LocationID != "" {
			locationID, parseErr := parseUUID(payload.LocationID)
			if parseErr != nil {
				writeError(w, http.StatusBadRequest, "INVALID_LOCATION_ID", "id do local invalido")
				return
			}
			patch.LocationID = &locationID
		}
		if len(payload.ResourceID) > 0 {
			if string(payload.ResourceID) == "null" {
				patch.ClearResource = true
			} else {
				var resourceText string
				if json.Unmarshal(payload.ResourceID, &resourceText) != nil {
					writeError(w, http.StatusBadRequest, "INVALID_RESOURCE_ID", "id do recurso invalido")
					return
				}
				resourceID, parseErr := parseUUID(resourceText)
				if parseErr != nil {
					writeError(w, http.StatusBadRequest, "INVALID_RESOURCE_ID", "id do recurso invalido")
					return
				}
				patch.ResourceID = &resourceID
			}
		}
		schedule, err := service.UpdateSchedule(r.Context(), patch)
		if err != nil {
			writeScheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "schedule": scheduleResponse(schedule)})
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
	}
}

func scheduleIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 200 {
		writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key invalida")
		return "", false
	}
	return key, true
}

func parseScheduleTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseOptionalScheduleTime(w http.ResponseWriter, value *string) *time.Time {
	if value == nil {
		return nil
	}
	parsed, err := parseScheduleTime(*value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SCHEDULE", "intervalo da agenda invalido")
		return nil
	}
	return &parsed
}

func decodeScheduleBody[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var value T
	r.Body = http.MaxBytesReader(w, r.Body, maxContractBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return value, errors.New("corpo invalido")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return value, errors.New("mais de um documento JSON")
	}
	return value, nil
}

func locationResponse(location Location) map[string]any {
	return map[string]any{"id": location.ID.String(), "company_id": location.CompanyID.String(), "name": location.Name, "kind": location.Kind, "time_zone": location.TimeZone, "created_at": location.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": location.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func resourceResponse(resource Resource) map[string]any {
	return map[string]any{"id": resource.ID.String(), "company_id": resource.CompanyID.String(), "location_id": resource.LocationID.String(), "name": resource.Name, "kind": resource.Kind, "active": resource.Active, "created_at": resource.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": resource.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func scheduleResponse(schedule Schedule) map[string]any {
	history := make([]map[string]any, 0, len(schedule.StatusHistory))
	for _, item := range schedule.StatusHistory {
		history = append(history, map[string]any{"id": item.ID.String(), "from_status": item.FromStatus, "to_status": item.ToStatus, "reason": item.Reason, "changed_at": item.ChangedAt.UTC().Format(time.RFC3339Nano)})
	}
	return map[string]any{"id": schedule.ID.String(), "company_id": schedule.CompanyID.String(), "contract_id": schedule.ContractID.String(), "service_order_id": optionalUUIDText(schedule.ServiceOrderID), "kind": schedule.Kind, "location_id": schedule.LocationID.String(), "resource_id": optionalUUIDText(schedule.ResourceID), "starts_at": schedule.StartsAt.UTC().Format(time.RFC3339Nano), "ends_at": schedule.EndsAt.UTC().Format(time.RFC3339Nano), "time_zone": schedule.TimeZone, "status": schedule.Status, "idempotency_key": schedule.IdempotencyKey, "version": schedule.Version, "created_at": schedule.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": schedule.UpdatedAt.UTC().Format(time.RFC3339Nano), "status_history": history}
}

func scheduleResponses(items []Schedule) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, scheduleResponse(item))
	}
	return result
}

func writeScheduleError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "nao foi possivel processar a agenda"
	switch {
	case errors.Is(err, ErrLocationNotFound), errors.Is(err, ErrResourceNotFound), errors.Is(err, ErrScheduleNotFound), errors.Is(err, ErrContractNotFound):
		status, code, message = http.StatusNotFound, "SCHEDULE_REFERENCE_NOT_FOUND", "referencia da agenda nao encontrada"
	case errors.Is(err, ErrScheduleConflict):
		status, code, message = http.StatusConflict, "SCHEDULE_CONFLICT", "intervalo da agenda indisponivel"
	case errors.Is(err, ErrScheduleIdempotency):
		status, code, message = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key ja utilizada com dados diferentes"
	case errors.Is(err, ErrScheduleInvalid), errors.Is(err, ErrScheduleTimeZone), errors.Is(err, ErrScheduleTransition), errors.Is(err, ErrScheduleContractState):
		status, code, message = http.StatusBadRequest, "INVALID_SCHEDULE", "dados da agenda invalidos"
	case errors.Is(err, ErrOutboxUnavailable):
		status, code, message = http.StatusServiceUnavailable, "OUTBOX_UNAVAILABLE", "outbox indisponivel"
	}
	writeError(w, status, code, message)
}
