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

func TestFiscalConfigPostgresRLSCompanyScopeValidityAndCleanup(t *testing.T) {
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
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })

	var role string
	var bypass bool
	if err := database.WithTenantTx(db.WithTenant(ctx, uuid.New()), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT current_user, rolsuper OR rolbypassrls
			  FROM pg_roles WHERE rolname = current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if role != "amparius_app" || bypass {
		t.Fatalf("esperado amparius_app sem SUPERUSER/BYPASSRLS: role=%s bypass=%v", role, bypass)
	}

	tenantA, tenantB := uuid.New(), uuid.New()
	companyA, companyB, companyOtherA := uuid.New(), uuid.New(), uuid.New()
	establishmentA, establishmentB := uuid.New(), uuid.New()
	cleanup := func() {
		cleanupCtx := context.Background()
		cleanupTx, cleanupErr := admin.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = cleanupTx.Rollback(cleanupCtx) }()
			for _, query := range []string{
				`DELETE FROM fiscal.fiscal_config WHERE tenant_id = ANY($1::uuid[])`,
				`DELETE FROM identity.establishment WHERE id = ANY($1::uuid[])`,
				`DELETE FROM identity.company WHERE id = ANY($1::uuid[])`,
				`DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])`,
			} {
				if _, cleanupErr = cleanupTx.Exec(cleanupCtx, query, []uuid.UUID{tenantA, tenantB, companyA, companyB, companyOtherA, establishmentA, establishmentB}); cleanupErr != nil {
					break
				}
			}
			if cleanupErr == nil {
				cleanupErr = cleanupTx.Commit(cleanupCtx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup administrativo fiscal_config: %v", cleanupErr)
		}
		var remaining int
		if err := admin.QueryRow(cleanupCtx, `SELECT count(*) FROM fiscal.fiscal_config WHERE tenant_id = ANY($1::uuid[])`, []uuid.UUID{tenantA, tenantB}).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup fiscal_config: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup fiscal_config deixou %d registro(s)", remaining)
		}
	}
	t.Cleanup(cleanup)

	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.tenant (id,name,document,regime,status)
		VALUES ($1,$2,$3,'economic_group','active'),($4,$5,$6,'economic_group','active')`,
		tenantA, "Tenant fiscal A", "fiscal-config-"+tenantA.String(),
		tenantB, "Tenant fiscal B", "fiscal-config-"+tenantB.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.company (id,tenant_id,document,name,kind,status)
		VALUES ($1,$2,$3,'Empresa fiscal A','funeral','active'),($4,$5,$6,'Empresa fiscal B','funeral','active'),($7,$8,$9,'Outra empresa fiscal A','funeral','active')`,
		companyA, tenantA, "cnpj-config-"+companyA.String(), companyB, tenantB, "cnpj-config-"+companyB.String(), companyOtherA, tenantA, "cnpj-config-"+companyOtherA.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.establishment (id,tenant_id,company_id,document,name,status)
		VALUES ($1,$2,$3,$4,'Estabelecimento fiscal A','active'),($5,$6,$7,$8,'Estabelecimento fiscal B','active')`,
		establishmentA, tenantA, companyA, "est-config-"+establishmentA.String(), establishmentB, tenantB, companyB, "est-config-"+establishmentB.String()); err != nil {
		t.Fatal(err)
	}

	validFrom := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	stateRegistration := "IE-SINTETICA-A"
	municipalRegistration := "IM-SINTETICA-A"
	repository, err := fiscal.NewConfigRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	config, err := repository.CreateConfig(
		db.WithCompany(db.WithTenant(ctx, tenantA), companyA),
		fiscal.FiscalConfig{
			CompanyID:             companyA,
			EstablishmentID:       &establishmentA,
			Regime:                "geral",
			Environment:           "homologacao",
			UF:                    "CE",
			MunicipalityCode:      "2304400",
			StateRegistration:     &stateRegistration,
			MunicipalRegistration: &municipalRegistration,
			ValidFrom:             validFrom,
			ValidTo:               &validTo,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if config.TenantID != tenantA || config.CompanyID != companyA || config.EstablishmentID == nil || *config.EstablishmentID != establishmentA {
		t.Fatalf("escopo retornado incorreto: %+v", config)
	}
	if config.ValidTo == nil || !config.ValidTo.Equal(validTo) {
		t.Fatalf("vigencia retornada incorreta: %v..%v", config.ValidFrom, config.ValidTo)
	}

	loaded, err := repository.GetConfig(db.WithCompany(db.WithTenant(ctx, tenantA), companyA), config.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MunicipalRegistration == nil || *loaded.MunicipalRegistration != municipalRegistration {
		t.Fatalf("inscricao municipal nao persistida: %+v", loaded)
	}

	if _, err := repository.GetConfig(db.WithCompany(db.WithTenant(ctx, tenantB), companyB), config.ID); !errors.Is(err, fiscal.ErrFiscalConfigNotFound) {
		t.Fatalf("isolamento entre tenant/empresa deveria ocultar config: %v", err)
	}
	var visible int
	if err := database.WithTenantTx(db.WithCompany(db.WithTenant(ctx, tenantB), companyB), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM fiscal.fiscal_config`).Scan(&visible)
	}); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("RLS expôs %d config(s) ao tenant B", visible)
	}
	var sameTenantWrongCompany int
	if err := database.WithTenantTx(db.WithCompany(db.WithTenant(ctx, tenantA), companyOtherA), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM fiscal.fiscal_config`).Scan(&sameTenantWrongCompany)
	}); err != nil {
		t.Fatal(err)
	}
	if sameTenantWrongCompany != 0 {
		t.Fatalf("RLS expôs %d config(s) para outra empresa do mesmo tenant", sameTenantWrongCompany)
	}

	if _, err := repository.CreateConfig(db.WithCompany(db.WithTenant(ctx, tenantA), companyA), fiscal.FiscalConfig{
		CompanyID: companyB, Regime: "geral", ValidFrom: validFrom,
	}); !errors.Is(err, fiscal.ErrTenantMismatch) {
		t.Fatalf("empresa fora do contexto deveria falhar fechado: %v", err)
	}

	if _, err := repository.CreateConfig(db.WithCompany(db.WithTenant(ctx, tenantA), companyA), fiscal.FiscalConfig{
		EstablishmentID: &establishmentA, Regime: "geral", ValidFrom: validFrom,
		ValidTo: &validTo,
	}); err == nil {
		t.Fatal("vigencias sobrepostas deveriam ser rejeitadas")
	}

	if _, err := repository.CreateConfig(db.WithTenant(ctx, tenantA), fiscal.FiscalConfig{Regime: "geral", ValidFrom: validFrom}); !errors.Is(err, db.ErrCompanyRequired) {
		t.Fatalf("empresa ausente deveria falhar antes da escrita: %v", err)
	}
}
