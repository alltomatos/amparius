package identity_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOnboardingDevelopmentIdempotencyAndRLS(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_ADMIN_DSN não definido; onboarding não criará dados sem cleanup administrativo")
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
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}
	var tenantIDs []uuid.UUID
	t.Cleanup(func() {
		defer adminPool.Close()
		if len(tenantIDs) == 0 {
			return
		}
		cleanupCtx := context.Background()
		tx, cleanupErr := adminPool.Begin(cleanupCtx)
		if cleanupErr == nil {
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
		}
		if cleanupErr != nil {
			t.Errorf("cleanup administrativo do onboarding: %v", cleanupErr)
			return
		}
		var remaining int
		if err := adminPool.QueryRow(cleanupCtx,
			`SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, tenantIDs,
		).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup do onboarding: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup deixou %d tenant(s) residuais", remaining)
		}
	})

	repository, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	request := identity.DevelopmentRequest("test-onboarding-" + uuid.NewString())
	first, err := repository.Onboard(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, first.TenantID)

	replay, err := repository.Onboard(ctx, request)
	if err != nil {
		t.Fatalf("replay idempotente: %v", err)
	}
	if replay != first {
		t.Fatalf("replay deveria devolver os mesmos IDs: primeiro=%+v replay=%+v", first, replay)
	}

	differentPayload := request
	differentPayload.OwnerName = "Payload divergente"
	if _, err := repository.Onboard(ctx, differentPayload); !errors.Is(err, identity.ErrIdempotencyConflict) {
		t.Fatalf("reutilização da chave com payload diferente: err=%v, quer ErrIdempotencyConflict", err)
	}

	secondRequest := identity.DevelopmentRequest("test-onboarding-" + uuid.NewString())
	if secondRequest.TenantDocument == request.TenantDocument {
		t.Fatal("fixtures sintéticas de chaves diferentes não podem compartilhar tenant_document")
	}
	second, err := repository.Onboard(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs = append(tenantIDs, second.TenantID)

	checkTenant := func(tenantID, visibleUserID, hiddenUserID uuid.UUID) {
		t.Helper()
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			var email, status, roleCode, city string
			if err := tx.QueryRow(ctx, `SELECT email, status FROM identity.app_user WHERE id = $1`, visibleUserID).Scan(&email, &status); err != nil {
				return err
			}
			if status != "pending_auth" {
				return fmt.Errorf("status do owner = %q, esperado pending_auth", status)
			}
			if tenantID == first.TenantID && email != "owner.dev@amparius.invalid" {
				return fmt.Errorf("email do owner = %q, esperado domínio sintético .invalid", email)
			}
			if err := tx.QueryRow(ctx, `
				SELECT r.code
				  FROM identity.user_role ur
				  JOIN identity.role r ON r.tenant_id = ur.tenant_id AND r.id = ur.role_id
				 WHERE ur.user_id = $1`, visibleUserID).Scan(&roleCode); err != nil {
				return err
			}
			if roleCode != "owner" {
				return fmt.Errorf("role do owner = %q, esperado owner", roleCode)
			}
			if err := tx.QueryRow(ctx, `SELECT city FROM identity.establishment WHERE id = $1`, first.EstablishmentID).Scan(&city); tenantID == first.TenantID && err != nil {
				return err
			} else if tenantID == first.TenantID && city != "Fortaleza" {
				return fmt.Errorf("cidade = %q, esperada Fortaleza", city)
			} else if tenantID != first.TenantID && err != pgx.ErrNoRows {
				return fmt.Errorf("RLS permitiu estabelecimento de outro tenant: err=%v", err)
			}
			var hidden int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM identity.app_user WHERE id = $1`, hiddenUserID).Scan(&hidden); err != nil {
				return err
			}
			if hidden != 0 {
				return fmt.Errorf("RLS expôs owner do outro tenant")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	checkTenant(first.TenantID, first.OwnerUserID, second.OwnerUserID)
	checkTenant(second.TenantID, second.OwnerUserID, first.OwnerUserID)
}
