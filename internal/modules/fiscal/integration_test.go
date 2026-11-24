package fiscal_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLookupTaxRuleTenantRLSVigenciaEFailClosed(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var role string
	var bypass bool
	if err := database.WithTenantTx(db.WithTenant(ctx, uuid.New()), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if role != "amparius_app" || bypass {
		t.Fatalf("esperado amparius_app sem bypass: role=%s bypass=%v", role, bypass)
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	var initial int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM fiscal.tax_rule`).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	if initial != 0 {
		t.Fatalf("tax_rule deveria estar vazia antes do teste; count=%d", initial)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	globalID, overrideID := uuid.New(), uuid.New()
	cleanup := func() {
		defer admin.Close()
		tx, cleanupErr := admin.Begin(ctx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(ctx) }()
			if _, cleanupErr = tx.Exec(ctx, `ALTER TABLE fiscal.tax_rule DISABLE TRIGGER tax_rule_global_append_only`); cleanupErr == nil {
				_, cleanupErr = tx.Exec(ctx, `DELETE FROM fiscal.tax_rule WHERE id = ANY($1::uuid[])`, []uuid.UUID{globalID, overrideID})
			}
			if cleanupErr == nil {
				_, cleanupErr = tx.Exec(ctx, `ALTER TABLE fiscal.tax_rule ENABLE TRIGGER tax_rule_global_append_only`)
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(ctx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup administrativo fiscal: %v", cleanupErr)
		}
		var remaining int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM fiscal.tax_rule`).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup fiscal: %v", err)
		} else if remaining != initial {
			t.Errorf("cleanup fiscal deixou %d regra(s)", remaining)
		}
	}
	t.Cleanup(cleanup)
	insert := `INSERT INTO fiscal.tax_rule
		(id, tenant_id, tax_type, regime, scope_kind, ncm, class_trib, basis_kind,
		rate, rate_unit, valid_from, valid_to, source_kind, source_name, source_url,
		source_version, source_published_at, source_hash, import_batch)
		VALUES ($1,$2,'is','geral','nfe','12345678','CLASSE-TESTE','isencao',NULL,NULL,$3,$4,'rfb','Teste sintetico','https://invalid.test','teste', $3, repeat('a',64), $5)`
	if _, err := admin.Exec(ctx, insert, globalID, nil, "2026-01-01", "2027-01-01", uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insert, overrideID, tenantA, "2026-01-01", "2027-01-01", uuid.New())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	checker, err := fiscal.NewChecker(database)
	if err != nil {
		t.Fatal(err)
	}
	criteria := fiscal.TaxRuleCriteria{TaxType: "is", Regime: "geral", ScopeKind: "nfe", NCM: "12345678", ClassTrib: "CLASSE-TESTE", BasisKind: "isencao"}
	date := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rule, err := checker.LookupTaxRule(db.WithTenant(ctx, tenantA), criteria, date)
	if err != nil || rule.ID != overrideID {
		t.Fatalf("tenant override esperado: id=%v err=%v", rule.ID, err)
	}
	rule, err = checker.LookupTaxRule(db.WithTenant(ctx, tenantB), criteria, date)
	if err != nil || rule.ID != globalID {
		t.Fatalf("fallback global esperado: id=%v err=%v", rule.ID, err)
	}
	if _, err := checker.LookupTaxRule(db.WithTenant(ctx, tenantA), criteria, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, fiscal.ErrTaxRuleNotFound) {
		t.Fatalf("fim half-open: %v", err)
	}
	if _, err := checker.LookupTaxRule(context.Background(), criteria, date); !errors.Is(err, fiscal.ErrTaxRuleUnavailable) {
		t.Fatalf("tenant ausente deve ser indisponivel: %v", err)
	}
	canceled, cancel := context.WithCancel(db.WithTenant(ctx, tenantA))
	cancel()
	if _, err := checker.LookupTaxRule(canceled, criteria, date); !errors.Is(err, fiscal.ErrTaxRuleUnavailable) {
		t.Fatalf("erro de banco deve ser distinguivel: %v", err)
	}
}

func TestDocumentsRLSIdempotencyAndAppendOnly(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	company, establishment := uuid.New(), uuid.New()
	cleanup := func() {
		defer admin.Close()
		cleanupCtx := context.Background()
		tx, cleanupErr := admin.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, q := range []string{"ALTER TABLE fiscal.emission_attempt DISABLE TRIGGER emission_attempt_append_only", "ALTER TABLE fiscal.tax_calc DISABLE TRIGGER tax_calc_append_only", "ALTER TABLE fiscal.fiscal_item DISABLE TRIGGER fiscal_item_append_only", "ALTER TABLE fiscal.fiscal_document DISABLE TRIGGER fiscal_document_history_preserve"} {
				if _, cleanupErr = tx.Exec(cleanupCtx, q); cleanupErr != nil {
					break
				}
			}
			for _, q := range []string{"DELETE FROM fiscal.emission_attempt WHERE tenant_id=$1", "DELETE FROM fiscal.tax_calc WHERE tenant_id=$1", "DELETE FROM fiscal.fiscal_item WHERE tenant_id=$1", "DELETE FROM fiscal.fiscal_document WHERE tenant_id=$1", "DELETE FROM identity.establishment WHERE tenant_id=$1", "DELETE FROM identity.company WHERE tenant_id=$1", "DELETE FROM identity.tenant WHERE id=$1"} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, q, tenantA)
			}
			if cleanupErr == nil {
				_, cleanupErr = tx.Exec(cleanupCtx, `DELETE FROM identity.tenant WHERE id=$1`, tenantB)
			}
			for _, q := range []string{"ALTER TABLE fiscal.emission_attempt ENABLE TRIGGER emission_attempt_append_only", "ALTER TABLE fiscal.tax_calc ENABLE TRIGGER tax_calc_append_only", "ALTER TABLE fiscal.fiscal_item ENABLE TRIGGER fiscal_item_append_only", "ALTER TABLE fiscal.fiscal_document ENABLE TRIGGER fiscal_document_history_preserve"} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, q)
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(cleanupCtx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup fiscal: %v", cleanupErr)
		}
	}
	t.Cleanup(cleanup)
	createTenant := func(id uuid.UUID) {
		if err := database.WithTenantTx(db.WithTenant(ctx, id), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, id, id.String(), "fiscal-"+id.String())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	createTenant(tenantA)
	createTenant(tenantB)
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.company (id,tenant_id,document,name,kind,status) VALUES ($1,$2,$3,'Empresa fiscal','funeral','active')`, company, tenantA, "company-"+company.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.establishment (id,tenant_id,company_id,document,name,status) VALUES ($1,$2,$3,$4,'Estabelecimento fiscal','active')`, establishment, tenantA, company, "est-"+establishment.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	ctxA, ctxB := db.WithTenant(ctx, tenantA), db.WithTenant(ctx, tenantB)
	document, err := repository.CreateDocument(ctxA, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfe", TotalCents: 10000})
	if err != nil {
		t.Fatal(err)
	}
	item, err := repository.CreateItem(ctxA, Item{DocumentID: document.ID, Competence: competence, Description: "Serviço", Quantity: "1", UnitPriceCents: 10000, TotalCents: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateTaxCalculation(ctxA, fiscal.TaxCalculation{ItemID: item.ID, Competence: competence, TaxType: "ibs", BaseCents: 10000, AmountCents: 100, CalculationInput: json.RawMessage(`{"version":1}`)}); err != nil {
		t.Fatal(err)
	}
	first, err := repository.RegisterEmissionAttempt(ctxA, fiscal.EmissionAttempt{DocumentID: document.ID, Competence: competence, Attempt: 1, IdempotencyKey: "fiscal-test-key", Mode: "normal", ResponseBody: stringPtr("first")})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repository.RegisterEmissionAttempt(ctxA, fiscal.EmissionAttempt{DocumentID: document.ID, Competence: competence, Attempt: 99, IdempotencyKey: "fiscal-test-key", Mode: "normal", ResponseBody: stringPtr("second")})
	if err != nil || replay.ID != first.ID || replay.ResponseBody == nil || *replay.ResponseBody != "first" {
		t.Fatalf("idempotencia: first=%+v replay=%+v err=%v", first, replay, err)
	}
	if _, err := repository.GetDocument(ctxB, document.ID, competence); !errors.Is(err, fiscal.ErrDocumentNotFound) {
		t.Fatalf("isolamento RLS: %v", err)
	}
	if err := database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE fiscal.tax_calc SET amount=0 WHERE item_id=$1`, item.ID)
		return err
	}); err == nil {
		t.Fatal("tax_calc deveria ser append-only")
	}
}

func stringPtr(value string) *string { return &value }

type Item = fiscal.Item
