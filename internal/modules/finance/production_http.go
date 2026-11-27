package finance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/auth"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// FinanceOperationalService é o contrato autenticado do financeiro usado pela
// jornada online da #76: título e baixa idempotente.
type FinanceOperationalService interface {
	CreateTitle(context.Context, Title) (Title, error)
	GetTitleByID(context.Context, uuid.UUID) (Title, error)
	ListTitlePage(context.Context, TitleListFilter) ([]Title, *TitleListPosition, error)
	CreatePaymentIdempotent(context.Context, string, Payment) (Payment, error)
}

type productionHandler struct{ service FinanceOperationalService }

// AuthenticatedRoutes monta as rotas financeiras com o escopo de tenant do
// middleware e as permissões finance:read/write. Repetir uma chave de
// idempotência com o mesmo corpo devolve o mesmo registro; com corpo diferente
// devolve 409 IDEMPOTENCY_CONFLICT.
func AuthenticatedRoutes(service FinanceOperationalService) []httpserver.AuthenticatedRoute {
	h := productionHandler{service: service}
	return []httpserver.AuthenticatedRoute{
		{Pattern: "POST /v1/finance/titles", Permission: auth.PermissionFinanceWrite, Handler: h.available(h.createTitle)},
		{Pattern: "GET /v1/finance/titles", Permission: auth.PermissionFinanceRead, Handler: h.available(h.listTitles)},
		{Pattern: "GET /v1/finance/titles/{id}", Permission: auth.PermissionFinanceRead, Handler: h.available(h.getTitle)},
		{Pattern: "POST /v1/finance/payments", Permission: auth.PermissionFinanceWrite, Handler: h.available(h.createPayment)},
	}
}

func (h productionHandler) available(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if h.service == nil {
			writeFinanceProductionError(w, r, http.StatusServiceUnavailable, "FINANCE_UNAVAILABLE", "financeiro indisponível")
			return
		}
		next(w, r)
	})
}

func (h productionHandler) createTitle(w http.ResponseWriter, r *http.Request) {
	payload, err := decodeFinanceBody[titlePayload](w, r)
	if err != nil {
		writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "título inválido")
		return
	}
	item, err := titleFromPayload(payload)
	if err != nil {
		writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "título inválido")
		return
	}
	created, err := h.service.CreateTitle(r.Context(), item)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	writeFinanceProductionJSON(w, http.StatusCreated, titleResponse(created))
}

func (h productionHandler) listTitles(w http.ResponseWriter, r *http.Request) {
	filter, err := parseTitleListFilter(r.URL.RawQuery)
	if err != nil {
		writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	items, next, err := h.service.ListTitlePage(r.Context(), filter)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	response := make([]map[string]any, 0, len(items))
	for _, item := range items {
		response = append(response, titleResponse(item))
	}
	payload := map[string]any{"items": response}
	if next != nil {
		payload["next_cursor"] = next.encode()
	}
	writeFinanceProductionJSON(w, http.StatusOK, payload)
}

func (h productionHandler) getTitle(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id == uuid.Nil {
		writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "id de título inválido")
		return
	}
	item, err := h.service.GetTitleByID(r.Context(), id)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	writeFinanceProductionJSON(w, http.StatusOK, titleResponse(item))
}

func (h productionHandler) createPayment(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 200 {
		writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Idempotency-Key inválida")
		return
	}
	payload, err := decodeFinanceBody[paymentPayload](w, r)
	if err != nil {
		writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "pagamento inválido")
		return
	}
	item, err := paymentFromPayload(payload)
	if err != nil {
		writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "pagamento inválido")
		return
	}
	created, err := h.service.CreatePaymentIdempotent(r.Context(), key, item)
	if err != nil {
		writeFinanceProductionServiceError(w, r, err)
		return
	}
	writeFinanceProductionJSON(w, http.StatusCreated, paymentResponse(created))
}

func writeFinanceProductionServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeFinanceProductionError(w, r, http.StatusNotFound, "FINANCE_REFERENCE_NOT_FOUND", "referência financeira não encontrada neste tenant")
	case errors.Is(err, ErrIdempotencyConflict):
		writeFinanceProductionError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "chave de idempotência reutilizada com conteúdo diferente")
	case errors.Is(err, ErrInvalidAmount), errors.Is(err, ErrInvalidIdempotencyKey), errors.Is(err, ErrTenantMismatch), errors.Is(err, ErrInvalidHTTPFinanceRequest):
		writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "dados financeiros inválidos")
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				writeFinanceProductionError(w, r, http.StatusNotFound, "FINANCE_REFERENCE_NOT_FOUND", "referência financeira não encontrada neste tenant")
				return
			case "23505":
				writeFinanceProductionError(w, r, http.StatusConflict, "FINANCE_CONFLICT", "registro financeiro já existe")
				return
			case "23514":
				writeFinanceProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "dados financeiros inválidos")
				return
			case "P0001":
				writeFinanceProductionError(w, r, http.StatusConflict, "FINANCE_CONFLICT", "operação financeira rejeitada")
				return
			}
		}
		writeFinanceProductionError(w, r, http.StatusServiceUnavailable, "FINANCE_UNAVAILABLE", "financeiro indisponível")
	}
}

func writeFinanceProductionError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeFinanceProductionJSON(w, status, map[string]any{
		"code":    code,
		"message": message,
		"details": map[string]any{},
		"traceId": observability.TraceIDFromContext(r.Context()),
	})
}

func writeFinanceProductionJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
