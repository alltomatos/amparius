package funeral

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type serviceOrderCreatePayload struct {
	Kind         string  `json:"kind"`
	Description  string  `json:"description"`
	ScheduledFor *string `json:"scheduled_for"`
}

type serviceOrderPatchPayload struct {
	Description  *string         `json:"description"`
	Status       string          `json:"status"`
	ScheduledFor json.RawMessage `json:"scheduled_for"`
}

func serveServiceOrderRoute(w http.ResponseWriter, r *http.Request, service ServiceOrderService) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasPrefix(path, "/_dev/funeral/contracts/") && strings.HasSuffix(path, "/service-orders") {
		contractText := strings.TrimSuffix(strings.TrimPrefix(path, "/_dev/funeral/contracts/"), "/service-orders")
		contractID, err := parseUUID(contractText)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_CONTRACT_ID", "id do contrato invalido")
			return
		}
		switch r.Method {
		case http.MethodPost:
			createServiceOrderHTTP(w, r, service, contractID)
		case http.MethodGet:
			orders, err := service.ListServiceOrders(r.Context(), contractID)
			if err != nil {
				writeServiceOrderError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service_orders": serviceOrderResponses(orders)})
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
		}
		return
	}

	prefix := "/_dev/funeral/service-orders/"
	if !strings.HasPrefix(path, prefix) {
		writeError(w, http.StatusNotFound, "FUNERAL_ROUTE_NOT_FOUND", "rota nao encontrada")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 || (len(parts) == 2 && parts[1] != "complete" && parts[1] != "status") {
		writeError(w, http.StatusNotFound, "FUNERAL_ROUTE_NOT_FOUND", "rota nao encontrada")
		return
	}
	id, err := parseUUID(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SERVICE_ORDER_ID", "id da ordem de servico invalido")
		return
	}
	if len(parts) == 2 {
		if parts[1] == "status" {
			if r.Method != http.MethodPatch {
				w.Header().Set("Allow", http.MethodPatch)
				writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
				return
			}
			key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if key == "" {
				writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key invalida")
				return
			}
			payload, err := decodeServiceOrderBody[serviceOrderPatchPayload](w, r)
			if err != nil || payload.Status == "" {
				writeError(w, http.StatusBadRequest, "INVALID_SERVICE_ORDER", "dados da ordem de servico invalidos")
				return
			}
			order, err := service.UpdateServiceOrder(r.Context(), ServiceOrderPatch{ID: id, Status: strings.TrimSpace(payload.Status), IdempotencyKey: key})
			if err != nil {
				writeServiceOrderError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service_order": serviceOrderResponse(order)})
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
			return
		}
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" {
			writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key invalida")
			return
		}
		order, err := service.CompleteServiceOrder(r.Context(), id, key)
		if err != nil {
			writeServiceOrderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service_order": serviceOrderResponse(order)})
		return
	}
	switch r.Method {
	case http.MethodGet:
		order, err := service.GetServiceOrderByID(r.Context(), id)
		if err != nil {
			writeServiceOrderError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service_order": serviceOrderResponse(order)})
	case http.MethodPatch:
		updateServiceOrderHTTP(w, r, service, id)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "metodo nao permitido")
	}
}

func createServiceOrderHTTP(w http.ResponseWriter, r *http.Request, service ServiceOrderService, contractID uuid.UUID) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 160 {
		writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key invalida")
		return
	}
	payload, err := decodeServiceOrderBody[serviceOrderCreatePayload](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "JSON invalido")
		return
	}
	input := ServiceOrderCreate{ContractID: contractID, Kind: strings.TrimSpace(payload.Kind), Description: payload.Description, IdempotencyKey: key}
	if payload.ScheduledFor != nil {
		parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(*payload.ScheduledFor))
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "INVALID_SERVICE_ORDER", "dados da ordem de servico invalidos")
			return
		}
		parsed = parsed.UTC()
		input.ScheduledFor = &parsed
	}
	order, err := service.CreateServiceOrder(r.Context(), input)
	if err != nil {
		writeServiceOrderError(w, err)
		return
	}
	w.Header().Set("Location", "/_dev/funeral/service-orders/"+order.ID.String())
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "service_order": serviceOrderResponse(order)})
}

func updateServiceOrderHTTP(w http.ResponseWriter, r *http.Request, service ServiceOrderService, id uuid.UUID) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 160 {
		writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key invalida")
		return
	}
	payload, err := decodeServiceOrderBody[serviceOrderPatchPayload](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "JSON invalido")
		return
	}
	patch := ServiceOrderPatch{ID: id, Description: payload.Description, Status: strings.TrimSpace(payload.Status), IdempotencyKey: key}
	if payload.ScheduledFor != nil {
		patch.ScheduledForSet = true
		if string(payload.ScheduledFor) == "null" {
			patch.ClearScheduledFor = true
		} else {
			var raw string
			if json.Unmarshal(payload.ScheduledFor, &raw) != nil {
				writeError(w, http.StatusBadRequest, "INVALID_SERVICE_ORDER", "dados da ordem de servico invalidos")
				return
			}
			parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(raw))
			if parseErr != nil {
				writeError(w, http.StatusBadRequest, "INVALID_SERVICE_ORDER", "dados da ordem de servico invalidos")
				return
			}
			parsed = parsed.UTC()
			patch.ScheduledFor = &parsed
		}
	}
	order, err := service.UpdateServiceOrder(r.Context(), patch)
	if err != nil {
		writeServiceOrderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service_order": serviceOrderResponse(order)})
}

func serviceOrderResponse(order ServiceOrder) map[string]any {
	history := make([]map[string]any, 0, len(order.StatusHistory))
	for _, item := range order.StatusHistory {
		history = append(history, map[string]any{"id": item.ID.String(), "from_status": item.FromStatus, "to_status": item.ToStatus, "reason": item.Reason, "changed_at": item.ChangedAt.UTC().Format(time.RFC3339Nano)})
	}
	response := map[string]any{"id": order.ID.String(), "company_id": order.CompanyID.String(), "contract_id": order.ContractID.String(), "kind": order.Kind, "description": order.Description, "status": order.Status, "idempotency_key": order.IdempotencyKey, "version": order.Version, "created_at": order.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": order.UpdatedAt.UTC().Format(time.RFC3339Nano), "status_history": history}
	if order.ScheduledFor != nil {
		response["scheduled_for"] = order.ScheduledFor.UTC().Format(time.RFC3339Nano)
	} else {
		response["scheduled_for"] = nil
	}
	if order.CompletedAt != nil {
		response["completed_at"] = order.CompletedAt.UTC().Format(time.RFC3339Nano)
	} else {
		response["completed_at"] = nil
	}
	return response
}

func serviceOrderResponses(orders []ServiceOrder) []map[string]any {
	result := make([]map[string]any, 0, len(orders))
	for _, order := range orders {
		result = append(result, serviceOrderResponse(order))
	}
	return result
}

func decodeServiceOrderBody[T any](w http.ResponseWriter, r *http.Request) (T, error) {
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
func parseUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("uuid invalido")
	}
	return id, nil
}

func writeServiceOrderError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "nao foi possivel processar a ordem de servico"
	switch {
	case errors.Is(err, ErrServiceOrderNotFound), errors.Is(err, ErrContractNotFound):
		status, code, message = http.StatusNotFound, "SERVICE_ORDER_REFERENCE_NOT_FOUND", "referencia da ordem de servico nao encontrada"
	case errors.Is(err, ErrServiceOrderInvalid), errors.Is(err, ErrServiceOrderStatus), errors.Is(err, ErrServiceOrderTransition), errors.Is(err, ErrContractNotActive):
		status, code, message = http.StatusBadRequest, "INVALID_SERVICE_ORDER", "dados da ordem de servico invalidos"
	case errors.Is(err, ErrServiceOrderIdempotency):
		status, code, message = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key ja utilizada com dados diferentes"
	case errors.Is(err, ErrOutboxUnavailable):
		status, code, message = http.StatusServiceUnavailable, "OUTBOX_UNAVAILABLE", "outbox indisponivel"
	}
	writeError(w, status, code, message)
}
