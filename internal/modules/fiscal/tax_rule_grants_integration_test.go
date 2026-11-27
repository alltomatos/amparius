package fiscal_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Prova o escopo da migration 0039: o papel da aplicacao le e insere regras
// somente sob RLS (tenant proprio + globais), nao escreve regra global, nao
// escreve em outro tenant e nao tem UPDATE/DELETE.
func TestTaxRuleAppRoleGrantScopeAndRLS(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
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
	var initial int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM fiscal.tax_rule`).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	if initial != 0 {
		t.Fatalf("tax_rule deveria estar vazia antes do teste; count=%d", initial)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	globalID, ownID := uuid.New(), uuid.New()
	t.Cleanup(func() {
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Errorf("cleanup fiscal: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		for _, q := range []string{
			`ALTER TABLE fiscal.tax_rule DISABLE TRIGGER tax_rule_global_append_only`,
			`DELETE FROM fiscal.tax_rule WHERE id = ANY($1::uuid[])`,
			`ALTER TABLE fiscal.tax_rule ENABLE TRIGGER tax_rule_global_append_only`,
		} {
			var args []any
			if q[:6] == "DELETE" {
				args = []any{[]uuid.UUID{globalID, ownID}}
			}
			if _, err := tx.Exec(ctx, q, args...); err != nil {
				t.Errorf("cleanup fiscal: %v", err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("cleanup fiscal: %v", err)
		}
	})
	const insert = `INSERT INTO fiscal.tax_rule
		(id, tenant_id, tax_type, regime, scope_kind, ncm, class_trib, basis_kind,
		rate, rate_unit, valid_from, valid_to, source_kind, source_name, source_url,
		source_version, source_published_at, source_hash, import_batch)
		VALUES ($1,$2,'is','geral','nfe',$3,'CLASSE-TESTE','isencao',NULL,NULL,'2026-01-01','2027-01-01',
		'rfb','Teste sintetico','https://invalid.test','teste','2026-01-01',repeat('a',64),$4)`
	if _, err := admin.Exec(ctx, insert, globalID, nil, "11111111", uuid.New()); err != nil {
		t.Fatal(err)
	}

	// Privilegios de tabela: exatamente SELECT e INSERT.
	err = database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		var sel, ins, upd, del bool
		if err := tx.QueryRow(ctx, `SELECT
			has_table_privilege(current_user,'fiscal.tax_rule','SELECT'),
			has_table_privilege(current_user,'fiscal.tax_rule','INSERT'),
			has_table_privilege(current_user,'fiscal.tax_rule','UPDATE'),
			has_table_privilege(current_user,'fiscal.tax_rule','DELETE')`).Scan(&sel, &ins, &upd, &del); err != nil {
			return err
		}
		if !sel || !ins || upd || del {
			t.Errorf("grants de tax_rule: select=%v insert=%v update=%v delete=%v", sel, ins, upd, del)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	denied := func(name string, tenantCtx uuid.UUID, tenantArg any, ncm string) {
		t.Helper()
		err := database.WithTenantTx(db.WithTenant(ctx, tenantCtx), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, insert, uuid.New(), tenantArg, ncm, uuid.New())
			return err
		})
		var pgErr *pgconn.PgError
		if err == nil || !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: esperado 42501 (RLS WITH CHECK), recebido %v", name, err)
		}
	}
	denied("regra global (tenant NULL) pelo papel da aplicacao", tenantA, nil, "22222222")
	denied("regra do tenant B no contexto do tenant A", tenantA, tenantB, "33333333")

	// Insercao no proprio tenant funciona; tenant B nao enxerga a regra de A.
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insert, ownID, tenantA, "44444444", uuid.New())
		return err
	}); err != nil {
		t.Fatalf("insercao no proprio tenant: %v", err)
	}
	visible := func(tenantCtx uuid.UUID) map[uuid.UUID]bool {
		got := map[uuid.UUID]bool{}
		err := database.WithTenantTx(db.WithTenant(ctx, tenantCtx), func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id FROM fiscal.tax_rule`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					return err
				}
				got[id] = true
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if a := visible(tenantA); !a[globalID] || !a[ownID] || len(a) != 2 {
		t.Errorf("tenant A deveria ver global + propria: %v", a)
	}
	if b := visible(tenantB); !b[globalID] || b[ownID] || len(b) != 1 {
		t.Errorf("tenant B deveria ver so a global: %v", b)
	}
}
