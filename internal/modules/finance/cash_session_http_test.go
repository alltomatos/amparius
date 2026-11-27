package finance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/finance"
	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const cashSessions = "/v1/finance/cash-sessions"
const cashMovements = "/v1/finance/cash-movements"
const financePayments = "/v1/finance/payments"

type cashTestGate map[string]auth.Principal

func (g cashTestGate) Authenticate(_ context.Context, bearer string) (auth.Principal, error) {
	if principal, ok := g[bearer]; ok {
		return principal, nil
	}
	return auth.Principal{}, auth.ErrInvalidBearer
}

func (g cashTestGate) Allows(p auth.Principal, permission auth.Permission) bool {
	return p.Subject != "reader" || permission == auth.PermissionFinanceRead
}

func cashTestPrincipal(tenant, user uuid.UUID, subject string) auth.Principal {
	return auth.Principal{Subject: subject, TenantID: tenant, UserID: user, CompanyID: uuid.New(), MFAAuthenticated: true}
}

func cashTestHandler(service finance.FinanceOperationalService, gate cashTestGate) http.Handler {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpserver.New(logger, httpserver.Options{
		Authenticator: gate, AuthenticatedRoutes: finance.AuthenticatedRoutes(service),
	}).Handler()
}

func cashHTTPRequest(ctx context.Context, handler http.Handler, method, path, token, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func assertCashHTTP(t *testing.T, res *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("status=%d, esperado=%d, corpo=%s", res.Code, status, res.Body)
	}
	if code != "" {
		var result struct{ Code string }
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || result.Code != code {
			t.Fatalf("código esperado=%s, corpo=%s", code, res.Body)
		}
	}
}

type cashErrorService struct {
	finance.FinanceOperationalService
	openErr, closeErr, moveErr error
}

func (s cashErrorService) OpenCashSession(context.Context, string, finance.CashSession) (finance.CashSession, error) {
	return finance.CashSession{}, s.openErr
}
func (s cashErrorService) CloseCashSession(context.Context, uuid.UUID, string, finance.CashClosure) (finance.CashSession, error) {
	return finance.CashSession{}, s.closeErr
}
func (s cashErrorService) RecordCashMovement(context.Context, string, finance.CashMovement) (finance.CashMovement, error) {
	return finance.CashMovement{}, s.moveErr
}

func TestCashSessionHTTPValidation(t *testing.T) {
	owner := cashTestPrincipal(uuid.New(), uuid.New(), "owner")
	handler := cashTestHandler(cashErrorService{
		openErr: finance.ErrCashSessionAlreadyOpen, closeErr: finance.ErrCashSessionAlreadyClosed,
		moveErr: finance.ErrIdempotencyConflict,
	}, cashTestGate{
		"Bearer owner": owner, "Bearer no-user": cashTestPrincipal(owner.TenantID, uuid.Nil, "no-user"),
		"Bearer reader": cashTestPrincipal(owner.TenantID, uuid.New(), "reader"),
	})
	id := uuid.NewString()
	open := `{"opening_amount_cents":1000}`
	closeBody := `{"counted_amount_cents":900}`
	move := fmt.Sprintf(`{"session_id":%q,"kind":"out","amount_cents":100,"reason":"teste"}`, id)
	for _, tc := range []struct {
		name, method, path, token, key, body, code string
		status                                     int
	}{
		{"open-no-key", "POST", cashSessions, "owner", "", open, "VALIDATION_ERROR", 400},
		{"close-no-key", "POST", cashSessions + "/" + id + "/close", "owner", "", closeBody, "VALIDATION_ERROR", 400},
		{"move-no-key", "POST", cashMovements, "owner", "", move, "VALIDATION_ERROR", 400},
		{"no-user", "POST", cashSessions, "no-user", "k", open, "UNAUTHENTICATED", 401},
		{"reader", "POST", cashSessions, "reader", "k", open, "", 403},
		{"unauthenticated", "GET", cashSessions, "", "", "", "", 401},
		{"open-conflict", "POST", cashSessions, "owner", "k", open, "CASH_SESSION_ALREADY_OPEN", 409},
		{"closed-conflict", "POST", cashSessions + "/" + id + "/close", "owner", "k", closeBody, "CASH_SESSION_ALREADY_CLOSED", 409},
		{"key-conflict", "POST", cashMovements, "owner", "k", move, "IDEMPOTENCY_CONFLICT", 409},
		{"bad-cursor", "GET", cashSessions + "?cursor=!!", "owner", "", "", "VALIDATION_ERROR", 400},
		{"bad-status", "GET", cashSessions + "?status=pending", "owner", "", "", "VALIDATION_ERROR", 400},
		{"bad-detail-id", "GET", cashSessions + "/x", "owner", "", "", "VALIDATION_ERROR", 400},
		{"bad-movements-id", "GET", cashSessions + "/x/movements", "owner", "", "", "VALIDATION_ERROR", 400},
		{"bad-reconciliation-id", "GET", cashSessions + "/x/reconciliation", "owner", "", "", "VALIDATION_ERROR", 400},
		{"bad-payment-session", "POST", financePayments, "owner", "k", fmt.Sprintf(`{"installment_id":%q,"amount_cents":1,"method":"cash","cash_session_id":"x"}`, id), "VALIDATION_ERROR", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertCashHTTP(t, cashHTTPRequest(context.Background(), handler, tc.method, tc.path, tc.token, tc.key, tc.body), tc.status, tc.code)
		})
	}
}

func TestCashSessionHTTPPostgres(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN são necessários")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	metrics := observability.NewMetrics()
	repo, err := finance.NewRepository(database, finance.WithCashSessionObservability(logger, metrics))
	if err != nil {
		t.Fatal(err)
	}
	tenantA, tenantB, user := uuid.New(), uuid.New(), uuid.New()
	for _, tenant := range []uuid.UUID{tenantA, tenantB} {
		_, err := admin.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status)
			VALUES ($1,'FIN02A fixture',$1::uuid::text,'economic_group','active')`, tenant)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cleanupCashTestTenant(t, admin, tenant) })
	}
	principalA := cashTestPrincipal(tenantA, user, "a")
	handler := cashTestHandler(repo, cashTestGate{
		"Bearer a": principalA, "Bearer b": cashTestPrincipal(tenantB, user, "b"),
	})
	request := func(method, path, token, key, body string, status int, code string) map[string]json.RawMessage {
		t.Helper()
		res := cashHTTPRequest(ctx, handler, method, path, token, key, body)
		assertCashHTTP(t, res, status, code)
		var result map[string]json.RawMessage
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	assertFields := func(result map[string]json.RawMessage, want map[string]string) {
		t.Helper()
		for field, value := range want {
			if got := string(result[field]); got != value {
				t.Fatalf("%s=%s, esperado %s", field, got, value)
			}
		}
	}
	assertDifferenceCounter := func(count int) {
		t.Helper()
		const name = "amparius_finance_cash_session_close_difference_total"
		res := httptest.NewRecorder()
		metrics.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		want := fmt.Sprintf("# TYPE %s counter\n%s %d\n", name, name, count)
		if res.Code != http.StatusOK || res.Body.String() != want {
			t.Fatalf("métrica de divergência incorreta: %d %s", res.Code, res.Body)
		}
	}
	assertDifferenceCounter(0)
	ctxA, ctxB := db.WithTenant(ctx, tenantA), db.WithTenant(ctx, tenantB)
	installment := createCashTestInstallment(t, repo, ctxA, 200000)
	installmentB := createCashTestInstallment(t, repo, ctxB, 200000)
	open := `{"opening_amount_cents":10000}`
	first := request("POST", cashSessions, "a", "open", open, 201, "")
	id, err := uuid.Parse(strings.Trim(string(first["id"]), `"`))
	if err != nil {
		t.Fatal(err)
	}
	path := cashSessions + "/" + id.String()
	assertFields(first, map[string]string{"user_id": fmt.Sprintf("%q", user), "status": `"open"`})
	assertFields(request("POST", cashSessions, "a", "open", open, 201, ""), map[string]string{"id": string(first["id"])})
	request("POST", cashSessions, "a", "open", `{"opening_amount_cents":1}`, 409, "IDEMPOTENCY_CONFLICT")
	request("POST", cashSessions, "a", "open-2", open, 409, "CASH_SESSION_ALREADY_OPEN")
	other := request("POST", cashSessions, "b", "open", open, 201, "")
	assertFields(request("GET", cashSessions+"?status=open", "b", "", "", 200, ""), map[string]string{
		"items": "[" + compactCashSession(t, other) + "]",
	})
	for _, suffix := range []string{"", "/movements", "/reconciliation"} {
		request("GET", path+suffix, "b", "", "", 404, "FINANCE_REFERENCE_NOT_FOUND")
	}

	movementBody := func(kind string, cents int64) string {
		return fmt.Sprintf(`{"session_id":%q,"kind":%q,"amount_cents":%d,"reason":"teste"}`, id, kind, cents)
	}
	paymentBody := func(installment, session uuid.UUID, method string, cents int64) string {
		return fmt.Sprintf(`{"installment_id":%q,"cash_session_id":%q,"amount_cents":%d,"method":%q}`, installment, session, cents, method)
	}
	request("POST", financePayments, "b", "cross", paymentBody(installmentB, id, "cash", 1), 404, "FINANCE_REFERENCE_NOT_FOUND")
	in := request("POST", cashMovements, "a", "in", movementBody("in", 2000), 201, "")
	out := request("POST", cashMovements, "a", "out", movementBody("out", 500), 201, "")
	assertFields(request("POST", cashMovements, "a", "in", movementBody("in", 2000), 201, ""), map[string]string{"id": string(in["id"])})
	request("POST", cashMovements, "a", "out", movementBody("out", 501), 409, "IDEMPOTENCY_CONFLICT")
	cashBody := paymentBody(installment, id, "cash", 3000)
	cash := request("POST", financePayments, "a", "cash", cashBody, 201, "")
	assertFields(cash, map[string]string{"cash_session_id": string(first["id"])})
	request("POST", financePayments, "a", "pix", paymentBody(installment, id, "pix", 4000), 201, "")
	request("POST", financePayments, "a", "card", paymentBody(installment, id, "card", 5000), 201, "")
	assertFields(request("GET", path, "a", "", "", 200, ""), map[string]string{"expected_amount_cents": "14500", "counted_amount_cents": "null"})
	closeBody := `{"counted_amount_cents":14470,"note":"contagem de teste"}`
	closed := request("POST", path+"/close", "a", "close", closeBody, 200, "")
	assertFields(closed, map[string]string{
		"status": `"closed"`, "expected_amount_cents": "14500", "counted_amount_cents": "14470",
		"difference_amount_cents": "-30", "closed_by": fmt.Sprintf("%q", user), "note": `"contagem de teste"`,
	})
	assertFields(request("POST", path+"/close", "a", "close", closeBody, 200, ""), map[string]string{"closed_at": string(closed["closed_at"])})
	request("POST", path+"/close", "a", "close", `{"counted_amount_cents":14470,"note":"alterado"}`, 409, "IDEMPOTENCY_CONFLICT")
	request("POST", path+"/close", "a", "another-close", closeBody, 409, "CASH_SESSION_ALREADY_CLOSED")
	otherPath := cashSessions + "/" + strings.Trim(string(other["id"]), `"`)
	assertFields(request("POST", otherPath+"/close", "b", "balanced-close", `{"counted_amount_cents":10000}`, 200, ""), map[string]string{
		"difference_amount_cents": "0",
	})
	assertDifferenceCounter(1)
	var event map[string]json.RawMessage
	if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
		t.Fatalf("esperado um único evento de divergência: %v", err)
	}
	assertFields(event, map[string]string{
		"msg": `"finance_cash_session_close_difference"`, "level": `"WARN"`,
		"tenant_id": fmt.Sprintf("%q", tenantA), "session_id": fmt.Sprintf("%q", id),
		"user_id": fmt.Sprintf("%q", user), "difference_amount_cents": "-30",
	})
	if strings.Contains(logs.String(), "contagem de teste") || strings.Contains(logs.String(), "alterado") {
		t.Fatal("nota livre apareceu no log de divergência")
	}
	t.Log("observabilidade: divergência -30 gerou um log estruturado e contador Prometheus 1 após replay, conflitos e fechamento sem diferença")
	request("POST", cashMovements, "a", "new-out", movementBody("out", 500), 409, "CASH_SESSION_ALREADY_CLOSED")
	request("POST", financePayments, "a", "new-cash", cashBody, 409, "CASH_SESSION_ALREADY_CLOSED")
	assertFields(request("POST", cashMovements, "a", "out", movementBody("out", 500), 201, ""), map[string]string{"id": string(out["id"])})
	assertFields(request("POST", financePayments, "a", "cash", cashBody, 201, ""), map[string]string{"id": string(cash["id"])})
	request("POST", financePayments, "a", "cash", paymentBody(installment, id, "cash", 3001), 409, "IDEMPOTENCY_CONFLICT")
	totals := request("GET", path+"/reconciliation", "a", "", "", 200, "")
	assertFields(totals, map[string]string{
		"expected_amount_cents": "14500", "difference_amount_cents": "-30",
		"movement_in_cents": "2000", "movement_out_cents": "500",
		"payments_by_method_cents": `{"card":5000,"cash":3000,"pix":4000}`,
	})
	moves := request("GET", path+"/movements", "a", "", "", 200, "")
	var items []struct{ ID uuid.UUID }
	if err := json.Unmarshal(moves["items"], &items); err != nil || len(items) != 2 {
		t.Fatalf("movimentos, esperado 2: %s", moves["items"])
	}
	second := request("POST", cashSessions, "a", "open-2", open, 201, "")
	firstPage := request("GET", cashSessions+"?limit=1", "a", "", "", 200, "")
	var cursor string
	if err := json.Unmarshal(firstPage["next_cursor"], &cursor); err != nil || cursor == "" {
		t.Fatalf("cursor ausente: %s", firstPage["next_cursor"])
	}
	assertFields(firstPage, map[string]string{"items": "[" + compactCashSession(t, closed) + "]"})
	assertFields(request("GET", cashSessions+"?limit=1&cursor="+cursor, "a", "", "", 200, ""), map[string]string{
		"items": "[" + compactCashSession(t, second) + "]",
	})

	proveCashPermissions(t, database, ctxA, ctxB, id)
	t.Log("RLS direta: sessões, movimentos e baixas do outro tenant invisíveis; UPDATE de abertura e DELETE negados (42501)")

	secondID := uuid.MustParse(strings.Trim(string(second["id"]), `"`))
	secondPath := cashSessions + "/" + secondID.String()
	start := make(chan struct{})
	paid := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		<-start
		paid <- cashHTTPRequest(ctx, handler, "POST", financePayments, "a", "race-pay", paymentBody(installment, secondID, "cash", 700))
	}()
	close(start)
	raced := request("POST", secondPath+"/close", "a", "race-close", `{"counted_amount_cents":10000}`, 200, "")
	res := <-paid
	switch res.Code {
	case 201:
		assertFields(raced, map[string]string{"expected_amount_cents": "10700", "difference_amount_cents": "-700"})
	case 409:
		assertCashHTTP(t, res, 409, "CASH_SESSION_ALREADY_CLOSED")
		assertFields(raced, map[string]string{"expected_amount_cents": "10000", "difference_amount_cents": "0"})
	default:
		t.Fatalf("baixa concorrente: %d %s", res.Code, res.Body)
	}
	t.Logf("fechamento concorrente: baixa HTTP %d, esperado %s; diferença persistida -30 centavos no fluxo principal", res.Code, raced["expected_amount_cents"])
	if os.Getenv("AMPA_FIN02A_LATENCY") == "1" {
		measureCashLatency(t, ctx, handler, installment, paymentBody)
	}
}

func compactCashSession(t *testing.T, item map[string]json.RawMessage) string {
	t.Helper()
	value, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func createCashTestInstallment(t *testing.T, repo *finance.Repository, ctx context.Context, cents int64) uuid.UUID {
	t.Helper()
	due := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	title, err := repo.CreateTitle(ctx, finance.Title{Direction: "receivable", AmountCents: cents, Status: "open", Origin: "sale", DueDate: due})
	if err != nil {
		t.Fatal(err)
	}
	installment, err := repo.CreateInstallment(ctx, finance.Installment{TitleID: title.ID, Number: 1, AmountCents: cents, DueDate: due, Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	return installment.ID
}

func proveCashPermissions(t *testing.T, database *db.DB, ctxA, ctxB context.Context, id uuid.UUID) {
	t.Helper()
	if err := database.WithTenantTx(ctxB, func(ctx context.Context, tx pgx.Tx) error {
		var role string
		var superuser, bypass bool
		if err := tx.QueryRow(ctx, `SELECT rolname,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &superuser, &bypass); err != nil {
			return err
		}
		if role != "amparius_app" || superuser || bypass {
			return fmt.Errorf("papel incompatível: %s superuser=%t bypassrls=%t", role, superuser, bypass)
		}
		for _, query := range []string{
			`SELECT count(*) FROM finance.cash_session WHERE id=$1`,
			`SELECT count(*) FROM finance.cash_movement WHERE session_id=$1`,
			`SELECT count(*) FROM finance.payment WHERE session_id=$1`,
		} {
			var count int
			if err := tx.QueryRow(ctx, query, id).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("RLS expôs %d registros do outro tenant", count)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE finance.cash_session SET opening_amount=0 WHERE id=$1`,
		`DELETE FROM finance.cash_session WHERE id=$1`,
	} {
		err := database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, query, id)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("esperado SQLSTATE 42501 em %s, recebido %v", query, err)
		}
	}
}

func cleanupCashTestTenant(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, statement := range []string{
		`SET LOCAL session_replication_role = replica`,
		`DELETE FROM finance.payment WHERE tenant_id=$1`,
		`DELETE FROM finance.cash_movement WHERE tenant_id=$1`,
		`DELETE FROM finance.cash_session WHERE tenant_id=$1`,
		`DELETE FROM finance.installment WHERE tenant_id=$1`,
		`DELETE FROM finance.title WHERE tenant_id=$1`,
		`DELETE FROM outbox.event WHERE tenant_id=$1`,
		`DELETE FROM identity.tenant WHERE id=$1`,
	} {
		args := []any(nil)
		if strings.Contains(statement, "$1") {
			args = append(args, tenant)
		}
		if _, err := tx.Exec(ctx, statement, args...); err != nil {
			t.Errorf("cleanup %s: %v", statement, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Error(err)
	}
}

func measureCashLatency(t *testing.T, ctx context.Context, handler http.Handler, installment uuid.UUID, paymentBody func(uuid.UUID, uuid.UUID, string, int64) string) {
	t.Helper()
	const warmup, samples = 20, 200
	var durations [5][]time.Duration
	measure := func(operation, iteration int, method, path, key, body string, status int) []byte {
		t.Helper()
		start := time.Now()
		res := cashHTTPRequest(ctx, handler, method, path, "a", key, body)
		elapsed := time.Since(start)
		assertCashHTTP(t, res, status, "")
		if iteration >= warmup {
			durations[operation] = append(durations[operation], elapsed)
		}
		return res.Body.Bytes()
	}
	for i := 0; i < warmup+samples; i++ {
		key := fmt.Sprintf("latency-%d", i)
		opened := measure(0, i, "POST", cashSessions, key, `{"opening_amount_cents":1000}`, 201)
		var item struct{ ID uuid.UUID }
		if err := json.Unmarshal(opened, &item); err != nil || item.ID == uuid.Nil {
			t.Fatalf("sessão da medição: %s", opened)
		}
		path := cashSessions + "/" + item.ID.String()
		measure(1, i, "POST", cashMovements, key, fmt.Sprintf(`{"session_id":%q,"kind":"out","amount_cents":1,"reason":"latência"}`, item.ID), 201)
		measure(2, i, "POST", financePayments, key, paymentBody(installment, item.ID, "cash", 1), 201)
		measure(3, i, "POST", path+"/close", key, `{"counted_amount_cents":1000}`, 200)
		measure(4, i, "GET", path+"/reconciliation", "", "", 200)
	}
	for i, label := range []string{"abrir", "movimentar", "baixar", "fechar", "conciliar"} {
		slices.Sort(durations[i])
		t.Logf("latência %s n=%d p50=%s p95=%s p99=%s", label, samples,
			durations[i][(samples*50+99)/100-1], durations[i][(samples*95+99)/100-1], durations[i][(samples*99+99)/100-1])
	}
}
