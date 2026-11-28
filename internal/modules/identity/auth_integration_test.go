package identity_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResolveSubjectUsesPersistedCompanyScopeAndRLS(t *testing.T) {
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
	var bypass bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatal("teste de autenticação exige papel sem SUPERUSER/BYPASSRLS")
	}

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	onboarded, err := repository.Onboard(ctx, identity.DevelopmentRequest("auth-scope-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs := []uuid.UUID{onboarded.TenantID}
	second, err := repository.Onboard(ctx, identity.DevelopmentRequest("auth-scope-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, second.TenantID)
	t.Cleanup(func() {
		defer adminPool.Close()
		cleanupCtx := context.Background()
		for _, statement := range []string{
			"DELETE FROM identity.user_company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.user_role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.onboarding_request WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.app_user WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.role WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.establishment WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
			"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
		} {
			if _, cleanupErr := adminPool.Exec(cleanupCtx, statement, tenantIDs); cleanupErr != nil {
				t.Errorf("cleanup %q: %v", statement, cleanupErr)
				return
			}
		}
	})

	subject := "oidc-subject-" + uuid.NewString()
	if _, err := adminPool.Exec(ctx, `
		UPDATE identity.app_user
		   SET auth_subject = $2, status = 'active', mfa_enabled = true
		 WHERE tenant_id = $1 AND id = $3`, onboarded.TenantID, subject, onboarded.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	// identity.create_onboarding (migration 0042) já vincula o owner à empresa
	// sede em identity.user_company; nenhum INSERT manual é necessário aqui.
	for _, fixture := range []struct {
		name   string
		tenant uuid.UUID
		want   int
	}{
		{name: "tenant próprio", tenant: onboarded.TenantID, want: 1},
		{name: "outro tenant", tenant: second.TenantID, want: 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var count int
			if err := database.WithTenantTx(db.WithTenant(ctx, fixture.tenant), func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT count(*) FROM identity.user_company`).Scan(&count)
			}); err != nil {
				t.Fatal(err)
			}
			if count != fixture.want {
				t.Fatalf("user_company visível = %d, esperado %d", count, fixture.want)
			}
		})
	}

	record, err := repository.ResolveSubject(db.WithTenant(ctx, onboarded.TenantID), onboarded.TenantID, subject)
	if err != nil {
		t.Fatal(err)
	}
	if !record.MFAEnabled || len(record.Roles) != 1 || record.Roles[0] != "owner" || len(record.CompanyIDs) != 1 || record.CompanyIDs[0] != onboarded.CompanyID {
		t.Fatalf("registro resolvido = %#v", record)
	}

	if _, err := repository.ResolveSubject(context.Background(), onboarded.TenantID, subject); !errors.Is(err, db.ErrTenantRequired) {
		t.Fatalf("sem tenant = %v, esperado ErrTenantRequired", err)
	}
	if _, err := repository.ResolveSubject(db.WithTenant(ctx, uuid.New()), onboarded.TenantID, subject); !errors.Is(err, identity.ErrAuthenticatedSubjectNotFound) {
		t.Fatalf("tenant divergente = %v, esperado subject não encontrado", err)
	}

}
