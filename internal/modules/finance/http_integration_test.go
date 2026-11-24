package finance_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/finance"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDevelopmentFinanceHTTPPostgresTenantIsolationAndCleanup(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_ADMIN_DSN não definido; teste HTTP Finance não criará dados sem cleanup administrativo")
	}

	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}

	var role string
	var bypass bool
	if err := database.WithTenantTx(db.WithTenant(ctx, uuid.New()), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if role != "amparius_app" || bypass {
		t.Fatalf("teste precisa de amparius_app sem BYPASSRLS; role=%s bypass=%v", role, bypass)
	}

	tenantA, tenantB := uuid.New(), uuid.New()
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		tx, cleanupErr := adminPool.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, statement := range []string{
				"ALTER TABLE finance.payment DISABLE TRIGGER payment_append_only",
				"ALTER TABLE finance.installment DISABLE TRIGGER installment_history_preserve",
				"ALTER TABLE finance.title DISABLE TRIGGER title_history_preserve",
				"ALTER TABLE finance.cash_session DISABLE TRIGGER cash_session_history_preserve",
				"ALTER TABLE finance.cash_movement DISABLE TRIGGER cash_movement_append_only",
				"ALTER TABLE finance.ledger_entry DISABLE TRIGGER ledger_entry_append_only",
			} {
				if _, cleanupErr = tx.Exec(cleanupCtx, statement); cleanupErr != nil {
					break
				}
			}
			for _, statement := range []string{
				"DELETE FROM finance.payment WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.cash_movement WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.ledger_entry WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.installment WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.title WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.cash_session WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.account WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
			} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, statement, []uuid.UUID{tenantA, tenantB})
			}
			for _, statement := range []string{
				"ALTER TABLE finance.payment ENABLE TRIGGER payment_append_only",
				"ALTER TABLE finance.installment ENABLE TRIGGER installment_history_preserve",
				"ALTER TABLE finance.title ENABLE TRIGGER title_history_preserve",
				"ALTER TABLE finance.cash_session ENABLE TRIGGER cash_session_history_preserve",
				"ALTER TABLE finance.cash_movement ENABLE TRIGGER cash_movement_append_only",
				"ALTER TABLE finance.ledger_entry ENABLE TRIGGER ledger_entry_append_only",
			} {
				if cleanupErr != nil {
					break
				}
				if _, cleanupErr = tx.Exec(cleanupCtx, statement); cleanupErr != nil {
					break
				}
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(cleanupCtx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup Finance HTTP: %v", cleanupErr)
			return
		}
		var remaining int
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, []uuid.UUID{tenantA, tenantB}).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup deixou %d tenant(s) residuais", remaining)
		}
		adminPool.Close()
	})

	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,$4,$5)`, tenantID, "Finance HTTP "+tenantID.String(), "http-"+tenantID.String(), "economic_group", "active")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	repository, err := finance.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger, httpserver.Options{DevelopmentMode: true, DevelopmentFinance: finance.NewDevelopmentHTTPHandler(repository)}).Handler()

	request := func(method, path string, tenant uuid.UUID, body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Amparius-Development-Tenant-ID", tenant.String())
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	readID := func(t *testing.T, rec *httptest.ResponseRecorder, key string) string {
		t.Helper()
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("resposta não JSON: %s", rec.Body.String())
		}
		var object struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(envelope[key], &object); err != nil || object.ID == "" {
			t.Fatalf("objeto %s inválido: %s", key, rec.Body.String())
		}
		return object.ID
	}

	account := request(http.MethodPost, "/_dev/finance/accounts", tenantA, `{"code":"1.1","name":"Caixa","type":"asset","status":"active"}`, "")
	if account.Code != http.StatusCreated {
		t.Fatalf("criar conta = %d: %s", account.Code, account.Body.String())
	}
	accountID := readID(t, account, "account")
	title := request(http.MethodPost, "/_dev/finance/titles", tenantA, `{"direction":"receivable","amount_cents":10000,"due_date":"2026-11-24","status":"open","origin":"manual"}`, "")
	if title.Code != http.StatusCreated {
		t.Fatalf("criar título = %d: %s", title.Code, title.Body.String())
	}
	titleID := readID(t, title, "title")
	installment := request(http.MethodPost, "/_dev/finance/titles/"+titleID+"/installments", tenantA, `{"number":1,"amount_cents":10000,"due_date":"2026-11-24","status":"open"}`, "")
	if installment.Code != http.StatusCreated {
		t.Fatalf("criar parcela = %d: %s", installment.Code, installment.Body.String())
	}
	installmentID := readID(t, installment, "installment")
	session := request(http.MethodPost, "/_dev/finance/cash-sessions", tenantA, `{"opening_amount_cents":5000,"status":"open"}`, "")
	if session.Code != http.StatusCreated {
		t.Fatalf("abrir caixa = %d: %s", session.Code, session.Body.String())
	}
	sessionID := readID(t, session, "cash_session")
	paymentBody := `{"installment_id":"` + installmentID + `","amount_cents":1000,"method":"pix"}`
	payment := request(http.MethodPost, "/_dev/finance/payments", tenantA, paymentBody, "http-payment-001")
	if payment.Code != http.StatusCreated {
		t.Fatalf("registrar baixa = %d: %s", payment.Code, payment.Body.String())
	}
	paymentID := readID(t, payment, "payment")
	replay := request(http.MethodPost, "/_dev/finance/payments", tenantA, `{"installment_id":"`+uuid.NewString()+`","amount_cents":900,"method":"cash"}`, "http-payment-001")
	if replay.Code != http.StatusCreated || readID(t, replay, "payment") != paymentID {
		t.Fatalf("replay de baixa = %d: %s", replay.Code, replay.Body.String())
	}
	movement := request(http.MethodPost, "/_dev/finance/cash-movements", tenantA, `{"session_id":"`+sessionID+`","kind":"in","amount_cents":100,"reason":"suprimento"}`, "http-cash-001")
	if movement.Code != http.StatusCreated {
		t.Fatalf("movimento de caixa = %d: %s", movement.Code, movement.Body.String())
	}
	ledger := request(http.MethodPost, "/_dev/finance/ledger-entries", tenantA, `{"account_id":"`+accountID+`","competence":"2026-11-24","debit_cents":10000,"credit_cents":0,"source":"http-test"}`, "")
	if ledger.Code != http.StatusCreated {
		t.Fatalf("lançamento = %d: %s", ledger.Code, ledger.Body.String())
	}
	if hidden := request(http.MethodGet, "/_dev/finance/titles/"+titleID, tenantB, "", ""); hidden.Code != http.StatusNotFound {
		t.Fatalf("isolamento de título = %d: %s", hidden.Code, hidden.Body.String())
	}
	if list := request(http.MethodGet, "/_dev/finance/titles", tenantB, "", ""); list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"titles":[]`) {
		t.Fatalf("listagem isolada = %d: %s", list.Code, list.Body.String())
	}
	if bad := request(http.MethodPost, "/_dev/finance/payments", tenantA, paymentBody, ""); bad.Code != http.StatusBadRequest {
		t.Fatalf("baixa sem chave = %d", bad.Code)
	}
}
