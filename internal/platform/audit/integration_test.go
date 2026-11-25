package audit_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/platform/audit"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuditAndConsentRespectRLSAndPrivacy(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN são necessários")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var bypass bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatal("auditoria exige papel sem SUPERUSER/BYPASSRLS")
	}
	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := identity.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	a, err := repository.Onboard(ctx, identity.DevelopmentRequest("audit-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	b, err := repository.Onboard(ctx, identity.DevelopmentRequest("audit-"+uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	tenantIDs := []uuid.UUID{a.TenantID, b.TenantID}
	t.Cleanup(func() {
		cleanup := context.Background()
		var cleanupErr error
		var tx pgx.Tx
		tx, cleanupErr = adminPool.Begin(cleanup)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanup) }()
			for _, statement := range []string{
				"ALTER TABLE privacy.consent_record DISABLE TRIGGER USER",
				"ALTER TABLE audit.log DISABLE TRIGGER USER",
				"DELETE FROM privacy.consent_record WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM audit.log WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.user_company WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.user_role WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.onboarding_request WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.app_user WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.role WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.establishment WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
				"ALTER TABLE audit.log ENABLE TRIGGER USER",
				"ALTER TABLE privacy.consent_record ENABLE TRIGGER USER",
			} {
				if statement[:6] == "DELETE" {
					_, cleanupErr = tx.Exec(cleanup, statement, tenantIDs)
				} else {
					_, cleanupErr = tx.Exec(cleanup, statement)
				}
				if cleanupErr != nil {
					break
				}
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(cleanup)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup de auditoria: %v", cleanupErr)
		}
		adminPool.Close()
		database.Close()
	})

	logger, err := audit.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	principal := platformauth.Principal{Subject: "subject-a", UserID: a.OwnerUserID, TenantID: a.TenantID, CompanyID: a.CompanyID, MFAAuthenticated: true}
	ctxA := platformauth.WithPrincipal(db.WithCompany(db.WithTenant(ctx, a.TenantID), a.CompanyID), principal)
	retention := time.Now().UTC().Add(24 * time.Hour)
	auditID, err := logger.Append(ctxA, audit.Entry{
		Action: "party.person.read", ResourceType: "party.person", ResourceID: uuid.New(),
		Outcome: audit.OutcomeSuccess, Detail: []byte(`{"resource_id":"opaque-ref","status":"success"}`), RetentionUntil: retention,
	})
	if err != nil {
		t.Fatal(err)
	}
	if auditID == uuid.Nil {
		t.Fatal("auditoria não devolveu id")
	}
	consentID, err := logger.RecordConsent(ctxA, audit.Consent{
		SubjectType: "party.person", SubjectID: uuid.New(), PurposeCode: "service.delivery",
		LegalBasisCode: "contract", Status: "granted", EvidenceRef: "evidence-1", RetentionUntil: retention,
	})
	if err != nil {
		t.Fatal(err)
	}
	if consentID == uuid.Nil {
		t.Fatal("consentimento não devolveu id")
	}
	if err := database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		var auditCount, consentCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit.log`).Scan(&auditCount); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM privacy.consent_record`).Scan(&consentCount); err != nil {
			return err
		}
		if auditCount != 2 || consentCount != 1 {
			return errors.New("contagens tenant A inesperadas")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.WithTenantTx(db.WithTenant(ctx, b.TenantID), func(ctx context.Context, tx pgx.Tx) error {
		var visible int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit.log`).Scan(&visible); err != nil {
			return err
		}
		if visible != 0 {
			return errors.New("RLS expôs auditoria de outro tenant")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.WithTenantTx(db.WithTenant(ctx, a.TenantID), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO audit.log (tenant_id, actor_kind, action, resource_type, outcome, detail, retention_until) VALUES ($1,'system','party.person.read','party.person','success',$2,now()+interval '1 day')`, a.TenantID, []byte(`{"email":"person@example.invalid"}`))
		return err
	}); err == nil {
		t.Fatal("detalhe com PII deveria ser recusado pelo banco")
	}
}
