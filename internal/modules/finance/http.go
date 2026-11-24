package finance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	developmentFinancePrefix = "/_dev/finance"
	developmentTenantHeader  = "X-Amparius-Development-Tenant-ID"
	maxFinanceBodyBytes      = 16 << 10
)

var ErrInvalidHTTPFinanceRequest = errors.New("requisição financeira inválida")

// DevelopmentService é a superfície mínima usada pela API financeira dev-only.
// O contexto recebido já contém o tenant derivado do cabeçalho HTTP.
type DevelopmentService interface {
	CreateAccount(context.Context, Account) (Account, error)
	GetAccountByID(context.Context, uuid.UUID) (Account, error)
	ListAccounts(context.Context, int) ([]Account, error)
	CreateTitle(context.Context, Title) (Title, error)
	GetTitleByID(context.Context, uuid.UUID) (Title, error)
	ListTitles(context.Context, int) ([]Title, error)
	CreateInstallment(context.Context, Installment) (Installment, error)
	GetInstallmentByID(context.Context, uuid.UUID) (Installment, error)
	ListInstallments(context.Context, uuid.UUID, int) ([]Installment, error)
	CreatePaymentIdempotent(context.Context, string, Payment) (Payment, error)
	CreateCashSession(context.Context, CashSession) (CashSession, error)
	GetCashSessionByID(context.Context, uuid.UUID) (CashSession, error)
	CreateCashMovementIdempotent(context.Context, string, CashMovement) (CashMovement, error)
	ListCashMovements(context.Context, uuid.UUID, int) ([]CashMovement, error)
	CreateLedgerEntry(context.Context, LedgerEntry) (LedgerEntry, error)
	ListLedgerEntries(context.Context, uuid.UUID, int) ([]LedgerEntry, error)
}

type accountPayload struct {
	ParentID *string `json:"parent_id"`
	Code     *string `json:"code"`
	Name     *string `json:"name"`
	Type     *string `json:"type"`
	Status   *string `json:"status"`
}

type titlePayload struct {
	Direction   *string         `json:"direction"`
	PartyID     *string         `json:"party_id"`
	AmountCents json.RawMessage `json:"amount_cents"`
	DueDate     *string         `json:"due_date"`
	Status      *string         `json:"status"`
	Origin      *string         `json:"origin"`
	Description *string         `json:"description"`
}

type installmentPayload struct {
	TitleID     *string         `json:"title_id"`
	Number      *int            `json:"number"`
	AmountCents json.RawMessage `json:"amount_cents"`
	DueDate     *string         `json:"due_date"`
	Status      *string         `json:"status"`
}

type paymentPayload struct {
	InstallmentID *string         `json:"installment_id"`
	BankAccountID *string         `json:"bank_account_id"`
	AmountCents   json.RawMessage `json:"amount_cents"`
	PaidAt        *string         `json:"paid_at"`
	Method        *string         `json:"method"`
	Reference     *string         `json:"reference"`
}

type cashSessionPayload struct {
	UserID             *string         `json:"user_id"`
	OpeningAmountCents json.RawMessage `json:"opening_amount_cents"`
	Status             *string         `json:"status"`
}

type cashMovementPayload struct {
	SessionID   *string         `json:"session_id"`
	Kind        *string         `json:"kind"`
	AmountCents json.RawMessage `json:"amount_cents"`
	Reason      *string         `json:"reason"`
	OccurredAt  *string         `json:"occurred_at"`
}

type ledgerEntryPayload struct {
	AccountID   *string         `json:"account_id"`
	Competence  *string         `json:"competence"`
	DebitCents  json.RawMessage `json:"debit_cents"`
	CreditCents json.RawMessage `json:"credit_cents"`
	Source      *string         `json:"source"`
	SourceID    *string         `json:"source_id"`
}

// NewDevelopmentHTTPHandler cria as rotas financeiras de desenvolvimento.
// Elas só são registradas pelo servidor quando APP_ENV=development e
// ENABLE_DEV_ONBOARDING=true. O tenant vem exclusivamente do cabeçalho
// X-Amparius-Development-Tenant-ID.
func NewDevelopmentHTTPHandler(service DevelopmentService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")

		tenantID, err := developmentFinanceTenantID(r)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, "cabeçalho "+developmentTenantHeader+" ausente ou inválido")
			return
		}
		if service == nil {
			writeFinanceError(w, http.StatusServiceUnavailable, "financeiro indisponível")
			return
		}
		ctx := db.WithTenant(r.Context(), tenantID)
		path := r.URL.Path

		switch {
		case path == developmentFinancePrefix+"/accounts":
			handleAccounts(w, r, service, ctx, tenantID)
		case strings.HasPrefix(path, developmentFinancePrefix+"/accounts/"):
			handleAccount(w, r, service, ctx, tenantID, r.PathValue("id"))
		case path == developmentFinancePrefix+"/titles":
			handleTitles(w, r, service, ctx, tenantID)
		case strings.HasSuffix(path, "/installments") && strings.HasPrefix(path, developmentFinancePrefix+"/titles/"):
			handleTitleInstallments(w, r, service, ctx, tenantID, r.PathValue("id"))
		case strings.HasPrefix(path, developmentFinancePrefix+"/titles/"):
			handleTitle(w, r, service, ctx, tenantID, r.PathValue("id"))
		case path == developmentFinancePrefix+"/installments":
			handleInstallments(w, r, service, ctx, tenantID)
		case strings.HasPrefix(path, developmentFinancePrefix+"/installments/"):
			handleInstallment(w, r, service, ctx, tenantID, r.PathValue("id"))
		case path == developmentFinancePrefix+"/payments":
			handlePayments(w, r, service, ctx, tenantID)
		case path == developmentFinancePrefix+"/cash-sessions":
			handleCashSessions(w, r, service, ctx, tenantID)
		case strings.HasPrefix(path, developmentFinancePrefix+"/cash-sessions/"):
			handleCashSession(w, r, service, ctx, tenantID, r.PathValue("id"))
		case path == developmentFinancePrefix+"/cash-movements":
			handleCashMovements(w, r, service, ctx, tenantID)
		case path == developmentFinancePrefix+"/ledger-entries":
			handleLedgerEntries(w, r, service, ctx, tenantID)
		default:
			writeFinanceError(w, http.StatusNotFound, "rota não encontrada")
		}
	})
}

func handleAccounts(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, tenantID uuid.UUID) {
	switch r.Method {
	case http.MethodPost:
		payload, err := decodeFinanceBody[accountPayload](w, r)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, "JSON inválido")
			return
		}
		account, err := accountFromPayload(payload)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, err.Error())
			return
		}
		created, err := service.CreateAccount(ctx, account)
		if err != nil {
			writeFinanceServiceError(w, err, "criar conta")
			return
		}
		w.Header().Set("Location", developmentFinancePrefix+"/accounts/"+created.ID.String())
		writeFinanceJSON(w, http.StatusCreated, map[string]any{"status": "ok", "account": accountResponse(created)})
	case http.MethodGet:
		limit, ok := financeListLimit(w, r)
		if !ok {
			return
		}
		items, err := service.ListAccounts(ctx, limit)
		if err != nil {
			writeFinanceServiceError(w, err, "listar contas")
			return
		}
		response := make([]any, 0, len(items))
		for _, item := range items {
			response = append(response, accountResponse(item))
		}
		writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "accounts": response})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func handleAccount(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID, rawID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	id, ok := parseFinanceID(w, rawID, "conta")
	if !ok {
		return
	}
	item, err := service.GetAccountByID(ctx, id)
	if err != nil {
		writeFinanceServiceError(w, err, "consultar conta")
		return
	}
	writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "account": accountResponse(item)})
}

func handleTitles(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, tenantID uuid.UUID) {
	switch r.Method {
	case http.MethodPost:
		payload, err := decodeFinanceBody[titlePayload](w, r)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, "JSON inválido")
			return
		}
		item, err := titleFromPayload(payload)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, err.Error())
			return
		}
		created, err := service.CreateTitle(ctx, item)
		if err != nil {
			writeFinanceServiceError(w, err, "criar título")
			return
		}
		w.Header().Set("Location", developmentFinancePrefix+"/titles/"+created.ID.String())
		writeFinanceJSON(w, http.StatusCreated, map[string]any{"status": "ok", "title": titleResponse(created)})
	case http.MethodGet:
		limit, ok := financeListLimit(w, r)
		if !ok {
			return
		}
		items, err := service.ListTitles(ctx, limit)
		if err != nil {
			writeFinanceServiceError(w, err, "listar títulos")
			return
		}
		response := make([]any, 0, len(items))
		for _, item := range items {
			response = append(response, titleResponse(item))
		}
		writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "titles": response})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func handleTitle(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID, rawID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	id, ok := parseFinanceID(w, rawID, "título")
	if !ok {
		return
	}
	item, err := service.GetTitleByID(ctx, id)
	if err != nil {
		writeFinanceServiceError(w, err, "consultar título")
		return
	}
	writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "title": titleResponse(item)})
}

func handleTitleInstallments(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID, rawID string) {
	titleID, ok := parseFinanceID(w, rawID, "título")
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		payload, err := decodeFinanceBody[installmentPayload](w, r)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, "JSON inválido")
			return
		}
		item, err := installmentFromPayload(payload)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, err.Error())
			return
		}
		if item.TitleID != uuid.Nil && item.TitleID != titleID {
			writeFinanceError(w, http.StatusBadRequest, "title_id diverge da rota")
			return
		}
		item.TitleID = titleID
		created, err := service.CreateInstallment(ctx, item)
		if err != nil {
			writeFinanceServiceError(w, err, "criar parcela")
			return
		}
		w.Header().Set("Location", developmentFinancePrefix+"/installments/"+created.ID.String())
		writeFinanceJSON(w, http.StatusCreated, map[string]any{"status": "ok", "installment": installmentResponse(created)})
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
		return
	}
	limit, ok := financeListLimit(w, r)
	if !ok {
		return
	}
	items, err := service.ListInstallments(ctx, titleID, limit)
	if err != nil {
		writeFinanceServiceError(w, err, "listar parcelas")
		return
	}
	response := make([]any, 0, len(items))
	for _, item := range items {
		response = append(response, installmentResponse(item))
	}
	writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "installments": response})
}

func handleInstallments(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID) {
	switch r.Method {
	case http.MethodPost:
		payload, err := decodeFinanceBody[installmentPayload](w, r)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, "JSON inválido")
			return
		}
		item, err := installmentFromPayload(payload)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, err.Error())
			return
		}
		if item.TitleID == uuid.Nil {
			writeFinanceError(w, http.StatusBadRequest, "title_id é obrigatório")
			return
		}
		created, err := service.CreateInstallment(ctx, item)
		if err != nil {
			writeFinanceServiceError(w, err, "criar parcela")
			return
		}
		w.Header().Set("Location", developmentFinancePrefix+"/installments/"+created.ID.String())
		writeFinanceJSON(w, http.StatusCreated, map[string]any{"status": "ok", "installment": installmentResponse(created)})
	case http.MethodGet:
		var titleID uuid.UUID
		if raw := strings.TrimSpace(r.URL.Query().Get("title_id")); raw != "" {
			var err error
			titleID, err = uuid.Parse(raw)
			if err != nil || titleID == uuid.Nil {
				writeFinanceError(w, http.StatusBadRequest, "title_id inválido")
				return
			}
		}
		limit, ok := financeListLimit(w, r)
		if !ok {
			return
		}
		items, err := service.ListInstallments(ctx, titleID, limit)
		if err != nil {
			writeFinanceServiceError(w, err, "listar parcelas")
			return
		}
		response := make([]any, 0, len(items))
		for _, item := range items {
			response = append(response, installmentResponse(item))
		}
		writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "installments": response})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func handleInstallment(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID, rawID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	id, ok := parseFinanceID(w, rawID, "parcela")
	if !ok {
		return
	}
	item, err := service.GetInstallmentByID(ctx, id)
	if err != nil {
		writeFinanceServiceError(w, err, "consultar parcela")
		return
	}
	writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "installment": installmentResponse(item)})
}

func handlePayments(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 200 {
		writeFinanceError(w, http.StatusBadRequest, "Idempotency-Key inválida")
		return
	}
	payload, err := decodeFinanceBody[paymentPayload](w, r)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	item, err := paymentFromPayload(payload)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := service.CreatePaymentIdempotent(ctx, key, item)
	if err != nil {
		writeFinanceServiceError(w, err, "registrar baixa")
		return
	}
	writeFinanceJSON(w, http.StatusCreated, map[string]any{"status": "ok", "payment": paymentResponse(created)})
}

func handleCashSessions(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	payload, err := decodeFinanceBody[cashSessionPayload](w, r)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	item, err := cashSessionFromPayload(payload)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := service.CreateCashSession(ctx, item)
	if err != nil {
		writeFinanceServiceError(w, err, "abrir caixa")
		return
	}
	w.Header().Set("Location", developmentFinancePrefix+"/cash-sessions/"+created.ID.String())
	writeFinanceJSON(w, http.StatusCreated, map[string]any{"status": "ok", "cash_session": cashSessionResponse(created)})
}

func handleCashSession(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID, rawID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	id, ok := parseFinanceID(w, rawID, "sessão de caixa")
	if !ok {
		return
	}
	item, err := service.GetCashSessionByID(ctx, id)
	if err != nil {
		writeFinanceServiceError(w, err, "consultar sessão de caixa")
		return
	}
	writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "cash_session": cashSessionResponse(item)})
}

func handleCashMovements(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID) {
	if r.Method == http.MethodGet {
		sessionID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("session_id")))
		if err != nil || sessionID == uuid.Nil {
			writeFinanceError(w, http.StatusBadRequest, "session_id é obrigatório e deve ser UUID válido")
			return
		}
		limit, ok := financeListLimit(w, r)
		if !ok {
			return
		}
		items, err := service.ListCashMovements(ctx, sessionID, limit)
		if err != nil {
			writeFinanceServiceError(w, err, "listar movimentos de caixa")
			return
		}
		response := make([]any, 0, len(items))
		for _, item := range items {
			response = append(response, cashMovementResponse(item))
		}
		writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "cash_movements": response})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 200 {
		writeFinanceError(w, http.StatusBadRequest, "Idempotency-Key inválida")
		return
	}
	payload, err := decodeFinanceBody[cashMovementPayload](w, r)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	item, err := cashMovementFromPayload(payload)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := service.CreateCashMovementIdempotent(ctx, key, item)
	if err != nil {
		writeFinanceServiceError(w, err, "registrar movimento de caixa")
		return
	}
	writeFinanceJSON(w, http.StatusCreated, map[string]any{"status": "ok", "cash_movement": cashMovementResponse(created)})
}

func handleLedgerEntries(w http.ResponseWriter, r *http.Request, service DevelopmentService, ctx context.Context, _ uuid.UUID) {
	if r.Method == http.MethodGet {
		var accountID uuid.UUID
		if raw := strings.TrimSpace(r.URL.Query().Get("account_id")); raw != "" {
			var err error
			accountID, err = uuid.Parse(raw)
			if err != nil || accountID == uuid.Nil {
				writeFinanceError(w, http.StatusBadRequest, "account_id inválido")
				return
			}
		}
		limit, ok := financeListLimit(w, r)
		if !ok {
			return
		}
		items, err := service.ListLedgerEntries(ctx, accountID, limit)
		if err != nil {
			writeFinanceServiceError(w, err, "listar lançamentos")
			return
		}
		response := make([]any, 0, len(items))
		for _, item := range items {
			response = append(response, ledgerEntryResponse(item))
		}
		writeFinanceJSON(w, http.StatusOK, map[string]any{"status": "ok", "ledger_entries": response})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
		return
	}
	payload, err := decodeFinanceBody[ledgerEntryPayload](w, r)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	item, err := ledgerEntryFromPayload(payload)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := service.CreateLedgerEntry(ctx, item)
	if err != nil {
		writeFinanceServiceError(w, err, "criar lançamento")
		return
	}
	writeFinanceJSON(w, http.StatusCreated, map[string]any{"status": "ok", "ledger_entry": ledgerEntryResponse(created)})
}

func accountFromPayload(p accountPayload) (Account, error) {
	parentID, err := optionalFinanceUUID("parent_id", p.ParentID)
	if err != nil {
		return Account{}, err
	}
	code, err := requiredFinanceString("code", p.Code)
	if err != nil {
		return Account{}, err
	}
	name, err := requiredFinanceString("name", p.Name)
	if err != nil {
		return Account{}, err
	}
	typ, err := requiredFinanceString("type", p.Type)
	if err != nil {
		return Account{}, err
	}
	status, err := requiredFinanceString("status", p.Status)
	if err != nil {
		return Account{}, err
	}
	return Account{ParentID: parentID, Code: code, Name: name, Type: typ, Status: status}, nil
}

func titleFromPayload(p titlePayload) (Title, error) {
	partyID, err := optionalFinanceUUID("party_id", p.PartyID)
	if err != nil {
		return Title{}, err
	}
	amount, err := financeAmount(p.AmountCents, false)
	if err != nil {
		return Title{}, err
	}
	dueDate, err := financeDate("due_date", p.DueDate, true)
	if err != nil {
		return Title{}, err
	}
	direction, err := requiredFinanceString("direction", p.Direction)
	if err != nil {
		return Title{}, err
	}
	status, err := requiredFinanceString("status", p.Status)
	if err != nil {
		return Title{}, err
	}
	origin, err := requiredFinanceString("origin", p.Origin)
	if err != nil {
		return Title{}, err
	}
	return Title{PartyID: partyID, Direction: direction, AmountCents: amount, DueDate: dueDate, Status: status, Origin: origin, Description: optionalFinanceString(p.Description)}, nil
}

func installmentFromPayload(p installmentPayload) (Installment, error) {
	titleID, err := optionalFinanceUUID("title_id", p.TitleID)
	if err != nil {
		return Installment{}, err
	}
	if p.Number == nil || *p.Number <= 0 {
		return Installment{}, fmt.Errorf("%w: number deve ser positivo", ErrInvalidHTTPFinanceRequest)
	}
	amount, err := financeAmount(p.AmountCents, false)
	if err != nil {
		return Installment{}, err
	}
	dueDate, err := financeDate("due_date", p.DueDate, true)
	if err != nil {
		return Installment{}, err
	}
	status, err := requiredFinanceString("status", p.Status)
	if err != nil {
		return Installment{}, err
	}
	return Installment{TitleID: titleID, Number: *p.Number, AmountCents: amount, DueDate: dueDate, Status: status}, nil
}

func paymentFromPayload(p paymentPayload) (Payment, error) {
	installmentID, err := optionalFinanceUUID("installment_id", p.InstallmentID)
	if err != nil {
		return Payment{}, err
	}
	if installmentID == uuid.Nil {
		return Payment{}, fmt.Errorf("%w: installment_id é obrigatório", ErrInvalidHTTPFinanceRequest)
	}
	bankAccountID, err := optionalFinanceUUID("bank_account_id", p.BankAccountID)
	if err != nil {
		return Payment{}, err
	}
	amount, err := financeAmount(p.AmountCents, true)
	if err != nil {
		return Payment{}, err
	}
	paidAt, err := financeTime("paid_at", p.PaidAt, false)
	if err != nil {
		return Payment{}, err
	}
	method, err := requiredFinanceString("method", p.Method)
	if err != nil {
		return Payment{}, err
	}
	return Payment{InstallmentID: installmentID, BankAccountID: bankAccountID, AmountCents: amount, PaidAt: paidAt, Method: method, Reference: optionalFinanceString(p.Reference)}, nil
}

func cashSessionFromPayload(p cashSessionPayload) (CashSession, error) {
	userID, err := optionalFinanceUUID("user_id", p.UserID)
	if err != nil {
		return CashSession{}, err
	}
	amount, err := financeAmount(p.OpeningAmountCents, false)
	if err != nil {
		return CashSession{}, err
	}
	status, err := requiredFinanceString("status", p.Status)
	if err != nil {
		return CashSession{}, err
	}
	return CashSession{UserID: userID, OpeningAmountCents: amount, Status: status}, nil
}

func cashMovementFromPayload(p cashMovementPayload) (CashMovement, error) {
	sessionID, err := optionalFinanceUUID("session_id", p.SessionID)
	if err != nil {
		return CashMovement{}, err
	}
	if sessionID == uuid.Nil {
		return CashMovement{}, fmt.Errorf("%w: session_id é obrigatório", ErrInvalidHTTPFinanceRequest)
	}
	amount, err := financeAmount(p.AmountCents, true)
	if err != nil {
		return CashMovement{}, err
	}
	occurredAt, err := financeTime("occurred_at", p.OccurredAt, false)
	if err != nil {
		return CashMovement{}, err
	}
	kind, err := requiredFinanceString("kind", p.Kind)
	if err != nil {
		return CashMovement{}, err
	}
	reason, err := requiredFinanceString("reason", p.Reason)
	if err != nil {
		return CashMovement{}, err
	}
	return CashMovement{SessionID: sessionID, Kind: kind, AmountCents: amount, Reason: reason, OccurredAt: occurredAt}, nil
}

func ledgerEntryFromPayload(p ledgerEntryPayload) (LedgerEntry, error) {
	accountID, err := optionalFinanceUUID("account_id", p.AccountID)
	if err != nil {
		return LedgerEntry{}, err
	}
	if accountID == uuid.Nil {
		return LedgerEntry{}, fmt.Errorf("%w: account_id é obrigatório", ErrInvalidHTTPFinanceRequest)
	}
	competence, err := financeDate("competence", p.Competence, true)
	if err != nil {
		return LedgerEntry{}, err
	}
	debit, err := financeAmount(p.DebitCents, false)
	if err != nil {
		return LedgerEntry{}, err
	}
	credit, err := financeAmount(p.CreditCents, false)
	if err != nil {
		return LedgerEntry{}, err
	}
	if (debit == 0) == (credit == 0) {
		return LedgerEntry{}, fmt.Errorf("%w: informe exatamente um lado do lançamento", ErrInvalidHTTPFinanceRequest)
	}
	sourceID, err := optionalFinanceUUID("source_id", p.SourceID)
	if err != nil {
		return LedgerEntry{}, err
	}
	source, err := requiredFinanceString("source", p.Source)
	if err != nil {
		return LedgerEntry{}, err
	}
	return LedgerEntry{AccountID: accountID, Competence: competence, DebitCents: debit, CreditCents: credit, Source: source, SourceID: sourceID}, nil
}

func decodeFinanceBody[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var result T
	r.Body = http.MaxBytesReader(w, r.Body, maxFinanceBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return result, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return result, errors.New("corpo deve ser objeto JSON")
	}
	strict := json.NewDecoder(bytes.NewReader(trimmed))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&result); err != nil {
		return result, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return result, errors.New("corpo contém mais de um JSON")
	}
	return result, nil
}

func financeAmount(raw json.RawMessage, positive bool) (int64, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || string(value) == "null" || bytes.ContainsAny(value, ".eE") || (len(value) > 0 && value[0] == '"') {
		return 0, fmt.Errorf("%w: valor deve ser inteiro em centavos", ErrInvalidHTTPFinanceRequest)
	}
	amount, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil || amount < 0 || (positive && amount == 0) {
		return 0, fmt.Errorf("%w: valor monetário inválido", ErrInvalidHTTPFinanceRequest)
	}
	return amount, nil
}

func requiredFinanceString(field string, raw *string) (string, error) {
	value := optionalFinanceString(raw)
	if value == "" {
		return "", fmt.Errorf("%w: %s é obrigatório", ErrInvalidHTTPFinanceRequest, field)
	}
	return value, nil
}

func optionalFinanceString(raw *string) string {
	if raw == nil {
		return ""
	}
	return strings.TrimSpace(*raw)
}

func optionalFinanceUUID(field string, raw *string) (uuid.UUID, error) {
	value := optionalFinanceString(raw)
	if value == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: %s deve ser UUID válido", ErrInvalidHTTPFinanceRequest, field)
	}
	return id, nil
}

func financeDate(field string, raw *string, required bool) (time.Time, error) {
	value := optionalFinanceString(raw)
	if value == "" {
		if required {
			return time.Time{}, fmt.Errorf("%w: %s é obrigatório", ErrInvalidHTTPFinanceRequest, field)
		}
		return time.Time{}, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s deve estar no formato AAAA-MM-DD", ErrInvalidHTTPFinanceRequest, field)
	}
	return parsed, nil
}

func financeTime(field string, raw *string, required bool) (time.Time, error) {
	value := optionalFinanceString(raw)
	if value == "" {
		if required {
			return time.Time{}, fmt.Errorf("%w: %s é obrigatório", ErrInvalidHTTPFinanceRequest, field)
		}
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s deve estar em RFC3339", ErrInvalidHTTPFinanceRequest, field)
	}
	return parsed, nil
}

func developmentFinanceTenantID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(r.Header.Get(developmentTenantHeader)))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("tenant inválido")
	}
	return id, nil
}

func financeListLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		writeFinanceError(w, http.StatusBadRequest, "limit deve ser inteiro positivo")
		return 0, false
	}
	return limit, true
}

func parseFinanceID(w http.ResponseWriter, raw, resource string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || id == uuid.Nil {
		writeFinanceError(w, http.StatusBadRequest, "id de "+resource+" inválido")
		return uuid.Nil, false
	}
	return id, true
}

func accountResponse(item Account) map[string]any {
	return map[string]any{"id": item.ID.String(), "code": item.Code, "name": item.Name, "type": item.Type, "status": item.Status, "parent_id": optionalID(item.ParentID)}
}
func titleResponse(item Title) map[string]any {
	return map[string]any{"id": item.ID.String(), "direction": item.Direction, "party_id": optionalID(item.PartyID), "amount_cents": item.AmountCents, "due_date": item.DueDate.Format("2006-01-02"), "status": item.Status, "origin": item.Origin, "description": item.Description}
}
func installmentResponse(item Installment) map[string]any {
	return map[string]any{"id": item.ID.String(), "title_id": item.TitleID.String(), "number": item.Number, "amount_cents": item.AmountCents, "due_date": item.DueDate.Format("2006-01-02"), "status": item.Status}
}
func paymentResponse(item Payment) map[string]any {
	return map[string]any{"id": item.ID.String(), "installment_id": item.InstallmentID.String(), "bank_account_id": optionalID(item.BankAccountID), "amount_cents": item.AmountCents, "paid_at": item.PaidAt.UTC().Format(time.RFC3339Nano), "method": item.Method, "reference": item.Reference, "idempotency_key": item.IdempotencyKey}
}
func cashSessionResponse(item CashSession) map[string]any {
	var openedAt any
	if item.OpenedAt != nil {
		openedAt = item.OpenedAt.UTC().Format(time.RFC3339Nano)
	}
	response := map[string]any{"id": item.ID.String(), "user_id": optionalID(item.UserID), "opened_at": openedAt, "opening_amount_cents": item.OpeningAmountCents, "status": item.Status}
	if item.ClosedAt != nil {
		response["closed_at"] = item.ClosedAt.UTC().Format(time.RFC3339Nano)
	} else {
		response["closed_at"] = nil
	}
	return response
}
func cashMovementResponse(item CashMovement) map[string]any {
	return map[string]any{"id": item.ID.String(), "session_id": item.SessionID.String(), "kind": item.Kind, "amount_cents": item.AmountCents, "reason": item.Reason, "occurred_at": item.OccurredAt.UTC().Format(time.RFC3339Nano), "idempotency_key": item.IdempotencyKey}
}
func ledgerEntryResponse(item LedgerEntry) map[string]any {
	return map[string]any{"id": item.ID.String(), "account_id": item.AccountID.String(), "competence": item.Competence.Format("2006-01-02"), "debit_cents": item.DebitCents, "credit_cents": item.CreditCents, "source": item.Source, "source_id": optionalID(item.SourceID)}
}
func optionalID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id.String()
}

func writeFinanceServiceError(w http.ResponseWriter, err error, action string) {
	status, message := http.StatusInternalServerError, "não foi possível "+action
	switch {
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, "registro não encontrado neste tenant"
	case errors.Is(err, ErrInvalidAmount), errors.Is(err, ErrInvalidIdempotencyKey), errors.Is(err, ErrTenantMismatch), errors.Is(err, db.ErrTenantRequired), errors.Is(err, ErrInvalidHTTPFinanceRequest):
		status, message = http.StatusBadRequest, "dados financeiros inválidos"
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503":
				status, message = http.StatusNotFound, "referência não encontrada neste tenant"
			case "23505":
				status, message = http.StatusConflict, "registro financeiro já existe"
			case "23514":
				status, message = http.StatusBadRequest, "dados financeiros inválidos"
			case "P0001":
				status, message = http.StatusConflict, "operação financeira rejeitada"
			}
		}
	}
	writeFinanceError(w, status, message)
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeFinanceError(w, http.StatusMethodNotAllowed, "método não permitido")
}
func writeFinanceError(w http.ResponseWriter, status int, message string) {
	writeFinanceJSON(w, status, map[string]string{"status": "error", "error": message})
}
func writeFinanceJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
