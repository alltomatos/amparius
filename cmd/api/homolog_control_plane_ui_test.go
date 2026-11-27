package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/config"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Homologação do control plane (/v1/context, /v1/companies, /v1/.../entitlements)
// com a fiação REAL de cmd/api e uma identidade de TESTE.
//
// A identidade de teste só existe neste arquivo _test.go: ela é injetada por
// handlerOption e nunca entra no binário. OIDC/MFA reais continuam adiados; o
// binário sem autenticador responde 503 (provado em
// TestControlPlaneSemAutenticadorRespondeMatrizDeStatus).
//
// Requer PostgreSQL DESCARTÁVEL (scripts/homolog-control-plane-ui.ps1):
// AMPA_TEST_POSTGRES_DSN / AMPA_TEST_POSTGRES_ADMIN_DSN (célula) e
// AMPA_CONTROL_POSTGRES_DSN (control plane).

const homologToken = "homolog-ui-token"

// homologGate é a identidade de teste: um único bearer fixo mapeado para um
// principal com MFA já satisfeito. Não valida assinatura de nada.
type homologGate struct{ principal auth.Principal }

func (g homologGate) Authenticate(_ context.Context, header string) (auth.Principal, error) {
	if header != "Bearer "+homologToken {
		return auth.Principal{}, auth.ErrInvalidBearer
	}
	return g.principal, nil
}

func (homologGate) Allows(_ auth.Principal, permission auth.Permission) bool {
	switch permission {
	case auth.PermissionContextRead, auth.PermissionEntitlementRead, auth.PermissionEntitlementWrite:
		return true
	}
	return false
}

type homologFixture struct {
	handler   http.Handler
	closeAll  func()
	principal auth.Principal
	foreign   auth.Principal // outro tenant: a empresa dele não pode aparecer
	cleanup   func()
}

func newHomologFixture(t testing.TB) *homologFixture {
	t.Helper()
	appDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	controlDSN := os.Getenv("AMPA_CONTROL_POSTGRES_DSN")
	if appDSN == "" || adminDSN == "" || controlDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN, AMPA_TEST_POSTGRES_ADMIN_DSN e AMPA_CONTROL_POSTGRES_DSN são necessários")
	}
	ctx := context.Background()
	cell, err := db.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	control, err := db.New(ctx, controlDSN)
	if err != nil {
		cell.Close()
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		cell.Close()
		control.Close()
		t.Fatal(err)
	}
	repo, err := identity.NewRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := repo.Onboard(ctx, identity.DevelopmentRequest("homolog-ui-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.Onboard(ctx, identity.DevelopmentRequest("homolog-ui-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenants := []uuid.UUID{owner.TenantID, other.TenantID}
	if _, err := admin.Exec(ctx, `INSERT INTO identity.user_company (tenant_id, user_id, company_id) VALUES ($1, $2, $3), ($4, $5, $6)`,
		owner.TenantID, owner.OwnerUserID, owner.CompanyID, other.TenantID, other.OwnerUserID, other.CompanyID); err != nil {
		t.Fatal(err)
	}
	service, err := entitlement.NewService(control)
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{entitlement.ModuleFuneral, entitlement.ModuleCemetery} {
		if err := service.SetModuleQuota(db.WithTenant(ctx, owner.TenantID), module, 2); err != nil {
			t.Fatal(err)
		}
	}
	from := time.Now().UTC().Truncate(24 * time.Hour)
	if _, err := service.Activate(db.WithTenant(ctx, owner.TenantID), uuid.New(), entitlement.ActivateInput{
		ModuleCode: entitlement.ModuleFuneral, CompanyID: owner.CompanyID, Origin: entitlement.OriginAdmin, ValidFrom: &from,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Env:              "production",
		PostgresDSN:      appDSN,
		ControlPlaneDSN:  controlDSN,
		RedisAddr:        "127.0.0.1:1",
		RabbitMQURL:      "amqp://amparius:amparius@127.0.0.1:1/",
		ReadinessTimeout: 500 * time.Millisecond,
	}
	principal := auth.Principal{Issuer: "homolog", Subject: "homolog-owner", UserID: owner.OwnerUserID, TenantID: owner.TenantID, CompanyID: owner.CompanyID, MFAAuthenticated: true}
	handler, closeAll, err := buildHandler(ctx, cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil,
		func(options *httpserver.Options) { options.Authenticator = homologGate{principal: principal} })
	if err != nil {
		t.Fatal(err)
	}
	fixture := &homologFixture{
		handler:   handler,
		closeAll:  closeAll,
		principal: principal,
		foreign:   auth.Principal{TenantID: other.TenantID, CompanyID: other.CompanyID, UserID: other.OwnerUserID},
	}
	fixture.cleanup = func() {
		fixture.closeAll()
		for _, tenantID := range tenants {
			cleanupCtx := db.WithTenant(context.Background(), tenantID)
			_ = control.WithTenantTx(cleanupCtx, func(ctx context.Context, tx pgx.Tx) error {
				for _, table := range []string{"entitlement_event", "quota_event", "mutation_idempotency", "license_assignment", "license_pool"} {
					_, _ = tx.Exec(ctx, "DELETE FROM entitlement."+table+" WHERE tenant_id = $1", tenantID)
				}
				return nil
			})
		}
		for _, statement := range []string{
			"DELETE FROM identity.user_company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.onboarding_request WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.user_role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.app_user WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.establishment WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
		} {
			if _, err := admin.Exec(context.Background(), statement, tenants); err != nil {
				t.Logf("cleanup %q: %v", statement, err)
			}
		}
		admin.Close()
		control.Close()
		cell.Close()
	}
	t.Cleanup(fixture.cleanup)
	return fixture
}

func (f *homologFixture) do(method, path, body, token string, headers map[string]string) (int, map[string]any) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	var payload map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	}
	return rec.Code, payload
}

// TestControlPlaneHTTPComIdentidadeDeTeste exercita, com PostgreSQL real e a
// fiação real do cmd/api, os contratos consumidos por web/control-plane.
func TestControlPlaneHTTPComIdentidadeDeTeste(t *testing.T) {
	f := newHomologFixture(t)
	company := f.principal.CompanyID.String()
	base := "/v1/companies/" + company + "/entitlements"

	if status, body := f.do(http.MethodGet, "/v1/context", "", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("sem credencial: %d %v, esperado 401", status, body)
	}
	if status, body := f.do(http.MethodGet, "/v1/context", "", "token-errado", nil); status != http.StatusUnauthorized {
		t.Fatalf("credencial inválida: %d %v, esperado 401", status, body)
	}
	status, ctxBody := f.do(http.MethodGet, "/v1/context", "", homologToken, nil)
	if status != http.StatusOK {
		t.Fatalf("/v1/context: %d %v", status, ctxBody)
	}
	if got := ctxBody["company"].(map[string]any)["id"]; got != company {
		t.Fatalf("contexto devolveu empresa %v, esperado %s", got, company)
	}

	status, companies := f.do(http.MethodGet, "/v1/companies", "", homologToken, nil)
	if status != http.StatusOK {
		t.Fatalf("/v1/companies: %d %v", status, companies)
	}
	items := companies["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != company {
		t.Fatalf("empresas visíveis = %v; esperado só a do tenant autenticado (RLS/escopo)", items)
	}

	// Empresa de outro tenant: nunca é lida nem alterada.
	foreignPath := "/v1/companies/" + f.foreign.CompanyID.String() + "/entitlements"
	if status, body := f.do(http.MethodGet, foreignPath, "", homologToken, nil); status == http.StatusOK {
		t.Fatalf("empresa de outro tenant legível: %d %v", status, body)
	}

	status, list := f.do(http.MethodGet, base, "", homologToken, nil)
	if status != http.StatusOK {
		t.Fatalf("entitlements: %d %v", status, list)
	}
	states := map[string]string{}
	for _, raw := range list["items"].([]any) {
		item := raw.(map[string]any)
		states[item["module"].(string)], _ = item["status"].(string)
	}
	if states[entitlement.ModuleFuneral] != "active" || states[entitlement.ModuleCemetery] == "active" || states[entitlement.ModuleFinance] != "active" {
		t.Fatalf("estados iniciais inesperados: %v", states)
	}

	// Conceder (PUT) cemitério com Idempotency-Key; replay devolve o mesmo.
	grantKey := map[string]string{"Idempotency-Key": "grant-" + uuid.NewString()}
	grantBody := `{"validFrom":"2026-01-01","version":0}`
	status, granted := f.do(http.MethodPut, base+"/"+entitlement.ModuleCemetery, grantBody, homologToken, grantKey)
	if status != http.StatusOK || granted["status"] != "active" {
		t.Fatalf("conceder: %d %v", status, granted)
	}
	if status, replay := f.do(http.MethodPut, base+"/"+entitlement.ModuleCemetery, grantBody, homologToken, grantKey); status != http.StatusOK || replay["version"] != granted["version"] {
		t.Fatalf("replay idempotente divergiu: %d %v", status, replay)
	}
	if status, body := f.do(http.MethodPut, base+"/"+entitlement.ModuleCemetery, grantBody, homologToken, nil); status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		t.Fatalf("PUT sem Idempotency-Key deveria ser recusado: %d %v", status, body)
	}

	// Revogar (PATCH) exige a versão atual: obsoleta = 409.
	version := granted["version"]
	if status, body := f.do(http.MethodPatch, base+"/"+entitlement.ModuleCemetery, `{"status":"revoked","version":999}`, homologToken, map[string]string{"Idempotency-Key": "rv-stale-" + uuid.NewString()}); status != http.StatusConflict || body["code"] != "STALE_VERSION" {
		t.Fatalf("revogar com versão obsoleta: %d %v", status, body)
	}
	status, revoked := f.do(http.MethodPatch, base+"/"+entitlement.ModuleCemetery, fmt.Sprintf(`{"status":"revoked","version":%v}`, version), homologToken, map[string]string{"Idempotency-Key": "rv-" + uuid.NewString()})
	if status != http.StatusOK || revoked["status"] != "revoked" {
		t.Fatalf("revogar: %d %v", status, revoked)
	}

	// A cota é versionada à parte da licença: o detalhe expõe quotaVersion e é
	// ela (não version) que o PATCH de quota exige. Defeito achado na
	// homologação da UI: a tela enviava a versão da licença e tomava 409.
	status, detail := f.do(http.MethodGet, base+"/"+entitlement.ModuleCemetery, "", homologToken, nil)
	if status != http.StatusOK {
		t.Fatalf("detalhe: %d %v", status, detail)
	}
	quotaVersion, ok := detail["quotaVersion"].(float64)
	if !ok || quotaVersion < 1 || quotaVersion == detail["version"].(float64) {
		t.Fatalf("o teste precisa de quotaVersion ≥ 1 distinta de version: %v", detail)
	}
	if status, body := f.do(http.MethodPatch, base+"/"+entitlement.ModuleCemetery, fmt.Sprintf(`{"quota":1,"version":%v}`, detail["version"]), homologToken, map[string]string{"Idempotency-Key": "q-stale-" + uuid.NewString()}); status != http.StatusConflict || body["code"] != "STALE_VERSION" {
		t.Fatalf("quota com a versão da licença deveria dar 409: %d %v", status, body)
	}
	status, quota := f.do(http.MethodPatch, base+"/"+entitlement.ModuleCemetery, fmt.Sprintf(`{"quota":3,"version":%v}`, quotaVersion), homologToken, map[string]string{"Idempotency-Key": "q-ok-" + uuid.NewString()})
	if status != http.StatusOK || quota["quota"] != float64(3) || quota["version"] != quotaVersion+1 {
		t.Fatalf("ajustar quota: %d %v", status, quota)
	}
	if _, after := f.do(http.MethodGet, base+"/"+entitlement.ModuleCemetery, "", homologToken, nil); after["quotaVersion"] != quotaVersion+1 || after["quota"] != float64(3) {
		t.Fatalf("detalhe não refletiu a nova cota: %v", after)
	}

	// Auditoria registra conceder e revogar com origem admin.
	status, audit := f.do(http.MethodGet, base+"/"+entitlement.ModuleCemetery+"/audit", "", homologToken, nil)
	if status != http.StatusOK {
		t.Fatalf("auditoria: %d %v", status, audit)
	}
	actions := []string{}
	for _, raw := range audit["items"].([]any) {
		actions = append(actions, raw.(map[string]any)["action"].(string))
	}
	if len(actions) < 2 {
		t.Fatalf("auditoria deveria ter conceder e revogar: %v", actions)
	}

	// Módulo core é imutável.
	if status, body := f.do(http.MethodPatch, base+"/"+entitlement.ModuleFinance, `{"status":"revoked","version":0}`, homologToken, map[string]string{"Idempotency-Key": "core-" + uuid.NewString()}); status == http.StatusOK {
		t.Fatalf("módulo core foi revogado: %d %v", status, body)
	}
}

// TestControlPlaneSemAutenticadorRespondeMatrizDeStatus fixa a divergência
// 404 × 503 observada na homologação de 2026-11-27: o status depende de a rota
// ter sido MONTADA, e a rota só é montada com AMPA_CONTROL_POSTGRES_DSN.
func TestControlPlaneSemAutenticadorRespondeMatrizDeStatus(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1/context"},
		{http.MethodGet, "/v1/companies"},
		{http.MethodGet, "/v1/companies/" + uuid.NewString() + "/entitlements"},
		{http.MethodGet, "/admin/v1/tenants"},
	}
	cfg := config.Config{
		Env:              "production",
		PostgresDSN:      "postgres://amparius:amparius@127.0.0.1:1/amparius?sslmode=disable&connect_timeout=1",
		RedisAddr:        "127.0.0.1:1",
		RabbitMQURL:      "amqp://amparius:amparius@127.0.0.1:1/",
		ReadinessTimeout: 500 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Sem control plane configurado: as rotas não existem (404), e isso é o
	// contrato (ADR 0038: "registra somente quando AMPA_CONTROL_POSTGRES_DSN").
	handler, closeHandler, err := buildHandler(ctx, cfg, logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHandler()
	for _, route := range routes {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("sem control plane, %s %s = %d, esperado 404 (rota não montada)", route.method, route.path, rec.Code)
		}
	}
}

// TestControlPlaneConfiguradoSemAutenticadorFalhaFechado503 é o outro lado da
// matriz: com o control plane configurado e SEM autenticador (estado real de
// produção enquanto OIDC/MFA não existe), toda rota montada responde 503.
func TestControlPlaneConfiguradoSemAutenticadorFalhaFechado503(t *testing.T) {
	controlDSN := os.Getenv("AMPA_CONTROL_POSTGRES_DSN")
	appDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if controlDSN == "" || appDSN == "" {
		t.Skip("AMPA_CONTROL_POSTGRES_DSN e AMPA_TEST_POSTGRES_DSN são necessários")
	}
	cfg := config.Config{
		Env:              "production",
		PostgresDSN:      appDSN,
		ControlPlaneDSN:  controlDSN,
		RedisAddr:        "127.0.0.1:1",
		RabbitMQURL:      "amqp://amparius:amparius@127.0.0.1:1/",
		ReadinessTimeout: 500 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	handler, closeHandler, err := buildHandler(ctx, cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHandler()
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/context"},
		{http.MethodGet, "/v1/companies"},
		{http.MethodGet, "/v1/companies/" + uuid.NewString() + "/entitlements"},
		{http.MethodPut, "/v1/companies/" + uuid.NewString() + "/entitlements/cemetery"},
		{http.MethodGet, "/admin/v1/tenants"},
		{http.MethodGet, "/admin/v1/tenants/" + uuid.NewString() + "/licenses"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"AUTHENTICATION_UNAVAILABLE"`) {
			t.Fatalf("%s %s = %d %s, esperado 503 AUTHENTICATION_UNAVAILABLE", route.method, route.path, rec.Code, rec.Body)
		}
	}
}

// TestHomologControlPlaneUIServe serve web/control-plane + API (identidade de
// teste, dados sintéticos, banco descartável) para homologar a UI no
// navegador. Só roda com AMPA_HOMOLOG_UI_ADDR (ex.: 127.0.0.1:18095) e termina
// quando o arquivo apontado por AMPA_HOMOLOG_UI_STOP existe (ou em 30 min).
func TestHomologControlPlaneUIServe(t *testing.T) {
	addr := os.Getenv("AMPA_HOMOLOG_UI_ADDR")
	if addr == "" {
		t.Skip("AMPA_HOMOLOG_UI_ADDR não definido; servidor de homologação da UI não iniciado")
	}
	f := newHomologFixture(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(root, "web", "control-plane", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`<script>window.AMPARIUS_CONFIG = { apiBase: "", accessToken: %q };</script>`, homologToken)
	page := strings.Replace(string(index), `<script src="./helpers.js"`, config+"\n    <script src=\"./helpers.js\"", 1)
	if page == string(index) {
		t.Fatal("não encontrei o ponto de injeção da configuração em index.html")
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/", f.handler)
	mux.Handle("/admin/", f.handler)
	mux.HandleFunc("/web/control-plane/{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("/web/control-plane/index.html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	})
	mux.Handle("/web/", http.FileServer(http.Dir(root)))
	mux.Handle("/assets/", http.FileServer(http.Dir(root)))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.ListenAndServe() }()
	t.Cleanup(func() { _ = server.Close() })

	t.Logf("HOMOLOG UI PRONTA: http://%s/web/control-plane/index.html company=%s", addr, f.principal.CompanyID)
	stop := os.Getenv("AMPA_HOMOLOG_UI_STOP")
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		if stop != "" {
			if _, err := os.Stat(stop); err == nil {
				return
			}
		}
		time.Sleep(time.Second)
	}
}
