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

func TestListUserCompaniesUsesPersistedScopeAndRLS(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN são necessários")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	var tenantIDs []uuid.UUID
	t.Cleanup(func() {
		defer adminPool.Close()
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
			if _, cleanupErr := adminPool.Exec(ctx, statement, tenantIDs); cleanupErr != nil {
				t.Errorf("cleanup %q: %v", statement, cleanupErr)
				return
			}
		}
	})

	repository, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.Onboard(ctx, identity.DevelopmentRequest("context-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.Onboard(ctx, identity.DevelopmentRequest("context-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, first.TenantID, second.TenantID)
	// identity.create_onboarding (migration 0042) já vincula o owner à empresa
	// sede em identity.user_company; nenhum INSERT manual é necessário aqui.

	page, err := repository.ListUserCompanies(db.WithTenant(ctx, first.TenantID), first.OwnerUserID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != first.CompanyID || page.Items[0].TenantID != first.TenantID {
		t.Fatalf("empresas visíveis = %+v", page.Items)
	}
	if page.Items[0].Document == "" {
		t.Fatal("documento deveria ser lido pelo caso de uso; a máscara é responsabilidade HTTP")
	}

	page, err = repository.ListUserCompanies(db.WithTenant(ctx, second.TenantID), first.OwnerUserID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("RLS/escopo vazou empresa de outro tenant: %+v", page.Items)
	}

	var bypass bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatal("integração não pode provar RLS com SUPERUSER/BYPASSRLS")
	}
}
