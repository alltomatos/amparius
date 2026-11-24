package fiscal_test

import (
	"context"
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
