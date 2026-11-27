package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/modules/finance"
	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStoreDashboardHTTPPostgres(t *testing.T) {
	cellDSN, controlDSN := os.Getenv("AMPA_ISSUE79_CELL_ADMIN_DSN"), os.Getenv("AMPA_ISSUE79_CONTROL_ADMIN_DSN")
	if cellDSN == "" || controlDSN == "" {
		t.Skip("requer os bancos descartáveis issue79_cell e issue79_control")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cellAdmin, cell := openStoreTestDatabases(t, ctx, cellDSN, "issue79_cell", "amparius_app", "finance.title", "funeral.schedule")
	controlAdmin, control := openStoreTestDatabases(t, ctx, controlDSN, "issue79_control", "amparius_control_app", "entitlement.license_assignment")
	identities, err := identity.NewRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	events, err := outbox.NewRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	funerals, err := funeral.NewRepository(cell, events)
	if err != nil {
		t.Fatal(err)
	}
	finances, err := finance.NewRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	checker, err := entitlement.NewChecker(control, entitlement.WithCompanyResolver(identities))
	if err != nil {
		t.Fatal(err)
	}

	exec := func(pool *pgxpool.Pool, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	onboard := func(token string) (auth.OIDCIdentity, uuid.UUID) {
		t.Helper()
		result, err := identities.Onboard(ctx, identity.DevelopmentRequest("store79-"+uuid.NewString()))
		if err != nil {
			t.Fatal(err)
		}
		exec(cellAdmin, `UPDATE identity.app_user SET auth_subject=$1,status='active',mfa_enabled=true WHERE id=$2`, token, result.OwnerUserID)
		exec(cellAdmin, `INSERT INTO identity.user_company (tenant_id,user_id,company_id) VALUES ($1,$2,$3)`, result.TenantID, result.OwnerUserID, result.CompanyID)
		return auth.OIDCIdentity{Issuer: "store79", Subject: token, TenantID: result.TenantID, CompanyID: result.CompanyID}, result.OwnerUserID
	}
	a, userA := onboard("a")
	b, _ := onboard("b")
	otherCompany := uuid.New()
	a2, foreign, unlinked := a, a, a
	a2.CompanyID, foreign.CompanyID, unlinked.CompanyID = otherCompany, b.CompanyID, uuid.New()
	for _, id := range []uuid.UUID{otherCompany, unlinked.CompanyID} {
		exec(cellAdmin, `INSERT INTO identity.company (id,tenant_id,document,name,kind,status) VALUES ($1,$2,$3,'Loja sintética','funeral','active')`, id, a.TenantID, id.String())
	}
	exec(cellAdmin, `INSERT INTO identity.user_company (tenant_id,user_id,company_id) VALUES ($1,$2,$3)`, a.TenantID, userA, otherCompany)
	for _, claims := range []auth.OIDCIdentity{a, b} {
		exec(controlAdmin, `INSERT INTO entitlement.license_assignment (tenant_id,company_id,module_code,valid_from,status) VALUES ($1,$2,'funeral','2020-01-01','active')`, claims.TenantID, claims.CompanyID)
	}
	itemsA := seedStoreTestSchedule(t, ctx, cellAdmin, a, true)
	itemsA2 := seedStoreTestSchedule(t, ctx, cellAdmin, a2, false)
	itemsB := seedStoreTestSchedule(t, ctx, cellAdmin, b, false)
	if len(itemsA) != 33 || len(itemsA2) != 1 || len(itemsB) != 1 {
		t.Fatalf("fixtures de agenda inesperadas: %d, %d, %d", len(itemsA), len(itemsA2), len(itemsB))
	}
	for _, fixture := range []struct {
		tenant uuid.UUID
		rows   int
	}{{a.TenantID, 10000}, {b.TenantID, 7}} {
		exec(cellAdmin, `
			INSERT INTO finance.title (tenant_id,direction,amount,due_date,status,origin)
			SELECT $1, CASE WHEN n=2 THEN 'payable' ELSE 'receivable' END, 10.01,
			       CASE WHEN n=3 THEN '2026-11-27'::date ELSE '2026-11-26'::date END,
			       CASE WHEN n=1 THEN 'paid' ELSE 'open' END,'manual'
			FROM generate_series(1,$2) n`, fixture.tenant, fixture.rows)
	}
	exec(cellAdmin, "ANALYZE funeral.schedule")
	exec(cellAdmin, "ANALYZE finance.title")

	err = cell.WithTenantTx(db.WithTenant(ctx, a.TenantID), func(ctx context.Context, tx pgx.Tx) error {
		var titles, schedules int
		err := tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM finance.title WHERE tenant_id=$1),
			(SELECT count(*) FROM funeral.schedule WHERE tenant_id=$1)`, b.TenantID).Scan(&titles, &schedules)
		if err == nil && (titles != 0 || schedules != 0) {
			t.Fatalf("RLS revelou outro tenant: %d títulos, %d agendas", titles, schedules)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	tokens := map[string]auth.OIDCIdentity{"a": a, "a2": a2, "b": b, "foreign": foreign, "unlinked": unlinked}
	oidc := auth.OIDCVerifierFunc(func(_ context.Context, token string) (auth.OIDCIdentity, error) {
		if claims, ok := tokens[token]; ok {
			return claims, nil
		}
		return auth.OIDCIdentity{}, auth.ErrInvalidOIDCToken
	})
	mfa := auth.MFAVerifierFunc(func(context.Context, auth.OIDCIdentity) (bool, error) { return true, nil })
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	policy, err := auth.DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	handlerFor := func(policy auth.RolePolicy, access funeral.AccessChecker) http.Handler {
		t.Helper()
		gate, err := auth.NewService(oidc, mfa, identities, identities, policy, logger)
		if err != nil {
			t.Fatal(err)
		}
		routes := append(finance.AuthenticatedRoutes(finances), funeral.AgendaAuthenticatedRoute(funerals, access))
		return httpserver.New(logger, httpserver.Options{Authenticator: gate, AuthenticatedRoutes: routes}).Handler()
	}
	handler := handlerFor(policy, checker)
	request := func(h http.Handler, path, token string, want int, code string) []byte {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-Company-ID", a2.CompanyID.String())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want || (code != "" && !strings.Contains(rec.Body.String(), `"code":"`+code+`"`)) {
			t.Fatalf("%s (%s): HTTP %d, esperado %d %s: %s", path, token, rec.Code, want, code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}

	const titles = "/v1/finance/titles"
	const agenda = "/v1/funeral/agenda?date=2026-11-26&time_zone=America/Fortaleza"
	restricted, err := auth.NewRolePolicy(map[string][]auth.Permission{"owner": {auth.PermissionPartyRead}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{agenda, titles} {
		request(handler, path, "", 401, "AUTHENTICATION_REQUIRED")
		request(handler, path, "foreign", 403, "SCOPE_FORBIDDEN")
		request(handler, path, "unlinked", 403, "SCOPE_FORBIDDEN")
		request(handlerFor(restricted, checker), path, "a", 403, "PERMISSION_DENIED")
	}
	request(handlerFor(policy, nil), agenda, "a", 503, "ENTITLEMENT_UNAVAILABLE")
	request(handler, agenda, "a2", 403, "ENTITLEMENT_MODULE_NOT_LICENSED")

	type agendaResponse struct {
		Items   []funeral.AgendaItem
		Pending []struct {
			ScheduleID uuid.UUID `json:"schedule_id"`
			Kind       string
		}
	}
	decodeAgenda := func(body []byte) agendaResponse {
		t.Helper()
		var result agendaResponse
		if err := json.Unmarshal(body, &result); err != nil || result.Items == nil || result.Pending == nil {
			t.Fatalf("resposta de agenda inválida: %v", err)
		}
		return result
	}
	assertAgenda := func(token string, ids []uuid.UUID, pendingCount int) {
		t.Helper()
		result := decodeAgenda(request(handler, agenda, token, 200, ""))
		if len(result.Items) != len(ids) || len(result.Pending) != pendingCount {
			t.Fatalf("agenda %s inválida: itens=%d pendências=%d", token, len(result.Items), len(result.Pending))
		}
		for i, item := range result.Items {
			if item.ID != ids[i] || item.TimeZone != "America/Fortaleza" {
				t.Fatalf("agenda %s fora do escopo/ordem: %+v", token, item)
			}
		}
		j := 0
		for _, item := range result.Items {
			if item.Status == "scheduled" {
				pending := result.Pending[j]
				if pending.ScheduleID != item.ID || pending.Kind != "schedule_confirmation" {
					t.Fatalf("pendência não deriva da agenda: %+v", pending)
				}
				j++
			}
		}
	}
	exec(controlAdmin, `INSERT INTO entitlement.license_assignment (tenant_id,company_id,module_code,valid_from,status) VALUES ($1,$2,'funeral','2020-01-01','active')`, a2.TenantID, a2.CompanyID)
	assertAgenda("a", itemsA, 8)
	assertAgenda("a2", itemsA2, 1)
	assertAgenda("b", itemsB, 1)
	emptyAgenda := decodeAgenda(request(handler, "/v1/funeral/agenda?date=2026-12-01&end_date=2026-12-03&time_zone=America/Fortaleza", "a", 200, ""))
	if len(emptyAgenda.Items) != 0 || len(emptyAgenda.Pending) != 0 {
		t.Fatal("agenda e pendências devem estar vazias fora dos agendamentos")
	}
	request(handler, agenda+"&company_id="+a2.CompanyID.String(), "a", 400, "VALIDATION_ERROR")
	interval := decodeAgenda(request(handler, agenda+"&end_date=2026-11-29", "a", 200, ""))
	if len(interval.Items) != 34 || len(interval.Pending) != 9 || interval.Items[33].StartsAt.Format(time.RFC3339) != "2026-11-27T03:00:00Z" {
		t.Fatal("o intervalo civil não incluiu o agendamento no início do segundo dia")
	}

	type titlePage struct {
		Items []struct {
			ID          uuid.UUID
			Status      string
			Direction   string
			DueDate     string `json:"due_date"`
			AmountCents int64  `json:"amount_cents"`
		}
		NextCursor string `json:"next_cursor"`
	}
	list := func(path, token string) titlePage {
		t.Helper()
		var page titlePage
		if err := json.Unmarshal(request(handler, path, token, 200, ""), &page); err != nil || page.Items == nil {
			t.Fatalf("página inválida: %v", err)
		}
		return page
	}
	const filtered = titles + "?status=open&direction=receivable&due_from=2026-11-26&due_to=2026-11-26"
	first := list(filtered+"&limit=2", "a")
	second := list(filtered+"&limit=2&cursor="+first.NextCursor, "a")
	var ids []uuid.UUID
	for _, page := range []titlePage{first, second} {
		if len(page.Items) != 2 || page.NextCursor == "" {
			t.Fatalf("página sem itens ou cursor: %+v", page)
		}
		for _, item := range page.Items {
			if item.Status != "open" || item.Direction != "receivable" || item.DueDate != "2026-11-26" || item.AmountCents != 1001 {
				t.Fatalf("filtros ou valor incorretos: %+v", item)
			}
			if len(ids) > 0 && ids[len(ids)-1].String() >= item.ID.String() {
				t.Fatal("ordem instável ou duplicada em títulos com o mesmo vencimento")
			}
			ids = append(ids, item.ID)
		}
	}
	tenantB := list(filtered, "b")
	if len(tenantB.Items) != 4 || tenantB.NextCursor != "" || len(list(titles, "a2").Items) != 100 {
		t.Fatal("escopo tenant, fim da paginação ou limite legado incorreto")
	}
	for _, item := range tenantB.Items {
		if slices.Contains(ids, item.ID) {
			t.Fatal("título de outro tenant na resposta")
		}
	}
	acrossDates := list(titles+"?limit=6", "b")
	lastPage := list(titles+"?limit=6&cursor="+acrossDates.NextCursor, "b")
	if len(acrossDates.Items) != 6 || acrossDates.Items[5].DueDate != "2026-11-26" ||
		len(lastPage.Items) != 1 || lastPage.Items[0].DueDate != "2026-11-27" || lastPage.NextCursor != "" {
		t.Fatal("cursor não avançou para o próximo vencimento ou não encerrou a paginação")
	}
	if p := list(titles+"?status=cancelled", "a"); len(p.Items) != 0 || p.NextCursor != "" {
		t.Fatal("listagem sem títulos deve ser vazia e sem cursor")
	}
	for _, query := range []string{"status=overdue", "direction=invalid", "due_from=2026-02-30", "due_from=2026-11-27&due_to=2026-11-26", "limit=0", "cursor=malformado", "status=open&status=paid", "company_id=externa"} {
		request(handler, titles+"?"+query, "a", 400, "VALIDATION_ERROR")
	}
	benchFirst := list(filtered+"&limit=50", "a")
	t.Log("fixtures: 10007 títulos em dois tenants, 3037 agendamentos em três empresas")
	for _, workload := range []struct{ name, path string }{
		{"títulos página 2/50", filtered + "&limit=50&cursor=" + benchFirst.NextCursor},
		{"agenda diária, 33 itens", agenda},
	} {
		const warmup, samples = 10, 100
		times := make([]time.Duration, 0, samples)
		for i := -warmup; i < samples; i++ {
			start := time.Now()
			request(handler, workload.path, "a", 200, "")
			if i >= 0 {
				times = append(times, time.Since(start))
			}
		}
		slices.Sort(times)
		t.Logf("%s, %d requisições após %d aquecimentos, p50=%s p95=%s max=%s", workload.name, samples, warmup, times[49], times[94], times[99])
		if times[94] > 100*time.Millisecond {
			t.Errorf("%s excedeu 100ms de p95 local", workload.name)
		}
	}
	control.Close()
	request(handler, agenda, "a", 503, "ENTITLEMENT_UNAVAILABLE")
}

func openStoreTestDatabases(t *testing.T, ctx context.Context, dsn, database, role string, tables ...string) (*pgxpool.Pool, *db.DB) {
	t.Helper()
	address, err := url.Parse(dsn)
	if err != nil || address.Hostname() != "127.0.0.1" || address.Path != "/"+database || address.RawQuery != "sslmode=disable" {
		t.Fatal("use somente os bancos descartáveis da #79 em 127.0.0.1")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	address.User = url.User(role)
	restricted, err := db.New(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restricted.Close)
	err = restricted.WithTenantTx(db.WithTenant(ctx, uuid.New()), func(ctx context.Context, tx pgx.Tx) error {
		for _, table := range tables {
			var user string
			var isolated bool
			if err := tx.QueryRow(ctx, `SELECT current_user,
				NOT rolsuper AND NOT rolbypassrls AND row_security_active($1::regclass)
				FROM pg_roles WHERE rolname=current_user`, table).Scan(&user, &isolated); err != nil {
				return err
			}
			if user != role || !isolated {
				t.Fatalf("RLS não efetiva em %s com %s", table, user)
			}
			t.Logf("%s: %s com NOSUPERUSER, NOBYPASSRLS e RLS ativa", table, user)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return admin, restricted
}

func seedStoreTestSchedule(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claims auth.OIDCIdentity, full bool) []uuid.UUID {
	t.Helper()
	tenant, company := claims.TenantID, claims.CompanyID
	partyID, deceasedID, contractID, locationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO party.party (id,tenant_id,kind,name,status) VALUES ($1,$2,'person','Pessoa sintética','active')`, partyID, tenant)
	exec(`INSERT INTO party.person (party_id,tenant_id,gender) VALUES ($1,$2,'unspecified')`, partyID, tenant)
	exec(`INSERT INTO party.deceased (id,tenant_id,person_id,death_at) VALUES ($1,$2,$3,'2026-11-25T06:00:00Z')`, deceasedID, tenant, partyID)
	exec(`INSERT INTO funeral.contract (id,tenant_id,company_id,customer_party_id,deceased_id,status,total_amount,idempotency_key,request_hash)
		VALUES ($1,$2,$3,$4,$5,'active',1,$1::uuid::text,repeat('a',64))`, contractID, tenant, company, partyID, deceasedID)
	exec(`INSERT INTO funeral.location (id,tenant_id,company_id,name,kind,time_zone,idempotency_key,request_hash)
		VALUES ($1,$2,$3,'Capela sintética','wake','America/Fortaleza',$1::uuid::text,repeat('a',64))`, locationID, tenant, company)
	const insert = `INSERT INTO funeral.schedule
		(tenant_id,company_id,contract_id,location_id,kind,time_zone,starts_at,ends_at,status,idempotency_key,request_hash)
		SELECT $1,$2,$3,$4,'wake','America/Fortaleza',starts_at,ends_at,status,gen_random_uuid()::text,repeat('a',64)
		FROM `
	const start = "'2026-11-26T03:00:00Z'::timestamptz"
	if full {
		exec(insert+`(SELECT `+start+` - n * interval '1 day' AS starts_at,
			`+start+` - n * interval '1 day' + interval '10 minutes' AS ends_at,
			'completed' AS status FROM generate_series(1,3000) n) history`, tenant, company, contractID, locationID)
	}
	dailyItems := 0
	if full {
		dailyItems = 31
	}
	exec(insert+`(SELECT `+start+` + n * interval '15 minutes' AS starts_at,
		`+start+` + n * interval '15 minutes' + interval '10 minutes' AS ends_at,
		(ARRAY['scheduled','confirmed','completed','cancelled'])[1+(n%4)] AS status
		FROM generate_series(0,$5) n) day_entries`, tenant, company, contractID, locationID, dailyItems)
	if full {
		exec(insert+`(VALUES
			(`+start+`-interval '30 minutes',`+start+`,'confirmed'),
			(`+start+`-interval '2 minutes',`+start+`+interval '2 minutes','completed'),
			(`+start+`+interval '1 day',`+start+`+interval '1 day 1 hour','scheduled')
		) AS boundaries(starts_at,ends_at,status)`, tenant, company, contractID, locationID)
	}
	rows, err := pool.Query(ctx, `SELECT id FROM funeral.schedule WHERE tenant_id=$1 AND company_id=$2
		AND ends_at > '2026-11-26T03:00:00Z' AND starts_at < '2026-11-27T03:00:00Z' ORDER BY starts_at,id`, tenant, company)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}
