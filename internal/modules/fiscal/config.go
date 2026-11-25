package fiscal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrFiscalConfigNotFound = errors.New("configuracao fiscal nao encontrada")
	ErrInvalidFiscalConfig  = errors.New("configuracao fiscal invalida")
)

// FiscalConfig e somente configuracao e metadado informado pelo contribuinte.
// Nenhum segredo de certificado faz parte deste contrato; o certificado usa
// uma referencia opaca ao cofre em fiscal.certificate.storage_ref.
type FiscalConfig struct {
	ID, TenantID, CompanyID uuid.UUID
	EstablishmentID         *uuid.UUID
	Regime                  string
	Environment             string
	UF                      string
	MunicipalityCode        string
	SeriesNFSe              string
	SeriesNFe               string
	StateRegistration       *string
	MunicipalRegistration   *string
	Status                  string
	ConfigVersion           int
	ValidFrom               time.Time
	ValidTo                 *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// ConfigRepository persiste a configuracao fiscal na celula do tenant.
// Tenant e empresa sao sempre obtidos do contexto autenticado; valores
// enviados no corpo nao podem trocar o escopo da operacao.
type ConfigRepository struct{ database *db.DB }

func NewConfigRepository(database *db.DB) (*ConfigRepository, error) {
	if database == nil {
		return nil, errors.New("banco fiscal nulo")
	}
	return &ConfigRepository{database: database}, nil
}

func (r *ConfigRepository) CreateConfig(ctx context.Context, config FiscalConfig) (FiscalConfig, error) {
	tenantID, companyID, err := configScope(ctx, config)
	if err != nil {
		return FiscalConfig{}, err
	}
	if err := validateFiscalConfig(config); err != nil {
		return FiscalConfig{}, err
	}

	var result FiscalConfig
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return scanFiscalConfig(tx.QueryRow(ctx, `
INSERT INTO fiscal.fiscal_config
		(tenant_id, company_id, establishment_id, regime, environment, uf,
		 municipality_code, series_nfse, series_nfe, state_registration,
		 municipal_registration, status, config_version, valid_from, valid_to)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
RETURNING id, tenant_id, company_id, establishment_id, regime, environment,
		  COALESCE(uf, ''), COALESCE(municipality_code, ''), COALESCE(series_nfse, ''),
		  COALESCE(series_nfe, ''), state_registration, municipal_registration,
		  status, config_version, valid_from, valid_to, created_at, updated_at`,
			tenantID, companyID, nullUUIDPtr(config.EstablishmentID), config.Regime,
			defaultConfigString(config.Environment, "homologacao"), nullString(config.UF),
			nullString(config.MunicipalityCode), nullString(config.SeriesNFSe),
			nullString(config.SeriesNFe), nullStringPtr(config.StateRegistration),
			nullStringPtr(config.MunicipalRegistration), defaultConfigString(config.Status, "active"),
			defaultConfigVersion(config.ConfigVersion), dateOnly(config.ValidFrom), datePtr(config.ValidTo)), &result)
	})
	if err != nil {
		return FiscalConfig{}, fmt.Errorf("criar configuracao fiscal: %w", err)
	}
	return result, nil
}

func (r *ConfigRepository) GetConfig(ctx context.Context, id uuid.UUID) (FiscalConfig, error) {
	tenantID, companyID, err := configScope(ctx, FiscalConfig{})
	if err != nil {
		return FiscalConfig{}, err
	}
	var result FiscalConfig
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return scanFiscalConfig(tx.QueryRow(ctx, `
SELECT id, tenant_id, company_id, establishment_id, regime, environment,
		   COALESCE(uf, ''), COALESCE(municipality_code, ''), COALESCE(series_nfse, ''),
		   COALESCE(series_nfe, ''), state_registration, municipal_registration,
		   status, config_version, valid_from, valid_to, created_at, updated_at
  FROM fiscal.fiscal_config
 WHERE id = $1 AND tenant_id = $2 AND company_id = $3`, id, tenantID, companyID), &result)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return FiscalConfig{}, ErrFiscalConfigNotFound
	}
	if err != nil {
		return FiscalConfig{}, fmt.Errorf("consultar configuracao fiscal: %w", err)
	}
	return result, nil
}

func configScope(ctx context.Context, config FiscalConfig) (uuid.UUID, uuid.UUID, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if config.TenantID != uuid.Nil && config.TenantID != tenantID {
		return uuid.Nil, uuid.Nil, ErrTenantMismatch
	}
	if config.CompanyID != uuid.Nil && config.CompanyID != companyID {
		return uuid.Nil, uuid.Nil, ErrTenantMismatch
	}
	return tenantID, companyID, nil
}

func validateFiscalConfig(config FiscalConfig) error {
	if config.Regime == "" || config.ValidFrom.IsZero() {
		return ErrInvalidFiscalConfig
	}
	if config.ValidTo != nil && !config.ValidTo.After(config.ValidFrom) {
		return ErrInvalidFiscalConfig
	}
	return nil
}

func scanFiscalConfig(row rowScanner, config *FiscalConfig) error {
	return row.Scan(
		&config.ID, &config.TenantID, &config.CompanyID, &config.EstablishmentID,
		&config.Regime, &config.Environment, &config.UF, &config.MunicipalityCode,
		&config.SeriesNFSe, &config.SeriesNFe, &config.StateRegistration,
		&config.MunicipalRegistration, &config.Status, &config.ConfigVersion,
		&config.ValidFrom, &config.ValidTo, &config.CreatedAt, &config.UpdatedAt,
	)
}

func defaultConfigString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultConfigVersion(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func dateOnly(value time.Time) time.Time {
	return time.Date(value.UTC().Year(), value.UTC().Month(), value.UTC().Day(), 0, 0, 0, 0, time.UTC)
}

func datePtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return dateOnly(*value)
}
