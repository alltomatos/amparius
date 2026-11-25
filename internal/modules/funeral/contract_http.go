package funeral

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

const maxContractBodyBytes = 32 << 10

type contractCreatePayload struct {
	CustomerPartyID string                      `json:"customer_party_id"`
	DeceasedID      string                      `json:"deceased_id"`
	FamilyLinkIDs   []string                    `json:"family_link_ids"`
	Items           []contractItemCreatePayload `json:"items"`
	PaymentPlan     *paymentPlanPayload         `json:"payment_plan"`
}

type paymentPlanPayload struct {
	InstallmentCount *int    `json:"installment_count"`
	FirstDueDate     *string `json:"first_due_date"`
	IntervalMonths   *int    `json:"interval_months"`
}

type contractItemCreatePayload struct {
	CatalogItemID   string `json:"catalog_item_id"`
	Quantity        *int   `json:"quantity"`
	UnitAmountCents *int64 `json:"unit_amount_cents"`
}

type contractStatusPayload struct {
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

func serveContracts(w http.ResponseWriter, r *http.Request, service ContractService) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/_dev/funeral/contracts" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
			return
		}
		createContractHTTP(w, r, service)
		return
	}
	prefix := "/_dev/funeral/contracts/"
	if !strings.HasPrefix(path, prefix) {
		writeError(w, http.StatusNotFound, "FUNERAL_ROUTE_NOT_FOUND", "rota não encontrada")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 || (len(parts) == 2 && parts[1] != "status") {
		writeError(w, http.StatusNotFound, "FUNERAL_ROUTE_NOT_FOUND", "rota não encontrada")
		return
	}
	id, err := uuid.Parse(parts[0])
	if err != nil || id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "INVALID_CONTRACT_ID", "id do contrato inválido")
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodPatch {
			w.Header().Set("Allow", http.MethodPatch)
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
			return
		}
		changeContractStatusHTTP(w, r, service, id)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	contract, err := service.GetContractByID(r.Context(), id)
	if err != nil {
		writeContractServiceError(w, err, "consultar contrato")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "contract": contractResponse(contract)})
}

func createContractHTTP(w http.ResponseWriter, r *http.Request, service ContractService) {
	companyID, err := db.CompanyFromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "COMPANY_REQUIRED", "empresa ausente no contexto")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 160 {
		writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key inválida")
		return
	}
	payload, err := decodeContractBody[contractCreatePayload](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "JSON inválido")
		return
	}
	input, err := contractCreateFromPayload(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_CONTRACT", "dados do contrato inválidos")
		return
	}
	input.CompanyID = companyID
	input.IdempotencyKey = key
	contract, err := service.CreateContract(r.Context(), input)
	if err != nil {
		writeContractServiceError(w, err, "criar contrato")
		return
	}
	w.Header().Set("Location", "/_dev/funeral/contracts/"+contract.ID.String())
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "contract": contractResponse(contract)})
}

func changeContractStatusHTTP(w http.ResponseWriter, r *http.Request, service ContractService, id uuid.UUID) {
	payload, err := decodeContractBody[contractStatusPayload](w, r)
	if err != nil || strings.TrimSpace(payload.Status) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_CONTRACT_STATUS", "status do contrato inválido")
		return
	}
	contract, err := service.ChangeContractStatus(r.Context(), id, payload.Status, payload.Reason)
	if err != nil {
		writeContractServiceError(w, err, "alterar status do contrato")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "contract": contractResponse(contract)})
}

func contractCreateFromPayload(payload contractCreatePayload) (ContractCreate, error) {
	customerID, err := uuid.Parse(strings.TrimSpace(payload.CustomerPartyID))
	if err != nil || customerID == uuid.Nil {
		return ContractCreate{}, ErrInvalidContract
	}
	deceasedID, err := uuid.Parse(strings.TrimSpace(payload.DeceasedID))
	if err != nil || deceasedID == uuid.Nil {
		return ContractCreate{}, ErrInvalidContract
	}
	input := ContractCreate{CustomerPartyID: customerID, DeceasedID: deceasedID}
	for _, raw := range payload.FamilyLinkIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || id == uuid.Nil {
			return ContractCreate{}, ErrInvalidContract
		}
		input.FamilyLinkIDs = append(input.FamilyLinkIDs, id)
	}
	for _, raw := range payload.Items {
		id, err := uuid.Parse(strings.TrimSpace(raw.CatalogItemID))
		if err != nil || id == uuid.Nil || raw.Quantity == nil || raw.UnitAmountCents == nil {
			return ContractCreate{}, ErrInvalidContract
		}
		input.Items = append(input.Items, ContractItemCreate{CatalogItemID: id, Quantity: *raw.Quantity, UnitAmountCents: *raw.UnitAmountCents})
	}
	if payload.PaymentPlan != nil {
		if payload.PaymentPlan.InstallmentCount == nil || payload.PaymentPlan.FirstDueDate == nil || payload.PaymentPlan.IntervalMonths == nil {
			return ContractCreate{}, ErrInvalidContract
		}
		firstDueDate, err := time.Parse("2006-01-02", strings.TrimSpace(*payload.PaymentPlan.FirstDueDate))
		if err != nil {
			return ContractCreate{}, ErrInvalidContract
		}
		input.PaymentPlan = &ContractPaymentPlan{
			InstallmentCount: *payload.PaymentPlan.InstallmentCount,
			FirstDueDate:     firstDueDate.UTC(),
			IntervalMonths:   *payload.PaymentPlan.IntervalMonths,
		}
	}
	return input, nil
}

func decodeContractBody[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var value T
	reader := io.LimitReader(r.Body, maxContractBodyBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil || len(data) > maxContractBodyBytes || len(bytes.TrimSpace(data)) == 0 {
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

func contractResponse(contract Contract) map[string]any {
	items := make([]map[string]any, 0, len(contract.Items))
	for _, item := range contract.Items {
		items = append(items, map[string]any{
			"id": item.ID.String(), "catalog_item_id": item.CatalogItemID.String(),
			"item_kind": item.ItemKind, "description": item.Description, "unit": item.Unit,
			"quantity": item.Quantity, "unit_amount_cents": item.UnitAmountCents,
			"total_amount_cents": item.TotalAmountCents,
		})
	}
	installments := make([]map[string]any, 0, len(contract.Installments))
	for _, installment := range contract.Installments {
		installments = append(installments, map[string]any{
			"id": installment.ID.String(), "number": installment.Number,
			"amount_cents": installment.AmountCents,
			"due_date":     installment.DueDate.Format("2006-01-02"), "status": installment.Status,
		})
	}
	history := make([]map[string]any, 0, len(contract.StatusHistory))
	for _, change := range contract.StatusHistory {
		history = append(history, map[string]any{
			"id": change.ID.String(), "from_status": change.FromStatus, "to_status": change.ToStatus,
			"reason": change.Reason, "changed_at": change.ChangedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		})
	}
	family := make([]string, 0, len(contract.FamilyLinkIDs))
	for _, id := range contract.FamilyLinkIDs {
		family = append(family, id.String())
	}
	return map[string]any{
		"id": contract.ID.String(), "company_id": contract.CompanyID.String(),
		"customer_party_id": contract.CustomerPartyID.String(), "deceased_id": contract.DeceasedID.String(),
		"status": contract.Status, "total_amount_cents": contract.TotalAmountCents,
		"idempotency_key": contract.IdempotencyKey, "family_link_ids": family,
		"items": items, "installments": installments, "status_history": history,
	}
}

func writeContractServiceError(w http.ResponseWriter, err error, action string) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "não foi possível "+action
	switch {
	case errors.Is(err, ErrContractNotFound), errors.Is(err, ErrCustomerNotFound), errors.Is(err, ErrDeceasedNotFound), errors.Is(err, ErrFamilyLinkNotFound), errors.Is(err, ErrCatalogItemNotFound):
		status, code, message = http.StatusNotFound, "CONTRACT_REFERENCE_NOT_FOUND", "referência do contrato não encontrada"
	case errors.Is(err, ErrInvalidContract), errors.Is(err, ErrInvalidContractStatus), errors.Is(err, ErrInvalidStatusTransition), errors.Is(err, db.ErrTenantRequired), errors.Is(err, db.ErrCompanyRequired), errors.Is(err, ErrTenantMismatch):
		status, code, message = http.StatusBadRequest, "INVALID_CONTRACT", "dados do contrato inválidos"
	case errors.Is(err, ErrIdempotencyConflict):
		status, code, message = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key já utilizada com dados diferentes"
	case errors.Is(err, ErrOutboxUnavailable):
		status, code, message = http.StatusServiceUnavailable, "OUTBOX_UNAVAILABLE", "outbox indisponível"
	}
	writeError(w, status, code, message)
}

// compile-time assertion documents that Repository is the HTTP service.
var _ ContractService = (*Repository)(nil)
