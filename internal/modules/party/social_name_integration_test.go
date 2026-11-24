package party_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSocialNameMigrationPreservesCivilNameAndTenantIsolation(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN nao definido")
	}
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_ADMIN_DSN nao definido; teste nao criara dados sem cleanup administrativo")
	}

	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var role string
	var superuser, bypassRLS bool
	if err := database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user, rolsuper, rolbypassrls
			  FROM pg_roles WHERE rolname = current_user`).Scan(&role, &superuser, &bypassRLS)
	}); err != nil {
		t.Fatal(err)
	}
	if superuser || bypassRLS {
		t.Fatalf("teste exige papel sem SUPERUSER/BYPASSRLS; role=%s superuser=%v bypass=%v", role, superuser, bypassRLS)
	}

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}

	tenantA, tenantB := uuid.New(), uuid.New()
	tenantIDs := []uuid.UUID{tenantA, tenantB}
	t.Cleanup(func() {
		defer adminPool.Close()
		cleanupCtx := context.Background()
		tx, cleanupErr := adminPool.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, statement := range []string{
				"DELETE FROM party.family_link WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.deceased WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.person WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.party_company WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.address WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.contact WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM party.party WHERE tenant_id = ANY($1::uuid[])",
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
			t.Errorf("cleanup administrativo do nome social: %v", cleanupErr)
			return
		}
		var remaining int
		if err := adminPool.QueryRow(cleanupCtx,
			`SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, tenantIDs,
		).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup do nome social: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup deixou %d tenant(s) residuais", remaining)
		}
	})

	createTenant := func(tenantID uuid.UUID) {
		t.Helper()
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO identity.tenant (id, name, document, regime, status)
				VALUES ($1, $2, $3, $4, $5)`,
				tenantID, "Social name "+tenantID.String(), "doc-"+tenantID.String(), "economic_group", "active")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	createPerson := func(tenantID uuid.UUID, civilName string, socialName any) uuid.UUID {
		t.Helper()
		var partyID uuid.UUID
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `
				INSERT INTO party.party (tenant_id, kind, name, status)
				VALUES ($1, 'person', $2, 'active')
				RETURNING id`, tenantID, civilName).Scan(&partyID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO party.person (tenant_id, party_id, social_name)
				VALUES ($1, $2, $3)`, tenantID, partyID, socialName)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return partyID
	}

	createTenant(tenantA)
	createTenant(tenantB)
	nullID := createPerson(tenantA, "Nome civil nulo", nil)
	emptyID := createPerson(tenantA, "Nome civil vazio", "")
	filledID := createPerson(tenantA, "Nome civil preenchido", "Nome de uso")
	hiddenID := createPerson(tenantB, "Nome civil tenant B", "Nome B")

	assertPerson := func(tenantID, partyID uuid.UUID, wantCivil string, wantSocial *string, wantDisplay string) {
		t.Helper()
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			var civil, display string
			var social pgtype.Text
			if err := tx.QueryRow(ctx, `
				SELECT p.name, pe.social_name,
				       COALESCE(NULLIF(btrim(pe.social_name), ''), p.name)
				  FROM party.party p
				  JOIN party.person pe
				    ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
				 WHERE p.id = $1`, partyID).Scan(&civil, &social, &display); err != nil {
				return err
			}
			if civil != wantCivil || display != wantDisplay {
				return fmt.Errorf("civil_name/display_name = %q/%q, esperado %q/%q", civil, display, wantCivil, wantDisplay)
			}
			if wantSocial == nil {
				if social.Valid {
					return fmt.Errorf("social_name = %q, esperado NULL", social.String)
				}
			} else if !social.Valid || social.String != *wantSocial {
				return fmt.Errorf("social_name = %q, esperado %q", social.String, *wantSocial)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	assertPerson(tenantA, nullID, "Nome civil nulo", nil, "Nome civil nulo")
	assertPerson(tenantA, emptyID, "Nome civil vazio", nil, "Nome civil vazio")
	social := "Nome de uso"
	assertPerson(tenantA, filledID, "Nome civil preenchido", &social, "Nome de uso")

	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE party.person SET social_name = 'Nome atualizado' WHERE party_id = $1`, filledID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	social = "Nome atualizado"
	assertPerson(tenantA, filledID, "Nome civil preenchido", &social, "Nome atualizado")

	if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
		var visible int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM party.person WHERE party_id = $1`, hiddenID).Scan(&visible); err != nil {
			return err
		}
		if visible != 0 {
			return fmt.Errorf("RLS expôs pessoa do tenant B")
		}
		if _, err := tx.Exec(ctx, `UPDATE party.person SET social_name = 'Invasao' WHERE party_id = $1`, hiddenID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	social = "Nome B"
	assertPerson(tenantB, hiddenID, "Nome civil tenant B", &social, "Nome B")
}
