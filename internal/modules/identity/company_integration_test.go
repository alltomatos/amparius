package identity_test

import (
	"context"
	"os"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCompanyBelongsToTenantScopesByTenantAndRLS(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_ADMIN_DSN não definido; sem cleanup administrativo")
	}

	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var role string
	var bypass bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user, rolsuper OR rolbypassrls
			  FROM pg_roles WHERE rolname = current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if role != "amparius_app" || bypass {
		t.Fatalf("integração exige papel sem SUPERUSER/BYPASSRLS; role=%s bypass=%v", role, bypass)
	}

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	var tenantIDs []uuid.UUID
	t.Cleanup(func() {
		defer adminPool.Close()
		cleanupCtx := context.Background()
		tx, cleanupErr := adminPool.Begin(cleanupCtx)
		if cleanupErr != nil {
			t.Errorf("abrir cleanup: %v", cleanupErr)
			return
		}
		defer func() { _ = tx.Rollback(cleanupCtx) }()
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
			if _, cleanupErr = tx.Exec(cleanupCtx, statement, tenantIDs); cleanupErr != nil {
				break
			}
		}
		if cleanupErr == nil {
			cleanupErr = tx.Commit(cleanupCtx)
		}
		if cleanupErr != nil {
			t.Errorf("cleanup administrativo: %v", cleanupErr)
		}
	})

	repository, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.Onboard(ctx, identity.DevelopmentRequest("test-company-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, first.TenantID)
	second, err := repository.Onboard(ctx, identity.DevelopmentRequest("test-company-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, second.TenantID)

	ctxA := db.WithTenant(ctx, first.TenantID)
	ok, err := repository.CompanyBelongsToTenant(ctxA, first.TenantID, first.CompanyID)
	if err != nil || !ok {
		t.Fatalf("empresa do próprio tenant deveria pertencer: ok=%v err=%v", ok, err)
	}

	ok, err = repository.CompanyBelongsToTenant(ctxA, first.TenantID, second.CompanyID)
	if err != nil {
		t.Fatalf("empresa de outro tenant não deve gerar erro de leitura: %v", err)
	}
	if ok {
		t.Fatal("RLS deixou vazar empresa de outro tenant")
	}

	ok, err = repository.CompanyBelongsToTenant(ctxA, first.TenantID, uuid.New())
	if err != nil || ok {
		t.Fatalf("empresa inexistente deveria negar: ok=%v err=%v", ok, err)
	}

	ok, err = repository.CompanyBelongsToTenant(ctxA, second.TenantID, first.CompanyID)
	if err != nil || ok {
		t.Fatalf("tenant divergente deveria negar sem consultar: ok=%v err=%v", ok, err)
	}

	if _, err := repository.CompanyBelongsToTenant(context.Background(), first.TenantID, first.CompanyID); err == nil {
		t.Fatal("contexto sem tenant deveria falhar")
	}
}
