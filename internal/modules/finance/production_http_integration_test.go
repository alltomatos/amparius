package finance_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/finance"
	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFinanceProductionHTTPPostgres(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN são necessários")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	var tenants []uuid.UUID
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		disable := []string{
			"ALTER TABLE finance.payment DISABLE TRIGGER payment_append_only",
			"ALTER TABLE finance.installment DISABLE TRIGGER installment_history_preserve",
			"ALTER TABLE finance.title DISABLE TRIGGER title_history_preserve",
		}
		enable := []string{
			"ALTER TABLE finance.payment ENABLE TRIGGER payment_append_only",
			"ALTER TABLE finance.installment ENABLE TRIGGER installment_history_preserve",
			"ALTER TABLE finance.title ENABLE TRIGGER title_history_preserve",
		}
		for _, statement := range disable {
			if _, err := admin.Exec(cleanupCtx, statement); err != nil {
				t.Logf("desabilitar trigger: %v", err)
			}
		}
		tx, err := admin.Begin(cleanupCtx)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = tx.Rollback(cleanupCtx) }()
		for _, statement := range []string{
			"DELETE FROM finance.payment WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM finance.installment WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM finance.title WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.user_company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.user_role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.onboarding_request WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.app_user WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.establishment WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
		} {
			if _, err := tx.Exec(cleanupCtx, statement, tenants); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
				return
			}
		}
		if err := tx.Commit(cleanupCtx); err != nil {
			t.Error(err)
		}
		for _, statement := range enable {
			if _, err := admin.Exec(cleanupCtx, statement); err != nil {
				t.Logf("reabilitar trigger: %v", err)
			}
		}
	})

	repo, err := finance.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	identities, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}

	onboard := func(role string) auth.OIDCIdentity {
		t.Helper()
		result, err := identities.Onboard(ctx, identity.DevelopmentRequest("fin76-"+uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, result.TenantID)
		subject := "finance-" + uuid.NewString()
		if _, err := admin.Exec(ctx, `UPDATE identity.app_user SET auth_subject=$1, status='active', mfa_enabled=true WHERE id=$2`, subject, result.OwnerUserID); err != nil {
			t.Fatal(err)
		}
		// identity.create_onboarding (migration 0042) já vincula o owner à
		// empresa sede em identity.user_company; nenhum INSERT manual é necessário.
		if role == "auditor" {
			if _, err := admin.Exec(ctx, `UPDATE identity.role SET code='auditor' WHERE tenant_id=$1 AND code='owner'`, result.TenantID); err != nil {
				t.Fatal(err)
			}
		}
		return auth.OIDCIdentity{Issuer: "finance-test-issuer", Subject: subject, TenantID: result.TenantID, CompanyID: result.CompanyID}
	}

	ownerA := onboard("owner")
	ownerB := onboard("owner")
	auditor := onboard("auditor")
	tokens := map[string]auth.OIDCIdentity{"owner-a": ownerA, "owner-b": ownerB, "auditor": auditor}
	invalidScope := ownerA
	invalidScope.CompanyID = ownerB.CompanyID
	tokens["invalid-scope"] = invalidScope

	oidc := auth.OIDCVerifierFunc(func(_ context.Context, token string) (auth.OIDCIdentity, error) {
		if claims, found := tokens[token]; found {
			return claims, nil
		}
		return auth.OIDCIdentity{}, auth.ErrInvalidOIDCToken
	})
	mfa := auth.MFAVerifierFunc(func(context.Context, auth.OIDCIdentity) (bool, error) { return true, nil })
	policy, err := auth.DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gate, err := auth.NewService(oidc, mfa, identities, identities, policy, logger)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpserver.New(logger, httpserver.Options{
		Authenticator:       gate,
		AuthenticatedRoutes: finance.AuthenticatedRoutes(repo),
	}).Handler()

	const titles, payments = "/v1/finance/titles", "/v1/finance/payments"
	request := func(t *testing.T, h http.Handler, method, path, token, body string, status int, code string) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != status || (code != "" && !strings.Contains(rec.Body.String(), `"code":"`+code+`"`)) {
			t.Fatalf("%s %s: status=%d body=%s, esperado %d %s", method, path, rec.Code, rec.Body, status, code)
		}
		return rec.Body.Bytes()
	}

	t.Run("rotas exigem autenticação, escopo e finance:read/write", func(t *testing.T) {
		restricted, err := auth.NewRolePolicy(map[string][]auth.Permission{"owner": {auth.PermissionPartyWrite}})
		if err != nil {
			t.Fatal(err)
		}
		withoutFinance, err := auth.NewService(oidc, mfa, identities, identities, restricted, logger)
		if err != nil {
			t.Fatal(err)
		}
		denied := httpserver.New(logger, httpserver.Options{
			Authenticator: withoutFinance, AuthenticatedRoutes: finance.AuthenticatedRoutes(repo),
		}).Handler()
		request(t, handler, http.MethodGet, titles, "", "", 401, "AUTHENTICATION_REQUIRED")
		request(t, handler, http.MethodGet, titles, "invalid-scope", "", 403, "SCOPE_FORBIDDEN")
		request(t, denied, http.MethodGet, titles, "owner-a", "", 403, "PERMISSION_DENIED")
		request(t, denied, http.MethodPost, payments, "owner-a", `{}`, 403, "PERMISSION_DENIED")
	})

	t.Run("cria título, lista e consulta persistido", func(t *testing.T) {
		body := request(t, handler, http.MethodPost, titles, "owner-a", `{"direction":"receivable","amount_cents":10000,"due_date":"2026-12-01","status":"open","origin":"sale"}`, 201, "")
		var created struct{ ID string }
		if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
			t.Fatalf("título criado inválido: %s", body)
		}
		got := request(t, handler, http.MethodGet, titles+"/"+created.ID, "owner-a", "", 200, "")
		if !strings.Contains(string(got), created.ID) || !strings.Contains(string(got), "10000") {
			t.Fatalf("consulta não retornou o título persistido: %s", got)
		}
		list := request(t, handler, http.MethodGet, titles, "owner-a", "", 200, "")
		if !strings.Contains(string(list), created.ID) {
			t.Fatalf("listagem não contém o título: %s", list)
		}
	})

	t.Run("pagamento é idempotente e isola por tenant", func(t *testing.T) {
		titleBody := request(t, handler, http.MethodPost, titles, "owner-a", `{"direction":"receivable","amount_cents":10000,"due_date":"2026-12-01","status":"open","origin":"sale"}`, 201, "")
		var title struct{ ID string }
		_ = json.Unmarshal(titleBody, &title)

		installment, err := repo.CreateInstallment(db.WithTenant(ctx, ownerA.TenantID), finance.Installment{
			TitleID: uuid.MustParse(title.ID), Number: 1, AmountCents: 10000,
			DueDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), Status: "open",
		})
		if err != nil {
			t.Fatal(err)
		}

		pay := func(h http.Handler, token, key, body string) []byte {
			req := httptest.NewRequest(http.MethodPost, payments, strings.NewReader(body)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Idempotency-Key", key)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("pagamento: status=%d body=%s", rec.Code, rec.Body)
			}
			return rec.Body.Bytes()
		}
		body := `{"installment_id":"` + installment.ID.String() + `","amount_cents":10000,"method":"pix","reference":"replay"}`
		first := pay(handler, "owner-a", "k1", body)
		var p1 struct{ ID string }
		_ = json.Unmarshal(first, &p1)
		second := pay(handler, "owner-a", "k1", body)
		var p2 struct{ ID string }
		_ = json.Unmarshal(second, &p2)
		if p1.ID == "" || p1.ID != p2.ID {
			t.Fatalf("replay devolveu IDs diferentes: %s vs %s", p1.ID, p2.ID)
		}
		var count int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM finance.payment WHERE tenant_id=$1 AND idempotency_key='k1'`, ownerA.TenantID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("esperado exatamente um lançamento, obtido %d", count)
		}

		// Sobrepagamento com chave diferente continua rejeitado: a parcela de
		// 100,00 já foi quitada, então um segundo pagamento de 50,00 excede.
		overpay := httptest.NewRequest(http.MethodPost, payments, strings.NewReader(`{"installment_id":"`+installment.ID.String()+`","amount_cents":5000,"method":"pix"}`)).WithContext(ctx)
		overpay.Header.Set("Authorization", "Bearer owner-a")
		overpay.Header.Set("Idempotency-Key", "k-over")
		recOver := httptest.NewRecorder()
		handler.ServeHTTP(recOver, overpay)
		if recOver.Code != http.StatusConflict || !strings.Contains(recOver.Body.String(), "FINANCE_CONFLICT") {
			t.Fatalf("sobrepagamento deveria ser 409, status=%d body=%s", recOver.Code, recOver.Body)
		}

		request(t, handler, http.MethodGet, titles+"/"+title.ID, "owner-b", "", 404, "FINANCE_REFERENCE_NOT_FOUND")
		request(t, handler, http.MethodGet, titles, "auditor", "", 200, "")
	})

	t.Run("parcelas expõem pago e saldo do título", func(t *testing.T) {
		titleBody := request(t, handler, http.MethodPost, titles, "owner-a", `{"direction":"receivable","amount_cents":10000,"due_date":"2026-12-01","status":"open","origin":"sale"}`, 201, "")
		var title struct{ ID string }
		_ = json.Unmarshal(titleBody, &title)
		titleID := uuid.MustParse(title.ID)
		tenantCtx := db.WithTenant(ctx, ownerA.TenantID)
		first, err := repo.CreateInstallment(tenantCtx, finance.Installment{TitleID: titleID, Number: 1, AmountCents: 6000, DueDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), Status: "open"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateInstallment(tenantCtx, finance.Installment{TitleID: titleID, Number: 2, AmountCents: 4000, DueDate: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Status: "open"}); err != nil {
			t.Fatal(err)
		}

		pay := func(key string, amount int64) {
			body := `{"installment_id":"` + first.ID.String() + `","amount_cents":` + strconv.FormatInt(amount, 10) + `,"method":"pix"}`
			req := httptest.NewRequest(http.MethodPost, payments, strings.NewReader(body)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer owner-a")
			req.Header.Set("Idempotency-Key", key)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("pagamento parcial: status=%d body=%s", rec.Code, rec.Body)
			}
		}
		pay("bal-1", 2500)

		detail := request(t, handler, http.MethodGet, titles+"/"+title.ID, "owner-a", "", 200, "")
		var titleDetail struct {
			PaidCents    int64 `json:"paid_cents"`
			BalanceCents int64 `json:"balance_cents"`
		}
		if err := json.Unmarshal(detail, &titleDetail); err != nil {
			t.Fatal(err)
		}
		if titleDetail.PaidCents != 2500 || titleDetail.BalanceCents != 7500 {
			t.Fatalf("saldo do título: %+v", titleDetail)
		}

		list := request(t, handler, http.MethodGet, titles+"/"+title.ID+"/installments", "owner-a", "", 200, "")
		var installments struct {
			Items []struct {
				Number       int   `json:"number"`
				PaidCents    int64 `json:"paid_cents"`
				BalanceCents int64 `json:"balance_cents"`
			} `json:"items"`
		}
		if err := json.Unmarshal(list, &installments); err != nil {
			t.Fatal(err)
		}
		if len(installments.Items) != 2 {
			t.Fatalf("parcelas: %s", list)
		}
		if got := installments.Items[0]; got.Number != 1 || got.PaidCents != 2500 || got.BalanceCents != 3500 {
			t.Fatalf("primeira parcela: %+v", got)
		}
		if got := installments.Items[1]; got.Number != 2 || got.PaidCents != 0 || got.BalanceCents != 4000 {
			t.Fatalf("segunda parcela: %+v", got)
		}

		request(t, handler, http.MethodGet, titles+"/"+title.ID+"/installments", "owner-b", "", 404, "FINANCE_REFERENCE_NOT_FOUND")
		request(t, handler, http.MethodGet, titles+"/"+uuid.NewString()+"/installments", "owner-a", "", 404, "FINANCE_REFERENCE_NOT_FOUND")
	})

	t.Run("referência ausente e entrada inválida têm resultado explícito", func(t *testing.T) {
		missing := httptest.NewRequest(http.MethodPost, payments, strings.NewReader(`{"installment_id":"`+uuid.NewString()+`","amount_cents":10000,"method":"pix"}`)).WithContext(ctx)
		missing.Header.Set("Authorization", "Bearer owner-a")
		missing.Header.Set("Idempotency-Key", "k-missing")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, missing)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "FINANCE_REFERENCE_NOT_FOUND") {
			t.Fatalf("referência ausente: status=%d body=%s", rec.Code, rec.Body)
		}
		request(t, handler, http.MethodPost, titles, "owner-a", `{"amount_cents":10000}`, 400, "VALIDATION_ERROR")
	})
}
