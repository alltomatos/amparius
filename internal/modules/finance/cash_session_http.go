package finance

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/google/uuid"
)

func (h productionHandler) openCashSession(w http.ResponseWriter, r *http.Request) {
	key, userID, ok := cashSessionWriteScope(w, r)
	if !ok {
		return
	}
	payload, err := decodeFinanceBody[struct {
		Amount json.RawMessage `json:"opening_amount_cents"`
	}](w, r)
	amount, amountErr := financeAmount(payload.Amount, false)
	if err != nil || amountErr != nil {
		writeFinanceProductionServiceError(w, r, ErrInvalidHTTPFinanceRequest)
		return
	}
	item, err := h.service.OpenCashSession(r.Context(), key, CashSession{UserID: userID, OpeningAmountCents: amount})
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	writeFinanceProductionJSON(w, http.StatusCreated, cashSessionProductionResponse(item))
}

func (h productionHandler) closeCashSession(w http.ResponseWriter, r *http.Request) {
	key, userID, ok := cashSessionWriteScope(w, r)
	if !ok {
		return
	}
	id, ok := cashSessionPathID(w, r)
	if !ok {
		return
	}
	payload, err := decodeFinanceBody[struct {
		Amount json.RawMessage `json:"counted_amount_cents"`
		Note   *string         `json:"note"`
	}](w, r)
	amount, amountErr := financeAmount(payload.Amount, false)
	if err != nil || amountErr != nil {
		writeFinanceProductionServiceError(w, r, ErrInvalidHTTPFinanceRequest)
		return
	}
	item, err := h.service.CloseCashSession(r.Context(), id, key, CashClosure{
		CountedCents: amount, UserID: userID, Note: optionalFinanceString(payload.Note),
	})
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	writeFinanceProductionJSON(w, http.StatusOK, cashSessionProductionResponse(item))
}

func (h productionHandler) listCashSessions(w http.ResponseWriter, r *http.Request) {
	filter, err := parseCashSessionFilter(r.URL.RawQuery)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	items, next, err := h.service.ListCashSessionPage(r.Context(), filter)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	response := make([]map[string]any, 0, len(items))
	for _, item := range items {
		response = append(response, cashSessionProductionResponse(item))
	}
	page := map[string]any{"items": response}
	if next != nil {
		page["next_cursor"] = base64.RawURLEncoding.EncodeToString([]byte(next.OpenedAt.UTC().Format(time.RFC3339Nano) + "/" + next.ID.String()))
	}
	writeFinanceProductionJSON(w, http.StatusOK, page)
}

func parseCashSessionFilter(rawQuery string) (CashSessionFilter, error) {
	var filter CashSessionFilter
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return filter, ErrInvalidHTTPFinanceRequest
	}
	for key, values := range query {
		if len(values) != 1 {
			return filter, ErrInvalidHTTPFinanceRequest
		}
		raw := values[0]
		switch key {
		case "limit":
			if raw = strings.TrimSpace(raw); raw != "" {
				filter.Limit, err = strconv.Atoi(raw)
				if err != nil || filter.Limit <= 0 {
					return filter, ErrInvalidHTTPFinanceRequest
				}
			}
		case "status":
			if raw != "open" && raw != "closed" {
				return filter, ErrInvalidHTTPFinanceRequest
			}
			filter.Status = raw
		case "cursor":
			if len(raw) > base64.RawURLEncoding.EncodedLen(len(time.RFC3339Nano)+1+36) {
				return filter, ErrInvalidHTTPFinanceRequest
			}
			data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
			stamp, rawID, ok := strings.Cut(string(data), "/")
			openedAt, timeErr := time.Parse(time.RFC3339Nano, stamp)
			id, idErr := uuid.Parse(rawID)
			if err != nil || !ok || timeErr != nil || idErr != nil || id == uuid.Nil {
				return filter, ErrInvalidHTTPFinanceRequest
			}
			filter.After = &CashSessionPosition{OpenedAt: openedAt.UTC(), ID: id}
		default:
			return filter, ErrInvalidHTTPFinanceRequest
		}
	}
	return filter, nil
}

func (h productionHandler) getCashReconciliation(w http.ResponseWriter, r *http.Request) {
	id, ok := cashSessionPathID(w, r)
	if !ok {
		return
	}
	totals, err := h.service.GetCashReconciliation(r.Context(), id)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	response := cashSessionProductionResponse(totals.Session)
	response["expected_amount_cents"] = totals.ExpectedCents
	response["movement_in_cents"] = totals.MovementInCents
	response["movement_out_cents"] = totals.MovementOutCents
	response["payments_by_method_cents"] = totals.PaymentByMethod
	writeFinanceProductionJSON(w, http.StatusOK, response)
}

func (h productionHandler) recordCashMovement(w http.ResponseWriter, r *http.Request) {
	key, _, ok := cashSessionWriteScope(w, r)
	if !ok {
		return
	}
	payload, err := decodeFinanceBody[struct {
		SessionID   *string         `json:"session_id"`
		Kind        *string         `json:"kind"`
		AmountCents json.RawMessage `json:"amount_cents"`
		Reason      *string         `json:"reason"`
	}](w, r)
	if err != nil {
		writeFinanceProductionServiceError(w, r, ErrInvalidHTTPFinanceRequest)
		return
	}
	item, err := cashMovementFromPayload(cashMovementPayload{
		SessionID: payload.SessionID, Kind: payload.Kind, AmountCents: payload.AmountCents, Reason: payload.Reason,
	})
	if err != nil || (item.Kind != "in" && item.Kind != "out") {
		writeFinanceProductionServiceError(w, r, ErrInvalidHTTPFinanceRequest)
		return
	}
	created, err := h.service.RecordCashMovement(r.Context(), key, item)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	writeFinanceProductionJSON(w, http.StatusCreated, cashMovementResponse(created))
}

func (h productionHandler) listCashMovements(w http.ResponseWriter, r *http.Request) {
	id, ok := cashSessionPathID(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			writeFinanceProductionServiceError(w, r, ErrInvalidHTTPFinanceRequest)
			return
		}
	}
	if _, err := h.service.GetCashSessionByID(r.Context(), id); err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	items, err := h.service.ListCashMovements(r.Context(), id, limit)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	response := make([]map[string]any, 0, len(items))
	for _, item := range items {
		response = append(response, cashMovementResponse(item))
	}
	writeFinanceProductionJSON(w, http.StatusOK, map[string]any{"items": response})
}

func cashSessionWriteScope(w http.ResponseWriter, r *http.Request) (string, uuid.UUID, bool) {
	principal, err := auth.PrincipalFromContext(r.Context())
	if err != nil || principal.UserID == uuid.Nil {
		writeFinanceProductionError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "usuário autenticado obrigatório")
		return "", uuid.Nil, false
	}
	key, err := idempotencyKey(r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return "", uuid.Nil, false
	}
	return key, principal.UserID, true
}

func cashSessionPathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		writeFinanceProductionServiceError(w, r, ErrInvalidHTTPFinanceRequest)
		return uuid.Nil, false
	}
	return id, true
}

func cashSessionProductionResponse(item CashSession) map[string]any {
	response := cashSessionResponse(item)
	response["expected_amount_cents"] = item.ExpectedAmountCents
	response["counted_amount_cents"] = item.CountedAmountCents
	response["difference_amount_cents"] = item.DifferenceAmountCents
	response["closed_by"] = optionalID(item.ClosedBy)
	response["note"] = optionalString(item.CloseNote)
	return response
}
