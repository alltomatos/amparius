package entitlement_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestListForCompanyScopesByTenantAndIncludesCoreModules(t *testing.T) {
	dsn := os.Getenv("AMPA_CONTROL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_CONTROL_POSTGRES_DSN não definido")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service, err := entitlement.NewService(database)
	if err != nil {
		t.Fatal(err)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	companyA, companyB := uuid.New(), uuid.New()
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		tenantCtx := db.WithTenant(ctx, tenantID)
		if err := service.SetModuleQuota(tenantCtx, entitlement.ModuleFuneral, 2); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
			cleanupCtx := db.WithTenant(context.Background(), tenantID)
			if err := database.WithTenantTx(cleanupCtx, func(ctx context.Context, tx pgx.Tx) error {
				for _, statement := range []string{
					"DELETE FROM entitlement.license_assignment WHERE tenant_id = $1",
					"DELETE FROM entitlement.license_pool WHERE tenant_id = $1",
				} {
					if _, err := tx.Exec(ctx, statement, tenantID); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Errorf("cleanup tenant %s: %v", tenantID, err)
			}
		}
	})
	from := time.Now().UTC().Truncate(24 * time.Hour)
	if _, err := service.Activate(db.WithTenant(ctx, tenantA), uuid.New(), entitlement.ActivateInput{
		ModuleCode: entitlement.ModuleFuneral, CompanyID: companyA, Origin: entitlement.OriginAdmin, ValidFrom: &from,
	}); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListForCompany(db.WithTenant(ctx, tenantA), companyA, time.Now().UTC(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 0 {
		t.Fatal("catálogo deveria incluir módulos core")
	}
	var foundFuneral bool
	for _, item := range page.Items {
		if item.ModuleCode == entitlement.ModuleFuneral {
			foundFuneral = true
			if item.Status != entitlement.StatusActive || item.CompanyID != companyA {
				t.Fatalf("entitlement da empresa A inesperado: %+v", item)
			}
		}
	}
	if !foundFuneral {
		t.Fatal("funeral não apareceu na leitura")
	}
	otherPage, err := service.ListForCompany(db.WithTenant(ctx, tenantB), companyB, time.Now().UTC(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range otherPage.Items {
		if item.ModuleCode == entitlement.ModuleFuneral && item.Status == entitlement.StatusActive {
			t.Fatal("licença do tenant A vazou para tenant B")
		}
	}
}
