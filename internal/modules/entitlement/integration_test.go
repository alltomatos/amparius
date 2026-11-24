package entitlement_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// tenantCompanyResolver simula o validador de empresa (identity) na integração
// do control plane: só a empresa conhecida pertence ao tenant.
type tenantCompanyResolver map[uuid.UUID]uuid.UUID

func (r tenantCompanyResolver) CompanyBelongsToTenant(_ context.Context, tenantID, companyID uuid.UUID) (bool, error) {
	owner, ok := r[companyID]
	return ok && owner == tenantID, nil
}

func TestCheckerControlPlaneTenantCompanyAndFailClosed(t *testing.T) {
	dsn := os.Getenv("AMPA_CONTROL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_CONTROL_POSTGRES_DSN não definido; integração do control plane não executada")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var bypass bool
	var role string
	probeTenant := uuid.New()
	if err := database.WithTenantTx(db.WithTenant(ctx, probeTenant), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatalf("integração exige papel sem SUPERUSER/BYPASSRLS; role=%s bypass=%v", role, bypass)
	}

	tenantA, tenantB := uuid.New(), uuid.New()
	companyA, companyB := uuid.New(), uuid.New()
	foreignCompany := uuid.New()
	resolver := tenantCompanyResolver{companyA: tenantA, companyB: tenantB}
	checker, err := entitlement.NewChecker(database, entitlement.WithCompanyResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	inactiveCode := "__test_inactive_" + uuid.NewString()
	date := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	cleanup := func() {
		for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
			_ = database.WithTenantTx(db.WithTenant(context.Background(), tenantID), func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `DELETE FROM entitlement.license_assignment WHERE tenant_id = $1`, tenantID)
				return err
			})
		}
		_ = database.WithTenantTx(db.WithTenant(context.Background(), tenantA), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM entitlement.module WHERE code = $1`, inactiveCode)
			return err
		})
	}
	t.Cleanup(cleanup)
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO entitlement.module (code, name, kind, sort_order, active)
			VALUES ($1, 'Teste inativo', 'optional', 100000, false)`, inactiveCode)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	insert := func(tenantID, companyID uuid.UUID, module, status string, from, to string) {
		err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO entitlement.license_assignment
				(tenant_id, module_code, company_id, valid_from, valid_to, status)
				VALUES ($1,$2,$3,$4::date,NULLIF($5,'')::date,$6)`, tenantID, module, companyID, from, to, status)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(tenantA, companyA, entitlement.ModuleFuneral, "active", "2026-01-01", "2026-12-31")
	insert(tenantA, companyA, entitlement.ModuleCemetery, "active", "2025-01-01", "2026-01-01")
	insert(tenantA, companyA, entitlement.ModuleCremation, "revoked", "2026-01-01", "2026-12-31")
	insert(tenantB, companyB, entitlement.ModuleFuneral, "active", "2026-01-01", "2026-12-31")

	ctxA, ctxB := db.WithTenant(ctx, tenantA), db.WithTenant(ctx, tenantB)
	ctxCompanyA := db.WithCompany(ctxA, companyA)
	for name, call := range map[string]func() error{
		"core ativo":            func() error { return checker.Check(ctxCompanyA, entitlement.ModuleFinance, date) },
		"optional vigente":      func() error { return checker.Check(ctxCompanyA, entitlement.ModuleFuneral, date) },
		"explicit company same": func() error { return checker.CheckCompany(ctxA, entitlement.ModuleFuneral, companyA, date) },
	} {
		if err := call(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, call := range map[string]func() error{
		"sem assignment": func() error { return checker.Check(ctxCompanyA, entitlement.ModuleInventory, date) },
		"vencido":        func() error { return checker.Check(ctxCompanyA, entitlement.ModuleCemetery, date) },
		"revogado":       func() error { return checker.Check(ctxCompanyA, entitlement.ModuleCremation, date) },
		"company errada": func() error { return checker.Check(db.WithCompany(ctxA, companyB), entitlement.ModuleFuneral, date) },
		"company desconhecida": func() error {
			return checker.Check(db.WithCompany(ctxA, foreignCompany), entitlement.ModuleFuneral, date)
		},
		"tenant isolado": func() error { return checker.Check(db.WithCompany(ctxB, companyA), entitlement.ModuleFuneral, date) },
		"inexistente":    func() error { return checker.Check(ctxCompanyA, "does-not-exist", date) },
		"inativo":        func() error { return checker.Check(ctxCompanyA, inactiveCode, date) },
	} {
		err := call()
		if !errors.Is(err, entitlement.ErrModuleDisabled) {
			t.Errorf("%s: esperado ErrModuleDisabled, recebido %v", name, err)
		}
	}
	if err := checker.CheckCompany(ctxA, entitlement.ModuleFuneral, companyA, time.Time{}); !errors.Is(err, entitlement.ErrModuleDisabled) {
		t.Fatalf("entrada inválida deveria bloquear com ErrModuleDisabled, recebido %v", err)
	}
	canceled, cancel := context.WithCancel(ctxCompanyA)
	cancel()
	if err := checker.Check(canceled, entitlement.ModuleFuneral, date); !errors.Is(err, entitlement.ErrEntitlementUnavailable) {
		t.Fatalf("falha de banco/transação deveria bloquear com ErrEntitlementUnavailable, recebido %v", err)
	}
}
