package finance_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/finance"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
)

type financeHTTPFake struct {
	account         finance.Account
	title           finance.Title
	installment     finance.Installment
	payment         finance.Payment
	cashSession     finance.CashSession
	cashMovement    finance.CashMovement
	ledger          finance.LedgerEntry
	lastPaymentKey  string
	lastMovementKey string
}

func (f *financeHTTPFake) CreateAccount(ctx context.Context, item finance.Account) (finance.Account, error) {
	item.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000001")
	f.account = item
	return item, nil
}
func (f *financeHTTPFake) GetAccountByID(context.Context, uuid.UUID) (finance.Account, error) {
	return f.account, nil
}
func (f *financeHTTPFake) ListAccounts(context.Context, int) ([]finance.Account, error) {
	return []finance.Account{}, nil
}
func (f *financeHTTPFake) CreateTitle(ctx context.Context, item finance.Title) (finance.Title, error) {
	item.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000002")
	f.title = item
	return item, nil
}
func (f *financeHTTPFake) GetTitleByID(context.Context, uuid.UUID) (finance.Title, error) {
	return f.title, nil
}
func (f *financeHTTPFake) ListTitles(context.Context, int) ([]finance.Title, error) {
	return []finance.Title{}, nil
}
func (f *financeHTTPFake) CreateInstallment(ctx context.Context, item finance.Installment) (finance.Installment, error) {
	item.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000003")
	f.installment = item
	return item, nil
}
func (f *financeHTTPFake) GetInstallmentByID(context.Context, uuid.UUID) (finance.Installment, error) {
	return f.installment, nil
}
func (f *financeHTTPFake) ListInstallments(context.Context, uuid.UUID, int) ([]finance.Installment, error) {
	return []finance.Installment{}, nil
}
func (f *financeHTTPFake) CreatePaymentIdempotent(_ context.Context, key string, item finance.Payment) (finance.Payment, error) {
	f.lastPaymentKey = key
	if f.payment.ID == uuid.Nil {
		f.payment = item
		f.payment.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000004")
	}
	return f.payment, nil
}
func (f *financeHTTPFake) CreateCashSession(_ context.Context, item finance.CashSession) (finance.CashSession, error) {
	item.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000005")
	openedAt := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	item.OpenedAt = &openedAt
	f.cashSession = item
	return item, nil
}
func (f *financeHTTPFake) GetCashSessionByID(context.Context, uuid.UUID) (finance.CashSession, error) {
	return f.cashSession, nil
}
func (f *financeHTTPFake) CreateCashMovementIdempotent(_ context.Context, key string, item finance.CashMovement) (finance.CashMovement, error) {
	f.lastMovementKey = key
	if f.cashMovement.ID == uuid.Nil {
		f.cashMovement = item
		f.cashMovement.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000006")
	}
	return f.cashMovement, nil
}
func (f *financeHTTPFake) ListCashMovements(context.Context, uuid.UUID, int) ([]finance.CashMovement, error) {
	return []finance.CashMovement{}, nil
}
func (f *financeHTTPFake) CreateLedgerEntry(_ context.Context, item finance.LedgerEntry) (finance.LedgerEntry, error) {
	f.ledger = item
	item.ID = uuid.MustParse("018f0000-0000-7000-8000-000000000007")
	return item, nil
}
func (f *financeHTTPFake) ListLedgerEntries(context.Context, uuid.UUID, int) ([]finance.LedgerEntry, error) {
	return []finance.LedgerEntry{}, nil
}

func newFinanceHTTPHandler(service finance.DevelopmentService) http.Handler {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpserver.New(logger, httpserver.Options{DevelopmentMode: true, DevelopmentFinance: finance.NewDevelopmentHTTPHandler(service)}).Handler()
}

func financeRequest(handler http.Handler, method, path, tenant, body string, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Amparius-Development-Tenant-ID", tenant)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDevelopmentFinanceStrictHeadersAndTenant(t *testing.T) {
	handler := newFinanceHTTPHandler(&financeHTTPFake{})
	for _, body := range []string{
		`{"code":"1","name":"Caixa","type":"asset","status":"active","tenant_id":"` + uuid.NewString() + `"}`,
		`{"code":"1","name":"Caixa","type":"asset","status":"active","extra":true}`,
		`{"code":"1","name":"Caixa","type":"asset","status":"active"} {}`,
	} {
		rec := financeRequest(handler, http.MethodPost, "/_dev/finance/accounts", uuid.NewString(), body, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d: %s", body, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Amparius-Development-Only") != "true" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("headers ausentes: %#v", rec.Header())
		}
	}
	if rec := financeRequest(handler, http.MethodPost, "/_dev/finance/accounts", "", `{}`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("tenant ausente = %d", rec.Code)
	}
	for _, amount := range []string{`1.5`, `"100"`, `1e3`, `null`, `-1`} {
		body := `{"direction":"receivable","amount_cents":` + amount + `,"due_date":"2026-11-24","status":"open","origin":"manual"}`
		if rec := financeRequest(handler, http.MethodPost, "/_dev/finance/titles", uuid.NewString(), body, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("amount_cents=%s = %d, quer 400: %s", amount, rec.Code, rec.Body.String())
		}
	}
}

func TestDevelopmentFinanceJourneyAndMoneyIdempotency(t *testing.T) {
	fake := &financeHTTPFake{}
	handler := newFinanceHTTPHandler(fake)
	tenant := uuid.NewString()

	account := financeRequest(handler, http.MethodPost, "/_dev/finance/accounts", tenant, `{"code":"1.1","name":"Caixa","type":"asset","status":"active"}`, "")
	if account.Code != http.StatusCreated || fake.account.Code != "1.1" {
		t.Fatalf("conta = %d: %s", account.Code, account.Body.String())
	}
	title := financeRequest(handler, http.MethodPost, "/_dev/finance/titles", tenant, `{"direction":"receivable","amount_cents":10000,"due_date":"2026-11-24","status":"open","origin":"manual"}`, "")
	if title.Code != http.StatusCreated || fake.title.AmountCents != 10000 {
		t.Fatalf("título = %d: %s", title.Code, title.Body.String())
	}
	titleID := fake.title.ID.String()
	installment := financeRequest(handler, http.MethodPost, "/_dev/finance/titles/"+titleID+"/installments", tenant, `{"number":1,"amount_cents":10000,"due_date":"2026-11-24","status":"open"}`, "")
	if installment.Code != http.StatusCreated || fake.installment.TitleID != fake.title.ID {
		t.Fatalf("parcela = %d: %s", installment.Code, installment.Body.String())
	}
	session := financeRequest(handler, http.MethodPost, "/_dev/finance/cash-sessions", tenant, `{"opening_amount_cents":5000,"status":"open"}`, "")
	if session.Code != http.StatusCreated {
		t.Fatalf("sessão = %d: %s", session.Code, session.Body.String())
	}

	paymentBody := `{"installment_id":"` + fake.installment.ID.String() + `","amount_cents":1000,"method":"pix"}`
	payment := financeRequest(handler, http.MethodPost, "/_dev/finance/payments", tenant, paymentBody, "pay-001")
	if payment.Code != http.StatusCreated || fake.lastPaymentKey != "pay-001" {
		t.Fatalf("baixa = %d: %s", payment.Code, payment.Body.String())
	}
	replay := financeRequest(handler, http.MethodPost, "/_dev/finance/payments", tenant, `{"installment_id":"`+uuid.NewString()+`","amount_cents":900,"method":"cash"}`, "pay-001")
	if replay.Code != http.StatusCreated || !strings.Contains(replay.Body.String(), fake.payment.ID.String()) {
		t.Fatalf("replay = %d: %s", replay.Code, replay.Body.String())
	}
	if missing := financeRequest(handler, http.MethodPost, "/_dev/finance/payments", tenant, paymentBody, ""); missing.Code != http.StatusBadRequest {
		t.Fatalf("baixa sem idempotência = %d", missing.Code)
	}

	movementBody := `{"session_id":"` + fake.cashSession.ID.String() + `","kind":"in","amount_cents":100,"reason":"suprimento"}`
	movement := financeRequest(handler, http.MethodPost, "/_dev/finance/cash-movements", tenant, movementBody, "cash-001")
	if movement.Code != http.StatusCreated || fake.lastMovementKey != "cash-001" {
		t.Fatalf("movimento = %d: %s", movement.Code, movement.Body.String())
	}
	if replay := financeRequest(handler, http.MethodPost, "/_dev/finance/cash-movements", tenant, movementBody, "cash-001"); replay.Code != http.StatusCreated {
		t.Fatalf("replay movimento = %d", replay.Code)
	}

	var response map[string]any
	if err := json.Unmarshal(payment.Body.Bytes(), &response); err != nil || response["status"] != "ok" {
		t.Fatalf("JSON de baixa inválido: %s", payment.Body.String())
	}
}

func TestDevelopmentFinanceRoutesAbsentWithoutOptIn(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger).Handler()
	req := httptest.NewRequest(http.MethodGet, "/_dev/finance/titles", nil)
	req.Header.Set("X-Amparius-Development-Tenant-ID", uuid.NewString())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rota sem opt-in = %d", rec.Code)
	}
}
