package fiscal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Codigos de contrato consumidos pela camada HTTP. Eles nao carregam dado
// pessoal, segredo nem mensagem de SQL; sao apenas identificadores estaveis.
const (
	CodeFiscalConfigMissing      = "FISCAL_CONFIG_MISSING"
	CodeFiscalConfigInvalid      = "FISCAL_CONFIG_INVALID"
	CodeFiscalCertificateInvalid = "FISCAL_CERTIFICATE_INVALID"
)

var (
	ErrInvalidCompetence          = errors.New("competencia fiscal invalida")
	ErrEstablishmentNotInScope    = errors.New("estabelecimento fora do escopo da empresa")
	ErrFiscalConfigMissing        = errors.New("configuracao fiscal ausente para a competencia")
	ErrFiscalConfigExpired        = errors.New("configuracao fiscal fora da vigencia")
	ErrFiscalConfigInactive       = errors.New("configuracao fiscal inativa")
	ErrFiscalConfigAmbiguous      = errors.New("mais de uma configuracao fiscal vigente")
	ErrFiscalConfigMalformed      = errors.New("configuracao fiscal malformada")
	ErrFiscalCertificateMissing   = errors.New("certificado fiscal ausente")
	ErrFiscalCertificateExpired   = errors.New("certificado fiscal expirado")
	ErrFiscalCertificateInactive  = errors.New("certificado fiscal inativo")
	ErrFiscalCertificateAmbiguous = errors.New("mais de um certificado fiscal vigente")
	ErrFiscalCertificateMalformed = errors.New("certificado fiscal malformado")
)

var fingerprintHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// CertificateReference contem somente metadados do certificado e a referencia
// opaca ao cofre (Vault/KMS). Senha, PIN, chave privada, PFX, token e bytes
// criptograficos nao fazem parte deste contrato e nunca podem ser logados.
type CertificateReference struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	CompanyID         uuid.UUID
	Kind              string
	SubjectCN         string
	IssuerCN          string
	SerialNumber      string
	FingerprintSHA256 string
	StorageKind       string
	StorageRef        string
	Status            string
	ValidFrom         time.Time
	ValidUntil        time.Time
}

// EmissionCriteria e montado pelo caso de uso a partir da sessao autenticada.
// Tenant e empresa vem do contexto confiavel; o corpo HTTP nao define escopo.
// IssuedAt e a referencia do certificado; quando zero, usa a competencia.
type EmissionCriteria struct {
	EstablishmentID *uuid.UUID
	Competence      time.Time
	IssuedAt        time.Time
}

// EmissionSetup e o resultado de uma consulta bem-sucedida: a configuracao
// vigente e a referencia segura do certificado. Nenhuma aliquota ou regra
// tributaria e calculada ou inventada aqui.
type EmissionSetup struct {
	Config      FiscalConfig
	Certificate CertificateReference
}

// EmissionErrorCode traduz o erro tipado para o codigo de contrato HTTP.
// Erros nao classificados (por exemplo indisponibilidade de banco) retornam
// vazio e devem ser tratados como 503 pela borda, nunca como sucesso.
func EmissionErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrFiscalConfigMissing):
		return CodeFiscalConfigMissing
	case errors.Is(err, ErrFiscalCertificateMissing),
		errors.Is(err, ErrFiscalCertificateExpired),
		errors.Is(err, ErrFiscalCertificateInactive),
		errors.Is(err, ErrFiscalCertificateAmbiguous),
		errors.Is(err, ErrFiscalCertificateMalformed):
		return CodeFiscalCertificateInvalid
	case errors.Is(err, ErrFiscalConfigExpired),
		errors.Is(err, ErrFiscalConfigInactive),
		errors.Is(err, ErrFiscalConfigAmbiguous),
		errors.Is(err, ErrFiscalConfigMalformed),
		errors.Is(err, ErrInvalidCompetence),
		errors.Is(err, ErrEstablishmentNotInScope):
		return CodeFiscalConfigInvalid
	default:
		return ""
	}
}

type fiscalEmissionSnapshot struct {
	configs              []FiscalConfig
	certificates         []CertificateReference
	establishmentInScope bool
}

type fiscalEmissionStore interface {
	snapshot(ctx context.Context, tenantID, companyID uuid.UUID, establishmentID *uuid.UUID) (fiscalEmissionSnapshot, error)
}

// EmissionGate consulta configuracao e certificado vigentes e falha fechado
// quando qualquer requisito estiver ausente, expirado, inativo ou ambiguo.
type EmissionGate struct {
	store fiscalEmissionStore
}

func NewEmissionGate(store fiscalEmissionStore) (*EmissionGate, error) {
	if store == nil {
		return nil, errors.New("repositorio fiscal de emissao nulo")
	}
	return &EmissionGate{store: store}, nil
}

func NewEmissionGateFromDatabase(database *db.DB) (*EmissionGate, error) {
	store, err := NewEmissionStore(database)
	if err != nil {
		return nil, err
	}
	return NewEmissionGate(store)
}

func (g *EmissionGate) Resolve(ctx context.Context, criteria EmissionCriteria) (EmissionSetup, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return EmissionSetup{}, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return EmissionSetup{}, err
	}
	competence, err := normalizeCompetence(criteria.Competence)
	if err != nil {
		return EmissionSetup{}, err
	}
	certificateInstant := criteria.IssuedAt
	if certificateInstant.IsZero() {
		certificateInstant = criteria.Competence
	}
	certificateInstant = certificateInstant.UTC()

	snapshot, err := g.store.snapshot(ctx, tenantID, companyID, criteria.EstablishmentID)
	if err != nil {
		return EmissionSetup{}, fmt.Errorf("consultar configuracao fiscal: %w", err)
	}
	if criteria.EstablishmentID != nil && !snapshot.establishmentInScope {
		return EmissionSetup{}, ErrEstablishmentNotInScope
	}
	config, err := resolveConfig(snapshot.configs, criteria.EstablishmentID, competence)
	if err != nil {
		return EmissionSetup{}, err
	}
	certificate, err := resolveCertificate(snapshot.certificates, certificateInstant)
	if err != nil {
		return EmissionSetup{}, err
	}
	return EmissionSetup{Config: config, Certificate: certificate}, nil
}

// normalizeCompetence reduz a competencia a data UTC, que e a granularidade
// das colunas valid_from/valid_to da configuracao fiscal.
func normalizeCompetence(competence time.Time) (time.Time, error) {
	if competence.IsZero() {
		return time.Time{}, ErrInvalidCompetence
	}
	return dateOnly(competence), nil
}

func resolveConfig(configs []FiscalConfig, establishmentID *uuid.UUID, competence time.Time) (FiscalConfig, error) {
	competence = dateOnly(competence)
	var specific, defaults []FiscalConfig
	var sawInactive, sawExpired, sawMalformed bool
	for _, config := range configs {
		inSpecific := establishmentID != nil && config.EstablishmentID != nil && *config.EstablishmentID == *establishmentID
		inDefault := config.EstablishmentID == nil
		if !inSpecific && !inDefault {
			continue
		}
		if err := validateResolvedConfig(config); err != nil {
			sawMalformed = true
			continue
		}
		if config.Status != "active" {
			sawInactive = true
			continue
		}
		if !effectiveAt(dateOnly(config.ValidFrom), config.ValidTo, competence) {
			sawExpired = true
			continue
		}
		if inSpecific {
			specific = append(specific, config)
		} else {
			defaults = append(defaults, config)
		}
	}
	switch {
	case len(specific) > 1 || len(defaults) > 1:
		return FiscalConfig{}, ErrFiscalConfigAmbiguous
	case len(specific) == 1:
		return specific[0], nil
	case len(defaults) == 1:
		return defaults[0], nil
	case sawExpired:
		return FiscalConfig{}, ErrFiscalConfigExpired
	case sawInactive:
		return FiscalConfig{}, ErrFiscalConfigInactive
	case sawMalformed:
		return FiscalConfig{}, ErrFiscalConfigMalformed
	default:
		return FiscalConfig{}, ErrFiscalConfigMissing
	}
}

func resolveCertificate(certificates []CertificateReference, instant time.Time) (CertificateReference, error) {
	instant = instant.UTC()
	var active []CertificateReference
	var sawInactive, sawExpired, sawMalformed bool
	for _, certificate := range certificates {
		if err := validateCertificateReference(certificate); err != nil {
			sawMalformed = true
			continue
		}
		if certificate.Status != "active" {
			sawInactive = true
			continue
		}
		if !certificateValidAt(certificate, instant) {
			sawExpired = true
			continue
		}
		active = append(active, certificate)
	}
	switch {
	case len(active) > 1:
		return CertificateReference{}, ErrFiscalCertificateAmbiguous
	case len(active) == 1:
		return active[0], nil
	case sawExpired:
		return CertificateReference{}, ErrFiscalCertificateExpired
	case sawInactive:
		return CertificateReference{}, ErrFiscalCertificateInactive
	case sawMalformed:
		return CertificateReference{}, ErrFiscalCertificateMalformed
	default:
		return CertificateReference{}, ErrFiscalCertificateMissing
	}
}

func validateResolvedConfig(config FiscalConfig) error {
	if config.ID == uuid.Nil || config.TenantID == uuid.Nil || config.CompanyID == uuid.Nil {
		return ErrFiscalConfigMalformed
	}
	if strings.TrimSpace(config.Regime) == "" || strings.TrimSpace(config.Environment) == "" {
		return ErrFiscalConfigMalformed
	}
	if config.ValidFrom.IsZero() {
		return ErrFiscalConfigMalformed
	}
	if config.ValidTo != nil && !config.ValidTo.After(config.ValidFrom) {
		return ErrFiscalConfigMalformed
	}
	return nil
}

func validateCertificateReference(certificate CertificateReference) error {
	if certificate.ID == uuid.Nil || certificate.TenantID == uuid.Nil || certificate.CompanyID == uuid.Nil {
		return ErrFiscalCertificateMalformed
	}
	if strings.TrimSpace(certificate.Kind) == "" ||
		strings.TrimSpace(certificate.StorageKind) == "" ||
		strings.TrimSpace(certificate.StorageRef) == "" {
		return ErrFiscalCertificateMalformed
	}
	if !fingerprintHex.MatchString(certificate.FingerprintSHA256) {
		return ErrFiscalCertificateMalformed
	}
	if certificate.ValidFrom.IsZero() || !certificate.ValidUntil.After(certificate.ValidFrom) {
		return ErrFiscalCertificateMalformed
	}
	return nil
}

func certificateValidAt(certificate CertificateReference, instant time.Time) bool {
	return !instant.Before(certificate.ValidFrom) && instant.Before(certificate.ValidUntil)
}

// EmissionStore consulta configuracao e certificado na celula do tenant.
// A consulta inteira roda em uma transacao com app.tenant_id/app.company_id,
// de modo que RLS e o filtro explicito se reforcam.
type EmissionStore struct{ database *db.DB }

func NewEmissionStore(database *db.DB) (*EmissionStore, error) {
	if database == nil {
		return nil, errors.New("banco fiscal nulo")
	}
	return &EmissionStore{database: database}, nil
}

func (s *EmissionStore) snapshot(ctx context.Context, tenantID, companyID uuid.UUID, establishmentID *uuid.UUID) (fiscalEmissionSnapshot, error) {
	var snapshot fiscalEmissionSnapshot
	err := s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if establishmentID == nil {
			snapshot.establishmentInScope = true
		} else if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM identity.establishment
     WHERE tenant_id = $1 AND company_id = $2 AND id = $3
)`, tenantID, companyID, *establishmentID).Scan(&snapshot.establishmentInScope); err != nil {
			return fmt.Errorf("consultar estabelecimento: %w", err)
		}
		rows, err := tx.Query(ctx, `
SELECT id, tenant_id, company_id, establishment_id, regime, environment,
       COALESCE(uf, ''), COALESCE(municipality_code, ''), COALESCE(series_nfse, ''),
       COALESCE(series_nfe, ''), state_registration, municipal_registration,
       status, config_version, valid_from, valid_to, created_at, updated_at
  FROM fiscal.fiscal_config
 WHERE tenant_id = $1 AND company_id = $2
 ORDER BY valid_from DESC, id`, tenantID, companyID)
		if err != nil {
			return fmt.Errorf("consultar configuracao fiscal: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var config FiscalConfig
			if err := scanFiscalConfig(rows, &config); err != nil {
				return fmt.Errorf("ler configuracao fiscal: %w", err)
			}
			snapshot.configs = append(snapshot.configs, config)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterar configuracoes fiscais: %w", err)
		}

		certRows, err := tx.Query(ctx, `
SELECT id, tenant_id, company_id, kind, subject_cn, COALESCE(issuer_cn, ''),
       serial_number, fingerprint_sha256, valid_from, valid_until,
       storage_kind, storage_ref, status
  FROM fiscal.certificate
 WHERE tenant_id = $1 AND company_id = $2
 ORDER BY valid_until DESC, id`, tenantID, companyID)
		if err != nil {
			return fmt.Errorf("consultar certificado fiscal: %w", err)
		}
		defer certRows.Close()
		for certRows.Next() {
			var certificate CertificateReference
			if err := scanCertificateReference(certRows, &certificate); err != nil {
				return fmt.Errorf("ler certificado fiscal: %w", err)
			}
			snapshot.certificates = append(snapshot.certificates, certificate)
		}
		return certRows.Err()
	})
	if err != nil {
		return fiscalEmissionSnapshot{}, err
	}
	return snapshot, nil
}

func scanCertificateReference(row rowScanner, certificate *CertificateReference) error {
	return row.Scan(
		&certificate.ID, &certificate.TenantID, &certificate.CompanyID, &certificate.Kind,
		&certificate.SubjectCN, &certificate.IssuerCN, &certificate.SerialNumber,
		&certificate.FingerprintSHA256, &certificate.ValidFrom, &certificate.ValidUntil,
		&certificate.StorageKind, &certificate.StorageRef, &certificate.Status,
	)
}
