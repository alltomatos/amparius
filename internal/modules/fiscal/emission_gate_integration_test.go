package fiscal_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestEmissionGatePostgresFailClosedValidityCrossTenantAndCleanup(t *testing.T) {
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
	companyA, companyOtherA, companyExpired, companyCertExpired, companyNoCert := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	companyB := uuid.New()
	establishmentA, establishmentB := uuid.New(), uuid.New()
	tenantIDs := []uuid.UUID{tenantA, tenantB}
	cleanup := func() {
		cleanupCtx := context.Background()
		tx, cleanupErr := admin.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, query := range []string{
				`DELETE FROM fiscal.fiscal_config WHERE tenant_id = ANY($1::uuid[])`,
				`DELETE FROM fiscal.certificate WHERE tenant_id = ANY($1::uuid[])`,
				`DELETE FROM identity.establishment WHERE tenant_id = ANY($1::uuid[])`,
				`DELETE FROM identity.company WHERE tenant_id = ANY($1::uuid[])`,
				`DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])`,
			} {
				if _, cleanupErr = tx.Exec(cleanupCtx, query, tenantIDs); cleanupErr != nil {
					break
				}
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(cleanupCtx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup administrativo fail-closed: %v", cleanupErr)
		}
		var remainingConfigs, remainingCertificates int
		if err := admin.QueryRow(cleanupCtx, `SELECT count(*) FROM fiscal.fiscal_config WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingConfigs); err != nil {
			t.Errorf("confirmar cleanup fiscal_config: %v", err)
		}
		if err := admin.QueryRow(cleanupCtx, `SELECT count(*) FROM fiscal.certificate WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&remainingCertificates); err != nil {
			t.Errorf("confirmar cleanup fiscal_certificate: %v", err)
		}
		if remainingConfigs != 0 || remainingCertificates != 0 {
			t.Errorf("cleanup deixou config=%d certificado=%d", remainingConfigs, remainingCertificates)
		}
	}
	t.Cleanup(cleanup)

	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.tenant (id,name,document,regime,status)
		VALUES ($1,$2,$3,'economic_group','active'),($4,$5,$6,'economic_group','active')`,
		tenantA, "Tenant fail-closed A", "gate-tenant-"+tenantA.String(),
		tenantB, "Tenant fail-closed B", "gate-tenant-"+tenantB.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.company (id,tenant_id,document,name,kind,status)
		VALUES ($1,$2,$3,'Empresa A','funeral','active'),($4,$5,$6,'Empresa outra A','funeral','active'),
		       ($7,$8,$9,'Empresa expirada A','funeral','active'),($10,$11,$12,'Empresa cert expirado A','funeral','active'),
		       ($13,$14,$15,'Empresa sem cert A','funeral','active'),($16,$17,$18,'Empresa B','funeral','active')`,
		companyA, tenantA, "gate-cnpj-"+companyA.String(),
		companyOtherA, tenantA, "gate-cnpj-"+companyOtherA.String(),
		companyExpired, tenantA, "gate-cnpj-"+companyExpired.String(),
		companyCertExpired, tenantA, "gate-cnpj-"+companyCertExpired.String(),
		companyNoCert, tenantA, "gate-cnpj-"+companyNoCert.String(),
		companyB, tenantB, "gate-cnpj-"+companyB.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.establishment (id,tenant_id,company_id,document,name,status)
		VALUES ($1,$2,$3,$4,'Estabelecimento A','active'),($5,$6,$7,$8,'Estabelecimento B','active')`,
		establishmentA, tenantA, companyA, "gate-est-"+establishmentA.String(),
		establishmentB, tenantB, companyB, "gate-est-"+establishmentB.String()); err != nil {
		t.Fatal(err)
	}

	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	expiredFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	expiredTo := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	repository, err := fiscal.NewConfigRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	createConfig := func(tenantID, companyID uuid.UUID, establishmentID *uuid.UUID, from, to time.Time) fiscal.FiscalConfig {
		t.Helper()
		config, err := repository.CreateConfig(
			db.WithCompany(db.WithTenant(ctx, tenantID), companyID),
			fiscal.FiscalConfig{
				CompanyID: companyID, EstablishmentID: establishmentID, Regime: "geral",
				Environment: "homologacao", UF: "CE", MunicipalityCode: "2304400",
				ValidFrom: from, ValidTo: &to,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return config
	}
	defaultConfig := createConfig(tenantA, companyA, nil, validFrom, validTo)
	specificConfig := createConfig(tenantA, companyA, &establishmentA, validFrom, validTo.AddDate(0, -6, 0))
	createConfig(tenantA, companyExpired, nil, expiredFrom, expiredTo)
	createConfig(tenantA, companyCertExpired, nil, validFrom, validTo)
	createConfig(tenantA, companyNoCert, nil, validFrom, validTo)
	tenantBConfig := createConfig(tenantB, companyB, nil, validFrom, validTo)

	insertCertificate := func(tenantID, companyID uuid.UUID, fingerSeed string, from, until time.Time) fiscal.CertificateReference {
		t.Helper()
		sum := sha256.Sum256([]byte(fingerSeed))
		fingerprint := hex.EncodeToString(sum[:])
		var certificate fiscal.CertificateReference
		if err := admin.QueryRow(ctx, `
			INSERT INTO fiscal.certificate
				(tenant_id, company_id, kind, subject_cn, issuer_cn, serial_number, fingerprint_sha256,
				 valid_from, valid_until, storage_kind, storage_ref, status)
			VALUES ($1,$2,'a1','Empresa Sintetica','AC Sintetica',$3,$4,$5,$6,'vault',$7,'active')
			RETURNING id, tenant_id, company_id, kind, subject_cn, issuer_cn, serial_number,
			          fingerprint_sha256, valid_from, valid_until, storage_kind, storage_ref, status`,
			tenantID, companyID, "serial-"+fingerSeed[:8], fingerprint, from, until,
			"vault://fiscal/"+fingerSeed).Scan(
			&certificate.ID, &certificate.TenantID, &certificate.CompanyID, &certificate.Kind,
			&certificate.SubjectCN, &certificate.IssuerCN, &certificate.SerialNumber,
			&certificate.FingerprintSHA256, &certificate.ValidFrom, &certificate.ValidUntil,
			&certificate.StorageKind, &certificate.StorageRef, &certificate.Status,
		); err != nil {
			t.Fatal(err)
		}
		return certificate
	}
	validCertificate := insertCertificate(tenantA, companyA, "cert-a-"+uuid.NewString(), validFrom, validTo)
	insertCertificate(tenantA, companyExpired, "cert-exp-"+uuid.NewString(), validFrom, validTo)
	insertCertificate(tenantA, companyCertExpired, "cert-bad-"+uuid.NewString(), expiredFrom, expiredTo)
	tenantBCertificate := insertCertificate(tenantB, companyB, "cert-b-"+uuid.NewString(), validFrom, validTo)

	gate, err := fiscal.NewEmissionGateFromDatabase(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	t.Run("vigente resolve padrao e certificado", func(t *testing.T) {
		setup, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyA), fiscal.EmissionCriteria{Competence: competence})
		if err != nil {
			t.Fatal(err)
		}
		if setup.Config.ID != defaultConfig.ID {
			t.Fatalf("configuracao padrao esperada %s, veio %s", defaultConfig.ID, setup.Config.ID)
		}
		if setup.Certificate.ID != validCertificate.ID || setup.Certificate.StorageRef != validCertificate.StorageRef {
			t.Fatalf("certificado vigente esperado %s", validCertificate.ID)
		}
	})

	t.Run("estabelecimento tem precedencia", func(t *testing.T) {
		setup, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyA), fiscal.EmissionCriteria{
			EstablishmentID: &establishmentA, Competence: competence,
		})
		if err != nil {
			t.Fatal(err)
		}
		if setup.Config.ID != specificConfig.ID {
			t.Fatalf("configuracao do estabelecimento esperada %s, veio %s", specificConfig.ID, setup.Config.ID)
		}
	})

	t.Run("configuracao expirada bloqueia", func(t *testing.T) {
		_, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyExpired), fiscal.EmissionCriteria{Competence: competence})
		if !errors.Is(err, fiscal.ErrFiscalConfigExpired) || fiscal.EmissionErrorCode(err) != fiscal.CodeFiscalConfigInvalid {
			t.Fatalf("esperado FISCAL_CONFIG_INVALID por expiracao: %v", err)
		}
		_, err = gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyA), fiscal.EmissionCriteria{
			Competence: time.Date(2027, 6, 15, 0, 0, 0, 0, time.UTC),
		})
		if !errors.Is(err, fiscal.ErrFiscalConfigExpired) {
			t.Fatalf("competencia fora das duas vigencias deveria bloquear: %v", err)
		}
	})

	t.Run("configuracao ausente bloqueia", func(t *testing.T) {
		_, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyOtherA), fiscal.EmissionCriteria{Competence: competence})
		if !errors.Is(err, fiscal.ErrFiscalConfigMissing) || fiscal.EmissionErrorCode(err) != fiscal.CodeFiscalConfigMissing {
			t.Fatalf("esperado FISCAL_CONFIG_MISSING: %v", err)
		}
	})

	t.Run("certificado expirado ou ausente bloqueia", func(t *testing.T) {
		_, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyCertExpired), fiscal.EmissionCriteria{Competence: competence})
		if !errors.Is(err, fiscal.ErrFiscalCertificateExpired) || fiscal.EmissionErrorCode(err) != fiscal.CodeFiscalCertificateInvalid {
			t.Fatalf("esperado FISCAL_CERTIFICATE_INVALID por expiracao: %v", err)
		}
		_, err = gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyNoCert), fiscal.EmissionCriteria{Competence: competence})
		if !errors.Is(err, fiscal.ErrFiscalCertificateMissing) || fiscal.EmissionErrorCode(err) != fiscal.CodeFiscalCertificateInvalid {
			t.Fatalf("esperado FISCAL_CERTIFICATE_INVALID por ausencia: %v", err)
		}
	})

	t.Run("tenant e empresa cruzados nao se enxergam", func(t *testing.T) {
		setup, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantB), companyB), fiscal.EmissionCriteria{Competence: competence})
		if err != nil {
			t.Fatal(err)
		}
		if setup.Config.ID != tenantBConfig.ID || setup.Certificate.ID != tenantBCertificate.ID {
			t.Fatalf("tenant B deveria resolver somente seu proprio setup")
		}
		if _, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyB), fiscal.EmissionCriteria{Competence: competence}); !errors.Is(err, fiscal.ErrFiscalConfigMissing) {
			t.Fatalf("empresa B com tenant A deveria falhar fechado: %v", err)
		}
		if _, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantB), companyA), fiscal.EmissionCriteria{Competence: competence}); !errors.Is(err, fiscal.ErrFiscalConfigMissing) {
			t.Fatalf("empresa A com tenant B deveria falhar fechado: %v", err)
		}
	})

	t.Run("estabelecimento de outro tenant/empresa bloqueia", func(t *testing.T) {
		_, err := gate.Resolve(db.WithCompany(db.WithTenant(ctx, tenantA), companyA), fiscal.EmissionCriteria{
			EstablishmentID: &establishmentB, Competence: competence,
		})
		if !errors.Is(err, fiscal.ErrEstablishmentNotInScope) {
			t.Fatalf("estabelecimento de outro tenant deveria bloquear: %v", err)
		}
	})

	t.Run("RLS de certificado exige tenant e empresa", func(t *testing.T) {
		count := func(ctx context.Context, tx pgx.Tx) (int, error) {
			var total int
			err := tx.QueryRow(ctx, `SELECT count(*) FROM fiscal.certificate`).Scan(&total)
			return total, err
		}
		var own, noCompany, otherCompany int
		if err := database.WithTenantTx(db.WithCompany(db.WithTenant(ctx, tenantA), companyA), func(ctx context.Context, tx pgx.Tx) error {
			var err error
			own, err = count(ctx, tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if own != 1 {
			t.Fatalf("RLS deveria expor 1 certificado da empresa A, expos %d", own)
		}
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantA), func(ctx context.Context, tx pgx.Tx) error {
			var err error
			noCompany, err = count(ctx, tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if noCompany != 0 {
			t.Fatalf("sem app.company_id nenhum certificado pode ser visivel, expos %d", noCompany)
		}
		if err := database.WithTenantTx(db.WithCompany(db.WithTenant(ctx, tenantA), companyB), func(ctx context.Context, tx pgx.Tx) error {
			var err error
			otherCompany, err = count(ctx, tx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if otherCompany != 0 {
			t.Fatalf("RLS deveria ocultar certificado do tenant B, expos %d", otherCompany)
		}
	})
}
