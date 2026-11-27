package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type adminHTTP struct {
	t      *testing.T
	server http.Handler
}

func (a adminHTTP) do(method, path, body, key string) (int, map[string]any) {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer integration")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	a.server.ServeHTTP(rec, req)
	var payload map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			a.t.Fatalf("%s %s: resposta não é JSON: %v\n%s", method, path, err, rec.Body.String())
		}
	}
	return rec.Code, payload
}

func TestAdminPlatformCrossTenantReadsAndWritesIntegration(t *testing.T) {
	dsn := os.Getenv("AMPA_CONTROL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_CONTROL_POSTGRES_DSN não definido; integração do /admin/v1 não executada")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)

	var role string
	var bypass bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatalf("integração exige papel sem SUPERUSER/BYPASSRLS; role=%s", role)
	}

	service, err := entitlement.NewService(database)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewPlatformReader(database)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	handler := NewAdminHandler(reader, service, slog.New(slog.NewJSONHandler(nopWriter{}, nil)))
	read, write := platformauth.PermissionPlatformRead, platformauth.PermissionPlatformWrite
	server := httpserver.New(nil, httpserver.Options{
		PlatformAuthenticator: adminGate(actor),
		PlatformRoutes: []httpserver.PlatformRoute{
			{Pattern: "GET /admin/v1/tenants", Permission: read, Handler: handler.Audited("tenants.list", handler.Tenants)},
			{Pattern: "GET /admin/v1/tenants/{tenantId}/licenses", Permission: read, Handler: handler.Audited("licenses.read", handler.Licenses)},
			{Pattern: "GET /admin/v1/tenants/{tenantId}/entitlement-events", Permission: read, Handler: handler.Audited("events.read", handler.Events)},
			{Pattern: "PUT /admin/v1/tenants/{tenantId}/companies/{companyId}/entitlements/{module}", Permission: write, Handler: handler.Audited("entitlement.grant", handler.Grant)},
			{Pattern: "POST /admin/v1/tenants/{tenantId}/companies/{companyId}/entitlements/{module}/revoke", Permission: write, Handler: handler.Audited("entitlement.revoke", handler.Revoke)},
			{Pattern: "PUT /admin/v1/tenants/{tenantId}/modules/{module}/quota", Permission: write, Handler: handler.Audited("quota.set", handler.SetQuota)},
		},
	})
	api := adminHTTP{t: t, server: server.Handler()}

	tenantA, tenantB := uuid.New(), uuid.New()
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		tenantID := tenantID
		t.Cleanup(func() {
			_ = database.WithTenantTx(db.WithTenant(context.Background(), tenantID), func(ctx context.Context, tx pgx.Tx) error {
				for _, table := range []string{"license_assignment", "license_pool", "user_quota", "mutation_idempotency"} {
					if _, err := tx.Exec(ctx, "DELETE FROM entitlement."+table+" WHERE tenant_id = $1", tenantID); err != nil {
						return err
					}
				}
				return nil
			})
		})
	}
	prefix := func(tenantID uuid.UUID) string { return "/admin/v1/tenants/" + tenantID.String() }
	module := entitlement.ModuleFuneral
	run := uuid.NewString()

	// Cotas: A=2 licenças, B=1 licença, ambas com tenant explícito no caminho.
	if status, body := api.do(http.MethodPut, prefix(tenantA)+"/modules/"+module+"/quota", `{"quota":2,"version":0}`, "q-a-"+run); status != http.StatusOK || body["quota"] != float64(2) {
		t.Fatalf("quota A: status=%d body=%v", status, body)
	}
	if status, body := api.do(http.MethodPut, prefix(tenantB)+"/modules/"+module+"/quota", `{"quota":1,"version":0}`, "q-b-"+run); status != http.StatusOK {
		t.Fatalf("quota B: status=%d body=%v", status, body)
	}

	// Grant com origem admin, Idempotency-Key e replay idêntico.
	c1, c2, c3, cb := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	grant := func(tenantID, company uuid.UUID, key string) (int, map[string]any) {
		return api.do(http.MethodPut, prefix(tenantID)+"/companies/"+company.String()+"/entitlements/"+module, `{"validFrom":"2026-01-01"}`, key)
	}
	status, first := grant(tenantA, c1, "g-c1-"+run)
	if status != http.StatusOK || first["status"] != "active" || first["tenantId"] != tenantA.String() {
		t.Fatalf("grant c1: status=%d body=%v", status, first)
	}
	if status, replay := grant(tenantA, c1, "g-c1-"+run); status != http.StatusOK || replay["id"] != first["id"] || replay["version"] != first["version"] {
		t.Fatalf("replay idempotente divergiu: status=%d body=%v", status, replay)
	}
	if status, body := grant(tenantA, c2, "g-c2-"+run); status != http.StatusOK {
		t.Fatalf("grant c2: status=%d body=%v", status, body)
	}
	if status, body := grant(tenantA, c3, "g-c3-"+run); status != http.StatusConflict || body["code"] != "LICENSE_POOL_EXHAUSTED" {
		t.Fatalf("cota esgotada deveria dar 409: status=%d body=%v", status, body)
	}
	if status, body := grant(tenantB, cb, "g-cb-"+run); status != http.StatusOK {
		t.Fatalf("grant B: status=%d body=%v", status, body)
	}

	// Revoke exige a versão: versão obsoleta é 409, versão correta revoga.
	revokePath := prefix(tenantA) + "/companies/" + c1.String() + "/entitlements/" + module + "/revoke"
	if status, body := api.do(http.MethodPost, revokePath, `{"version":99}`, "r-stale-"+run); status != http.StatusConflict || body["code"] != "STALE_VERSION" {
		t.Fatalf("revoke obsoleto: status=%d body=%v", status, body)
	}
	if status, body := api.do(http.MethodPost, revokePath, fmt.Sprintf(`{"version":%v}`, first["version"]), "r-ok-"+run); status != http.StatusOK || body["status"] != "revoked" {
		t.Fatalf("revoke: status=%d body=%v", status, body)
	}

	// Leitura cross-tenant: os dois tenants aparecem, paginados por cursor.
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		path := "/admin/v1/tenants?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		status, body := api.do(http.MethodGet, path, "", "")
		if status != http.StatusOK {
			t.Fatalf("tenants: status=%d body=%v", status, body)
		}
		items := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("limit=1 devolveu %d itens", len(items))
		}
		seen[items[0].(map[string]any)["tenantId"].(string)] = true
		pages++
		next, _ := body["nextCursor"].(string)
		if next == "" || pages > 50 {
			break
		}
		cursor = next
	}
	if !seen[tenantA.String()] || !seen[tenantB.String()] {
		t.Fatalf("visão cross-tenant sem A ou B: %v", seen)
	}
	if status, body := api.do(http.MethodGet, "/admin/v1/tenants?cursor=%25%25", "", ""); status != http.StatusBadRequest {
		t.Fatalf("cursor inválido: status=%d body=%v", status, body)
	}

	// Licenças do tenant A: cota, uso e licenças próprias, sem dados do B.
	status, licenses := api.do(http.MethodGet, prefix(tenantA)+"/licenses", "", "")
	if status != http.StatusOK {
		t.Fatalf("licenses A: status=%d body=%v", status, licenses)
	}
	pools := licenses["pools"].([]any)
	if len(pools) != 1 {
		t.Fatalf("A deveria ter 1 pool: %v", pools)
	}
	pool := pools[0].(map[string]any)
	if pool["quota"] != float64(2) || pool["used"] != float64(1) || pool["version"].(float64) < 1 {
		t.Fatalf("pool A inesperado: %v", pool)
	}
	assignments := licenses["assignments"].([]any)
	statuses := map[string]string{}
	for _, raw := range assignments {
		item := raw.(map[string]any)
		if item["tenantId"] != tenantA.String() {
			t.Fatalf("vazamento entre tenants: %v", item)
		}
		statuses[item["companyId"].(string)] = item["status"].(string)
	}
	if len(assignments) != 2 || statuses[c1.String()] != "revoked" || statuses[c2.String()] != "active" {
		t.Fatalf("licenças A inesperadas: %v", statuses)
	}
	summary := licenses["tenant"].(map[string]any)
	if summary["assignmentsTotal"] != float64(2) || summary["assignmentsActive"] != float64(1) || summary["quotaTotal"] != float64(2) {
		t.Fatalf("resumo A inesperado: %v", summary)
	}

	// Eventos: auditoria com ator de plataforma e origin=admin, só do tenant A.
	status, events := api.do(http.MethodGet, prefix(tenantA)+"/entitlement-events", "", "")
	if status != http.StatusOK {
		t.Fatalf("eventos A: status=%d body=%v", status, events)
	}
	items := events["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("A deveria ter 3 eventos (2 ativações + 1 revogação; o replay não duplica): %d %v", len(items), items)
	}
	actions := map[string]int{}
	for _, raw := range items {
		event := raw.(map[string]any)
		if event["tenantId"] != tenantA.String() || event["origin"] != entitlement.OriginAdmin || event["actorId"] != actor.String() {
			t.Fatalf("evento sem tenant/origem/ator corretos: %v", event)
		}
		actions[event["action"].(string)]++
	}
	if actions["activated"] != 2 || actions["revoked"] != 1 {
		t.Fatalf("ações inesperadas: %v", actions)
	}
	if first := items[0].(map[string]any); first["action"] != "revoked" {
		t.Fatalf("evento mais recente deveria vir primeiro: %v", first)
	}
	if status, page := api.do(http.MethodGet, prefix(tenantA)+"/entitlement-events?limit=2", "", ""); status != http.StatusOK || len(page["items"].([]any)) != 2 || page["nextCursor"] == nil {
		t.Fatalf("paginação de eventos: status=%d body=%v", status, page)
	}

	// Tenant sem registro: 404 estruturado; id malformado: 400.
	if status, body := api.do(http.MethodGet, prefix(uuid.New())+"/licenses", "", ""); status != http.StatusNotFound || body["code"] != "TENANT_NOT_FOUND" {
		t.Fatalf("tenant desconhecido: status=%d body=%v", status, body)
	}
}

// O papel de aplicação lê só as views: sem SELECT direto nas tabelas de
// entitlement de outro tenant, e as views não expõem colunas de PII.
func TestPlatformViewsAreReadOnlyForAppRole(t *testing.T) {
	dsn := os.Getenv("AMPA_CONTROL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_CONTROL_POSTGRES_DSN não definido; integração do /admin/v1 não executada")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)

	err = database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM reporting.platform_license_pool`)
		return err
	})
	var pgErr *pgconn.PgError
	if err == nil || !errors.As(err, &pgErr) {
		t.Fatalf("escrita em view de plataforma deveria falhar com erro do banco: %v", err)
	}

	err = database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT table_name, column_name FROM information_schema.columns
			 WHERE table_schema = 'reporting' AND table_name LIKE 'platform\_%'`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var table, column string
			if err := rows.Scan(&table, &column); err != nil {
				return err
			}
			for _, forbidden := range []string{"name", "document", "cnpj", "email", "phone", "payload", "idempotency"} {
				if strings.Contains(column, forbidden) {
					t.Errorf("coluna sensível %s.%s em view de plataforma", table, column)
				}
			}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClassifyReportErrorFailsClosed(t *testing.T) {
	for _, code := range []string{"42P01", "42703", "3F000", "42501"} {
		err := classifyReportError(&pgconn.PgError{Code: code})
		if !errors.Is(err, ErrPlatformReportUnavailable) {
			t.Fatalf("%s deveria virar ErrPlatformReportUnavailable: %v", code, err)
		}
	}
	if err := classifyReportError(errors.New("rede")); errors.Is(err, ErrPlatformReportUnavailable) {
		t.Fatal("erro genérico não deve ser classificado como view ausente")
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
