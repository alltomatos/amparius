package fiscal_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaxEnginePostgresDatadoPrecedenciaEFailClosed(t *testing.T) {
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
	var initialRules int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM fiscal.tax_rule`).Scan(&initialRules); err != nil {
		admin.Close()
		t.Fatal(err)
	}

	tenantA, tenantB := uuid.New(), uuid.New()
	company, establishment := uuid.New(), uuid.New()
	globalISS, tenantISS, globalCBS, simplesISS := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ruleIDs := []uuid.UUID{globalISS, tenantISS, globalCBS, simplesISS}

	cleanup := func() {
		defer admin.Close()
		cleanupCtx := context.Background()
		tx, cleanupErr := admin.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, q := range []string{
				"ALTER TABLE fiscal.tax_calc DISABLE TRIGGER tax_calc_append_only",
				"ALTER TABLE fiscal.fiscal_item DISABLE TRIGGER fiscal_item_append_only",
				"ALTER TABLE fiscal.fiscal_document DISABLE TRIGGER fiscal_document_history_preserve",
			} {
				if _, cleanupErr = tx.Exec(cleanupCtx, q); cleanupErr != nil {
					break
				}
			}
			for _, q := range []string{
				"DELETE FROM fiscal.tax_calc WHERE tenant_id=$1",
				"DELETE FROM fiscal.fiscal_item WHERE tenant_id=$1",
				"DELETE FROM fiscal.fiscal_document WHERE tenant_id=$1",
				"DELETE FROM identity.establishment WHERE tenant_id=$1",
				"DELETE FROM identity.company WHERE tenant_id=$1",
				"DELETE FROM identity.tenant WHERE id=$1",
			} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, q, tenantA)
			}
			if cleanupErr == nil {
				_, cleanupErr = tx.Exec(cleanupCtx, `DELETE FROM identity.tenant WHERE id=$1`, tenantB)
			}
			if cleanupErr == nil {
				_, cleanupErr = tx.Exec(cleanupCtx, `ALTER TABLE fiscal.tax_rule DISABLE TRIGGER tax_rule_global_append_only`)
			}
			if cleanupErr == nil {
				_, cleanupErr = tx.Exec(cleanupCtx, `DELETE FROM fiscal.tax_rule WHERE id = ANY($1::uuid[])`, ruleIDs)
			}
			if cleanupErr == nil {
				_, cleanupErr = tx.Exec(cleanupCtx, `ALTER TABLE fiscal.tax_rule ENABLE TRIGGER tax_rule_global_append_only`)
			}
			for _, q := range []string{
				"ALTER TABLE fiscal.tax_calc ENABLE TRIGGER tax_calc_append_only",
				"ALTER TABLE fiscal.fiscal_item ENABLE TRIGGER fiscal_item_append_only",
				"ALTER TABLE fiscal.fiscal_document ENABLE TRIGGER fiscal_document_history_preserve",
			} {
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
		var remainingRules int
		if err := admin.QueryRow(cleanupCtx, `SELECT count(*) FROM fiscal.tax_rule`).Scan(&remainingRules); err != nil {
			t.Errorf("confirmar cleanup de regras: %v", err)
		} else if remainingRules != initialRules {
			t.Errorf("cleanup deixou %d regra(s), esperado %d", remainingRules, initialRules)
		}
	}
	t.Cleanup(cleanup)

	createTenant := func(id uuid.UUID) {
		if err := database.WithTenantTx(db.WithTenant(ctx, id), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, id, id.String(), "fiscal-tax-"+id.String())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	createTenant(tenantA)
	createTenant(tenantB)
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.company (id,tenant_id,document,name,kind,status) VALUES ($1,$2,$3,'Empresa motor fiscal','funeral','active')`, company, tenantA, "company-"+company.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.establishment (id,tenant_id,company_id,document,name,status) VALUES ($1,$2,$3,$4,'Estabelecimento motor fiscal','active')`, establishment, tenantA, company, "est-"+establishment.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	insertRule := `INSERT INTO fiscal.tax_rule
		(id, tenant_id, tax_type, regime, scope_kind, service_code, municipality_code, class_trib,
		 basis_kind, rate, rate_unit, deduction_amount, simples_annex, rbt12_from, rbt12_to,
		 valid_from, valid_to, source_kind, source_name, source_url, source_version,
		 source_published_at, source_hash, import_batch)
		VALUES ($1,$2,$3,$4,'nfse',$5,$6,$7,$8,$9,'percent',$10,$11,$12,$13,$14,$15,
		        $16,'SINTETICO NAO OFICIAL - teste de integracao','https://invalid.test','teste',
		        $14,$17,$18)`
	insert := func(ruleID uuid.UUID, tenant *uuid.UUID, taxType, regime, service string, municipality, classTrib *string, basis string,
		rate, deduction, annex, rbt12From, rbt12To *string, validFrom, validTo *string, sourceKind string) {
		t.Helper()
		if _, err := admin.Exec(ctx, insertRule, ruleID, tenant, taxType, regime, service, municipality, classTrib,
			basis, rate, deduction, annex, rbt12From, rbt12To, validFrom, validTo, sourceKind, strings.Repeat("f", 64), uuid.New()); err != nil {
			t.Fatalf("inserir regra %s: %v", taxType, err)
		}
	}
	from, to := "2026-01-01", "2027-01-01"
	str := func(v string) *string { return &v }
	// Valores SINTETICOS e NAO OFICIAIS: apenas para exercitar consulta datada.
	insert(globalISS, nil, "iss", "geral", "25.01", str("2304400"), nil, "percentual", str("5.000000"), nil, nil, nil, nil, &from, &to, "municipio")
	insert(tenantISS, &tenantA, "iss", "geral", "25.01", str("2304400"), nil, "percentual", str("4.500000"), nil, nil, nil, nil, &from, &to, "municipio")
	insert(globalCBS, nil, "cbs", "geral", "25.01", nil, str("000001"), "percentual", str("0.900000"), nil, nil, nil, nil, &from, &to, "rfb")
	insert(simplesISS, nil, "iss", "simples_nacional", "25.02", str("2304400"), nil, "faixa_simples", str("6.000000"), str("5000.00"), str("III"), str("0.00"), str("2000000.00"), &from, &to, "municipio")

	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	checker, err := fiscal.NewChecker(database)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := fiscal.NewTaxEngine(checker)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	ctxA, ctxB := db.WithTenant(ctx, tenantA), db.WithTenant(ctx, tenantB)

	issCriteria := fiscal.TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "25.01", MunicipalityCode: "2304400", BasisKind: "percentual"}
	computationA, err := engine.ComputeItem(ctxA, competence, []fiscal.TaxItemRequest{{Criteria: issCriteria, BaseCents: 10000}})
	if err != nil {
		t.Fatal(err)
	}
	if computationA.Lines[0].AmountCents != 450 || computationA.Lines[0].Rate == nil || *computationA.Lines[0].Rate != "4.500000" {
		t.Fatalf("precedencia tenant: %+v", computationA.Lines[0])
	}
	computationB, err := engine.ComputeItem(ctxB, competence, []fiscal.TaxItemRequest{{Criteria: issCriteria, BaseCents: 10000}})
	if err != nil {
		t.Fatal(err)
	}
	if computationB.Lines[0].AmountCents != 500 || *computationB.Lines[0].Rate != "5.000000" {
		t.Fatalf("fallback global: %+v", computationB.Lines[0])
	}
	if computationA.Hash == computationB.Hash {
		t.Fatal("regra diferente deveria mudar o hash")
	}

	if _, err := engine.ComputeItem(ctxA, time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC), []fiscal.TaxItemRequest{{Criteria: issCriteria, BaseCents: 10000}}); !errors.Is(err, fiscal.ErrTaxRuleRequired) {
		t.Fatalf("competencia fora da vigencia deveria bloquear: %v", err)
	}
	if _, err := engine.ComputeItem(ctxA, competence, []fiscal.TaxItemRequest{{Criteria: fiscal.TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "77.77", MunicipalityCode: "2304400"}, BaseCents: 10000}}); !errors.Is(err, fiscal.ErrTaxRuleRequired) {
		t.Fatalf("servico sem regra deveria bloquear: %v", err)
	}

	document, err := repository.CreateDocument(ctxA, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 10000})
	if err != nil {
		t.Fatal(err)
	}
	item, err := repository.CreateItem(ctxA, fiscal.Item{DocumentID: document.ID, Competence: competence, Description: "Servico sintetico", Quantity: "1", UnitPriceCents: 10000, TotalCents: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if count := countTaxCalc(t, database, ctxA, item.ID, competence); count != 0 {
		t.Fatalf("regra ausente nao deveria persistir tax_calc: %d", count)
	}

	cbsCriteria := fiscal.TaxRuleCriteria{TaxType: "cbs", Regime: "geral", ScopeKind: "nfse", ServiceCode: "25.01", BasisKind: "percentual"}
	computation, err := engine.ComputeItem(ctxA, competence, []fiscal.TaxItemRequest{
		{Criteria: issCriteria, BaseCents: 10000},
		{Criteria: cbsCriteria, BaseCents: 10000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(computation.Lines) != 2 || computation.Lines[0].TaxType != "cbs" || computation.Lines[1].TaxType != "iss" {
		t.Fatalf("ordem canonica: %+v", computation.Lines)
	}
	for _, calculation := range computation.ToCalculations(tenantA, item.ID) {
		if _, err := repository.CreateTaxCalculation(ctxA, calculation); err != nil {
			t.Fatal(err)
		}
	}
	if count := countTaxCalc(t, database, ctxA, item.ID, competence); count != 2 {
		t.Fatalf("tax_calc persistidos: %d", count)
	}
	if err := database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		var storedHash, rate string
		var amount int64
		if err := tx.QueryRow(ctx, `SELECT calculation_hash, rate::text, (amount*100)::bigint FROM fiscal.tax_calc WHERE tenant_id=$1 AND item_id=$2 AND tax_type='iss'`, tenantA, item.ID).Scan(&storedHash, &rate, &amount); err != nil {
			return err
		}
		if storedHash != computation.Lines[1].CalculationHash {
			t.Fatalf("hash persistido diverge: %s != %s", storedHash, computation.Lines[1].CalculationHash)
		}
		if rate != "4.500000" || amount != 450 {
			t.Fatalf("snapshot persistido inesperado: rate=%s amount=%d", rate, amount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE fiscal.tax_calc SET amount=0 WHERE item_id=$1`, item.ID)
		return err
	}); err == nil {
		t.Fatal("tax_calc deveria ser append-only")
	}

	simplesCriteria := fiscal.TaxRuleCriteria{TaxType: "iss", Regime: "simples_nacional", ScopeKind: "nfse", ServiceCode: "25.02", MunicipalityCode: "2304400", BasisKind: "faixa_simples", SimplesAnnex: "III"}
	simples, err := engine.ComputeItem(ctxA, competence, []fiscal.TaxItemRequest{{Criteria: simplesCriteria, BaseCents: 10000, RBT12Cents: 100000000}})
	if err != nil {
		t.Fatal(err)
	}
	if simples.Lines[0].AmountCents != 550 || simples.Lines[0].Rate == nil || *simples.Lines[0].Rate != "5.500000" {
		t.Fatalf("simples: %+v", simples.Lines[0])
	}
	if _, err := engine.ComputeItem(ctxA, competence, []fiscal.TaxItemRequest{{Criteria: simplesCriteria, BaseCents: 10000, RBT12Cents: 300000000}}); !errors.Is(err, fiscal.ErrTaxRuleRequired) {
		t.Fatalf("RBT12 fora da faixa deveria bloquear: %v", err)
	}
}

func countTaxCalc(t *testing.T, database *db.DB, ctx context.Context, itemID uuid.UUID, competence time.Time) int {
	t.Helper()
	var count int
	if err := database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM fiscal.tax_calc WHERE tenant_id=current_setting('app.tenant_id')::uuid AND item_id=$1 AND competence=$2`, itemID, competence).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
