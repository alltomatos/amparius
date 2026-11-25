package fiscal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrDocumentNotFound = errors.New("documento fiscal nao encontrado")
	ErrInvalidDocument  = errors.New("documento fiscal invalido")
	ErrInvalidSnapshot  = errors.New("snapshot fiscal invalido")
	ErrInvalidAttempt   = errors.New("tentativa de emissao invalida")
	ErrAttemptNotFound  = errors.New("tentativa de emissao nao encontrada")
	ErrTenantMismatch   = errors.New("tenant da entidade diverge do contexto")
)

type Document struct {
	ID, TenantID, CompanyID, EstablishmentID, PartyID uuid.UUID
	Competence                                        time.Time
	Kind, Series, Status, ContingencyMode             string
	Number                                            *int64
	TotalCents                                        int64
	IssuedAt                                          *time.Time
	XMLObjectRef, XMLHash                             *string
}

type Item struct {
	ID, TenantID, DocumentID, CatalogItemID uuid.UUID
	Competence                              time.Time
	Description                             string
	Quantity                                string
	UnitPriceCents, TotalCents              int64
}

type TaxCalculation struct {
	ID, TenantID, ItemID   uuid.UUID
	Competence             time.Time
	TaxType                string
	BaseCents, AmountCents int64
	Rate                   *string
	RuleID                 *uuid.UUID
	RuleSourceHash         *string
	CalculationInput       json.RawMessage
	// CalculationHash e o SHA-256 do snapshot canonico calculado pelo motor.
	// Fica em coluna propria porque jsonb nao preserva a forma textual exata.
	CalculationHash *string
}

type EmissionAttempt struct {
	ID, TenantID, DocumentID uuid.UUID
	Competence               time.Time
	Attempt                  int
	IdempotencyKey           string
	Mode, Result             string
	Protocol                 *string
	ResponseBody             *string
	ResponseHash             *string
	ResponseContentType      *string
	HTTPStatus               *int
	Environment              *string
	AccessKey                *string
	DPSID                    *string
	RequestHash              *string
	AttemptedAt              time.Time
}

// Colunas de transporte da migration 0008. São lidas em toda consulta de
// tentativa para que replay e histórico preservem a trilha completa.
const emissionAttemptColumns = `id,tenant_id,document_id,competence,attempt,idempotency_key,mode,result,protocol,response_body,response_hash,response_content_type,http_status,ambiente,chave_acesso,dps_id,request_hash,attempted_at`

type Repository struct{ database *db.DB }

func NewDocumentRepository(database *db.DB) (*Repository, error) {
	if database == nil {
		return nil, errors.New("banco fiscal nulo")
	}
	return &Repository{database: database}, nil
}

// NewRepository mantém a convenção dos demais módulos.
func NewRepository(database *db.DB) (*Repository, error) { return NewDocumentRepository(database) }

func (r *Repository) GetDocument(ctx context.Context, id uuid.UUID, competence time.Time) (Document, error) {
	var result Document
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return result, err
	}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return scanDocument(tx.QueryRow(ctx, `
SELECT id,tenant_id,company_id,establishment_id,competence,kind,COALESCE(series,''),number,status,contingency_mode,
 COALESCE(party_id, '00000000-0000-0000-0000-000000000000'::uuid),(total*100)::bigint,issued_at,xml_object_ref,xml_hash
FROM fiscal.fiscal_document WHERE tenant_id=$1 AND id=$2 AND competence=$3`, tenant, id, competence.UTC()), &result)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrDocumentNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("consultar documento fiscal: %w", err)
	}
	return result, nil
}

func (r *Repository) GetEmissionAttempt(ctx context.Context, idempotencyKey string) (EmissionAttempt, error) {
	var result EmissionAttempt
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return result, err
	}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return scanAttempt(tx.QueryRow(ctx, `SELECT `+emissionAttemptColumns+` FROM fiscal.emission_attempt WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, idempotencyKey), &result)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EmissionAttempt{}, ErrAttemptNotFound
	}
	if err != nil {
		return EmissionAttempt{}, fmt.Errorf("consultar tentativa de emissao: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateDocument(ctx context.Context, document Document) (Document, error) {
	var result Document
	tenant, err := tenantFor(ctx, document.TenantID)
	if err != nil {
		return result, err
	}
	if err := validateDocument(document); err != nil {
		return result, err
	}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return scanDocument(tx.QueryRow(ctx, `
INSERT INTO fiscal.fiscal_document
	(tenant_id, company_id, establishment_id, competence, kind, series, number, status,
	 contingency_mode, party_id, total, issued_at, xml_object_ref, xml_hash)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::numeric/100,$12,$13,$14)
RETURNING id, tenant_id, company_id, establishment_id, competence, kind, COALESCE(series, ''), number,
 status, contingency_mode, COALESCE(party_id, '00000000-0000-0000-0000-000000000000'::uuid), (total * 100)::bigint, issued_at, xml_object_ref, xml_hash`,
			tenant, document.CompanyID, document.EstablishmentID, document.Competence.UTC(), document.Kind,
			nullString(document.Series), document.Number, defaultString(document.Status, "draft"),
			defaultString(document.ContingencyMode, "none"), nullUUID(document.PartyID), document.TotalCents,
			document.IssuedAt, nullStringPtr(document.XMLObjectRef), nullStringPtr(document.XMLHash)), &result)
	})
	if err != nil {
		return Document{}, fmt.Errorf("criar documento fiscal: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateItem(ctx context.Context, item Item) (Item, error) {
	var result Item
	tenant, err := tenantFor(ctx, item.TenantID)
	if err != nil {
		return result, err
	}
	if err := validateItem(item); err != nil {
		return result, err
	}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return scanItem(tx.QueryRow(ctx, `
INSERT INTO fiscal.fiscal_item
	(tenant_id, document_id, competence, item_id, description, quantity, unit_price, total)
VALUES ($1,$2,$3,$4,$5,$6::numeric,$7::numeric/100,$8::numeric/100)
RETURNING id, tenant_id, document_id, competence, COALESCE(item_id, '00000000-0000-0000-0000-000000000000'::uuid), description, quantity::text,
 (unit_price*100)::bigint, (total*100)::bigint`, tenant, item.DocumentID, item.Competence.UTC(),
			nullUUID(item.CatalogItemID), item.Description, item.Quantity, item.UnitPriceCents,
			item.TotalCents), &result)
	})
	if err != nil {
		return Item{}, fmt.Errorf("criar item fiscal: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateTaxCalculation(ctx context.Context, calc TaxCalculation) (TaxCalculation, error) {
	var result TaxCalculation
	tenant, err := tenantFor(ctx, calc.TenantID)
	if err != nil {
		return result, err
	}
	if err := validateCalculation(calc); err != nil {
		return result, err
	}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return scanCalculation(tx.QueryRow(ctx, `
INSERT INTO fiscal.tax_calc
	(tenant_id,item_id,competence,tax_type,base,rate,amount,rule_id,rule_source_hash,calculation_input,calculation_hash)
VALUES ($1,$2,$3,$4,$5::numeric/100,$6::numeric,$7::numeric/100,$8,$9,$10::jsonb,$11)
RETURNING id,tenant_id,item_id,competence,tax_type,(base*100)::bigint,rate::text,
 (amount*100)::bigint,rule_id,rule_source_hash,calculation_input,calculation_hash`, tenant, calc.ItemID,
			calc.Competence.UTC(), calc.TaxType, calc.BaseCents, nullStringPtr(calc.Rate),
			calc.AmountCents, nullUUIDPtr(calc.RuleID), nullStringPtr(calc.RuleSourceHash), []byte(calc.CalculationInput), nullStringPtr(calc.CalculationHash)), &result)
	})
	if err != nil {
		return TaxCalculation{}, fmt.Errorf("criar snapshot tax_calc: %w", err)
	}
	return result, nil
}

// RegisterEmissionAttempt is idempotent by tenant and key. A replay returns the
// original row and never mutates an append-only emission history.
func (r *Repository) RegisterEmissionAttempt(ctx context.Context, attempt EmissionAttempt) (EmissionAttempt, error) {
	var result EmissionAttempt
	tenant, err := tenantFor(ctx, attempt.TenantID)
	if err != nil {
		return result, err
	}
	if err := validateAttempt(attempt); err != nil {
		return result, err
	}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
INSERT INTO fiscal.emission_attempt
	(tenant_id,document_id,competence,attempt,idempotency_key,mode,result,protocol,response_body,response_hash,response_content_type,http_status,ambiente,chave_acesso,dps_id,request_hash,attempted_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,COALESCE($17,now()))
ON CONFLICT (tenant_id,idempotency_key) DO NOTHING
RETURNING `+emissionAttemptColumns,
			tenant, attempt.DocumentID, attempt.Competence.UTC(), attempt.Attempt, attempt.IdempotencyKey,
			attempt.Mode, defaultString(attempt.Result, "pending"), nullStringPtr(attempt.Protocol),
			nullStringPtr(attempt.ResponseBody), nullStringPtr(attempt.ResponseHash), nullStringPtr(attempt.ResponseContentType),
			intPtr(attempt.HTTPStatus), nullStringPtr(attempt.Environment), nullStringPtr(attempt.AccessKey),
			nullStringPtr(attempt.DPSID), nullStringPtr(attempt.RequestHash), timePtr(attempt.AttemptedAt))
		if err := scanAttempt(row, &result); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return scanAttempt(tx.QueryRow(ctx, `SELECT `+emissionAttemptColumns+` FROM fiscal.emission_attempt WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, attempt.IdempotencyKey), &result)
	})
	if err != nil {
		return EmissionAttempt{}, fmt.Errorf("registrar tentativa de emissao: %w", err)
	}
	return result, nil
}

func (r *Repository) ListEmissionAttempts(ctx context.Context, documentID uuid.UUID, competence time.Time) ([]EmissionAttempt, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var result []EmissionAttempt
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+emissionAttemptColumns+` FROM fiscal.emission_attempt WHERE tenant_id=$1 AND document_id=$2 AND competence=$3 ORDER BY attempt`, tenant, documentID, competence.UTC())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item EmissionAttempt
			if err := scanAttempt(rows, &item); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("consultar historico de emissao: %w", err)
	}
	return result, nil
}

type rowScanner interface{ Scan(...any) error }

func scanDocument(row rowScanner, d *Document) error {
	return row.Scan(&d.ID, &d.TenantID, &d.CompanyID, &d.EstablishmentID, &d.Competence, &d.Kind, &d.Series, &d.Number, &d.Status, &d.ContingencyMode, &d.PartyID, &d.TotalCents, &d.IssuedAt, &d.XMLObjectRef, &d.XMLHash)
}
func scanItem(row rowScanner, i *Item) error {
	return row.Scan(&i.ID, &i.TenantID, &i.DocumentID, &i.Competence, &i.CatalogItemID, &i.Description, &i.Quantity, &i.UnitPriceCents, &i.TotalCents)
}
func scanCalculation(row rowScanner, c *TaxCalculation) error {
	return row.Scan(&c.ID, &c.TenantID, &c.ItemID, &c.Competence, &c.TaxType, &c.BaseCents, &c.Rate, &c.AmountCents, &c.RuleID, &c.RuleSourceHash, &c.CalculationInput, &c.CalculationHash)
}
func scanAttempt(row rowScanner, a *EmissionAttempt) error {
	return row.Scan(&a.ID, &a.TenantID, &a.DocumentID, &a.Competence, &a.Attempt, &a.IdempotencyKey, &a.Mode, &a.Result, &a.Protocol, &a.ResponseBody, &a.ResponseHash, &a.ResponseContentType, &a.HTTPStatus, &a.Environment, &a.AccessKey, &a.DPSID, &a.RequestHash, &a.AttemptedAt)
}

func intPtr(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func tenantFor(ctx context.Context, entity uuid.UUID) (uuid.UUID, error) {
	t, err := db.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if entity != uuid.Nil && entity != t {
		return uuid.Nil, ErrTenantMismatch
	}
	return t, nil
}
func validateDocument(d Document) error {
	if d.Competence.IsZero() || d.CompanyID == uuid.Nil || d.EstablishmentID == uuid.Nil || strings.TrimSpace(d.Kind) == "" || d.TotalCents < 0 || d.TotalCents > math.MaxInt64/100 {
		return ErrInvalidDocument
	}
	return nil
}
func validateItem(i Item) error {
	if i.DocumentID == uuid.Nil || i.Competence.IsZero() || strings.TrimSpace(i.Description) == "" || i.UnitPriceCents < 0 || i.UnitPriceCents > math.MaxInt64/100 || i.TotalCents < 0 || i.TotalCents > math.MaxInt64/100 || strings.TrimSpace(i.Quantity) == "" {
		return ErrInvalidDocument
	}
	if !validDecimal(i.Quantity, 6) {
		return ErrInvalidDocument
	}
	return nil
}

func validDecimal(value string, scale int) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return false
	}
	for _, r := range parts[0] {
		if r < '0' || r > '9' {
			return false
		}
	}
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > scale {
			return false
		}
		for _, r := range parts[1] {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return parts[0] != "0" || len(parts) == 2 && strings.Trim(parts[1], "0") != ""
}
func validateCalculation(c TaxCalculation) error {
	if c.ItemID == uuid.Nil || c.Competence.IsZero() || strings.TrimSpace(c.TaxType) == "" || c.BaseCents < 0 || c.AmountCents < 0 || !json.Valid(c.CalculationInput) {
		return ErrInvalidSnapshot
	}
	if c.CalculationHash != nil && !fingerprintHex.MatchString(*c.CalculationHash) {
		return ErrInvalidSnapshot
	}
	return nil
}
func validateAttempt(a EmissionAttempt) error {
	if a.DocumentID == uuid.Nil || a.Competence.IsZero() || a.Attempt <= 0 || strings.TrimSpace(a.IdempotencyKey) == "" || strings.TrimSpace(a.Mode) == "" {
		return ErrInvalidAttempt
	}
	return nil
}
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func nullUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}
func nullUUIDPtr(value *uuid.UUID) any {
	if value == nil || *value == uuid.Nil {
		return nil
	}
	return *value
}
func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
func nullStringPtr(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return *value
}
func timePtr(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
