package funeral_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type failingAppender struct {
	delegate    funeral.EventAppender
	appendFirst bool
	cause       error
}

func (f failingAppender) AppendTx(ctx context.Context, tx pgx.Tx, event outbox.Event) (outbox.Event, error) {
	if f.appendFirst {
		if _, err := f.delegate.AppendTx(ctx, tx, event); err != nil {
			return outbox.Event{}, err
		}
	}
	return outbox.Event{}, f.cause
}

type atomicityFixture struct {
	admin     *pgxpool.Pool
	database  *db.DB
	events    *outbox.Repository
	tenant    uuid.UUID
	company   uuid.UUID
	customer  uuid.UUID
	deceased  uuid.UUID
	family    uuid.UUID
	item      uuid.UUID
	tenantIDs []uuid.UUID
}

func newAtomicityFixture(t *testing.T) *atomicityFixture {
	t.Helper()
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	var superuser, bypass bool
	var role string
	if err := admin.QueryRow(ctx, `SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&role, &superuser, &bypass); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	database, err := db.New(ctx, dsn)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper, rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &superuser, &bypass)
	}); err != nil {
		database.Close()
		admin.Close()
		t.Fatal(err)
	}
	if superuser || bypass {
		database.Close()
		admin.Close()
		t.Fatalf("teste precisa de role NOSUPERUSER/NOBYPASSRLS; role=%s superuser=%v bypass=%v", role, superuser, bypass)
	}
	events, err := outbox.NewRepository(database)
	if err != nil {
		database.Close()
		admin.Close()
		t.Fatal(err)
	}
	fixture := &atomicityFixture{admin: admin, database: database, events: events}
	fixture.tenant, fixture.company = uuid.New(), uuid.New()
	fixture.customer, fixture.deceased, fixture.family, fixture.item = uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fixture.tenantIDs = []uuid.UUID{fixture.tenant}
	if _, err := admin.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, fixture.tenant, "Atomicidade "+fixture.tenant.String(), "atomic-"+fixture.tenant.String()); err != nil {
		fixture.close(t)
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO identity.company (id,tenant_id,document,name,kind,status) VALUES ($1,$2,$3,'Empresa Funeral','funeral','active')`, fixture.company, fixture.tenant, "company-"+fixture.company.String()); err != nil {
		fixture.close(t)
		t.Fatal(err)
	}
	for _, partyID := range []uuid.UUID{fixture.customer, fixture.deceased} {
		if _, err := admin.Exec(ctx, `INSERT INTO party.party (id,tenant_id,kind,name,status) VALUES ($1,$2,'person','Pessoa de teste','active')`, partyID, fixture.tenant); err != nil {
			fixture.close(t)
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `INSERT INTO party.person (party_id,tenant_id,gender) VALUES ($1,$2,'unspecified')`, partyID, fixture.tenant); err != nil {
			fixture.close(t)
			t.Fatal(err)
		}
	}
	if _, err := admin.Exec(ctx, `INSERT INTO party.deceased (id,tenant_id,person_id,death_at) VALUES ($1,$2,$3,now())`, fixture.deceased, fixture.tenant, fixture.deceased); err != nil {
		fixture.close(t)
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO party.family_link (id,tenant_id,deceased_id,person_id,relation) VALUES ($1,$2,$3,$4,'child')`, fixture.family, fixture.tenant, fixture.deceased, fixture.customer); err != nil {
		fixture.close(t)
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO catalog.catalog_item (id,tenant_id,kind,name,unit,active) VALUES ($1,$2,'service','Servico funerario','service',true)`, fixture.item, fixture.tenant); err != nil {
		fixture.close(t)
		t.Fatal(err)
	}
	t.Cleanup(func() { fixture.close(t) })
	return fixture
}

func (f *atomicityFixture) close(t *testing.T) {
	t.Helper()
	defer f.admin.Close()
	defer f.database.Close()
	ctx := context.Background()
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, statement := range []string{
		"ALTER TABLE funeral.contract_status_history DISABLE TRIGGER contract_status_append_only",
		"ALTER TABLE funeral.contract_family_member DISABLE TRIGGER contract_family_append_only",
		"ALTER TABLE funeral.contract_item DISABLE TRIGGER contract_item_append_only",
		"ALTER TABLE funeral.contract DISABLE TRIGGER contract_history_preserve",
		"ALTER TABLE outbox.event_attempt DISABLE TRIGGER event_attempt_append_only",
		"ALTER TABLE outbox.event DISABLE TRIGGER event_history_preserve",
		"DELETE FROM funeral.contract_status_history WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM funeral.contract_family_member WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM funeral.contract_item WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM funeral.contract WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM outbox.event_attempt WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM outbox.event WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM party.family_link WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM party.deceased WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM party.person WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM party.party WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM catalog.catalog_item WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
		"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
		"ALTER TABLE funeral.contract_status_history ENABLE TRIGGER contract_status_append_only",
		"ALTER TABLE funeral.contract_family_member ENABLE TRIGGER contract_family_append_only",
		"ALTER TABLE funeral.contract_item ENABLE TRIGGER contract_item_append_only",
		"ALTER TABLE funeral.contract ENABLE TRIGGER contract_history_preserve",
		"ALTER TABLE outbox.event_attempt ENABLE TRIGGER event_attempt_append_only",
		"ALTER TABLE outbox.event ENABLE TRIGGER event_history_preserve",
	} {
		if strings.HasPrefix(statement, "ALTER TABLE") {
			_, err = tx.Exec(ctx, statement)
		} else {
			_, err = tx.Exec(ctx, statement, f.tenantIDs)
		}
		if err != nil {
			t.Errorf("cleanup 0025: %v", err)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Errorf("cleanup 0025: %v", err)
	}
}

func (f *atomicityFixture) ctx() context.Context {
	return db.WithCompany(db.WithTenant(context.Background(), f.tenant), f.company)
}

func (f *atomicityFixture) input(key string) funeral.ContractCreate {
	return funeral.ContractCreate{
		CustomerPartyID: f.customer,
		DeceasedID:      f.deceased,
		FamilyLinkIDs:   []uuid.UUID{f.family},
		Items:           []funeral.ContractItemCreate{{CatalogItemID: f.item, Quantity: 1, UnitAmountCents: 15000}},
		IdempotencyKey:  key,
	}
}

func (f *atomicityFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := f.database.WithTenantTx(db.WithTenant(context.Background(), f.tenant), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, query, args...).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestFuneralContractEventAtomicityAndPayloadPrivacy cobre o OUT-01A no
// agregado que já publica evento: dado e evento na mesma transação, rollback
// dos dois lados e payload sem PII.
func TestFuneralContractEventAtomicityAndPayloadPrivacy(t *testing.T) {
	fixture := newAtomicityFixture(t)
	ctx := fixture.ctx()

	repository, err := funeral.NewRepository(fixture.database, fixture.events)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateContract(ctx, fixture.input("atomic-ok"))
	if err != nil {
		t.Fatalf("criar contrato: %v", err)
	}
	if contracts := fixture.count(t, `SELECT count(*) FROM funeral.contract WHERE tenant_id=$1`, fixture.tenant); contracts != 1 {
		t.Fatalf("contratos = %d, esperado 1", contracts)
	}
	if events := fixture.count(t, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1 AND aggregate_id=$2`, fixture.tenant, created.ID); events != 1 {
		t.Fatalf("eventos = %d, esperado 1", events)
	}
	var payload string
	if err := fixture.database.WithTenantTx(db.WithTenant(context.Background(), fixture.tenant), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT payload::text FROM outbox.event WHERE tenant_id=$1 AND aggregate_id=$2 AND event_type='funeral.contract.created'`, fixture.tenant, created.ID).Scan(&payload)
	}); err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal([]byte(payload), &keys); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]struct{}{"contract_id": {}, "status": {}, "customer_party_id": {}, "deceased_id": {}, "total_amount_cents": {}}
	for key := range keys {
		if _, ok := allowed[key]; !ok {
			t.Fatalf("payload do evento tem chave inesperada %q: %s", key, payload)
		}
	}
	if len(keys) != len(allowed) {
		t.Fatalf("payload do evento incompleto: %s", payload)
	}

	// Replay idempotente não duplica contrato nem evento.
	if _, err := repository.CreateContract(ctx, fixture.input("atomic-ok")); err != nil {
		t.Fatalf("replay idempotente: %v", err)
	}
	if contracts := fixture.count(t, `SELECT count(*) FROM funeral.contract WHERE tenant_id=$1`, fixture.tenant); contracts != 1 {
		t.Fatalf("replay duplicou contrato: %d", contracts)
	}
	if events := fixture.count(t, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1`, fixture.tenant); events != 1 {
		t.Fatalf("replay duplicou evento: %d", events)
	}

	// Falha depois do append desfaz o agregado e o evento.
	failing, err := funeral.NewRepository(fixture.database, failingAppender{delegate: fixture.events, appendFirst: true, cause: errors.New("falha sintetica apos evento")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.CreateContract(ctx, fixture.input("atomic-fail-after")); err == nil {
		t.Fatal("falha injetada depois do evento deveria abortar a criacao")
	}
	if contracts := fixture.count(t, `SELECT count(*) FROM funeral.contract WHERE tenant_id=$1`, fixture.tenant); contracts != 1 {
		t.Fatalf("falha depois do evento deixou agregado: %d contrato(s)", contracts)
	}
	if events := fixture.count(t, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1`, fixture.tenant); events != 1 {
		t.Fatalf("falha depois do evento deixou evento: %d", events)
	}

	// Falha antes do append também não grava o agregado.
	noop, err := funeral.NewRepository(fixture.database, failingAppender{delegate: fixture.events, cause: errors.New("falha sintetica antes do evento")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noop.CreateContract(ctx, fixture.input("atomic-fail-before")); err == nil {
		t.Fatal("falha injetada antes do evento deveria abortar a criacao")
	}
	if contracts := fixture.count(t, `SELECT count(*) FROM funeral.contract WHERE tenant_id=$1`, fixture.tenant); contracts != 1 {
		t.Fatalf("falha antes do evento deixou agregado: %d", contracts)
	}
	if events := fixture.count(t, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1`, fixture.tenant); events != 1 {
		t.Fatalf("falha antes do evento deixou evento: %d", events)
	}

	// Rollback direto de uma transação que muda status e publica evento desfaz
	// os dois: o contrato continua draft e nenhum evento novo fica.
	sentinel := errors.New("rollback sintetico de status")
	if err := fixture.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE funeral.contract SET status='active', updated_at=now() WHERE tenant_id=$1 AND id=$2`, fixture.tenant, created.ID); err != nil {
			return err
		}
		if _, err := fixture.events.AppendTx(ctx, tx, outbox.Event{
			TenantID: fixture.tenant, AggregateType: "funeral.contract", AggregateID: created.ID,
			EventType: "funeral.contract.status_changed", Version: 1,
			Payload:        json.RawMessage(`{"from_status":"draft","to_status":"active"}`),
			IdempotencyKey: "funeral.contract.status:" + created.ID.String() + ":active",
		}); err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("rollback sintetico esperado, obtido %v", err)
	}
	var status string
	if err := fixture.database.WithTenantTx(db.WithTenant(context.Background(), fixture.tenant), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM funeral.contract WHERE tenant_id=$1 AND id=$2`, fixture.tenant, created.ID).Scan(&status)
	}); err != nil {
		t.Fatal(err)
	}
	if status != funeral.ContractDraft {
		t.Fatalf("rollback deixou status %q, esperado draft", status)
	}
	if events := fixture.count(t, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1`, fixture.tenant); events != 1 {
		t.Fatalf("rollback deixou evento: %d", events)
	}
}
