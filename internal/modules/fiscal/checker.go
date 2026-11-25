// Package fiscal consulta regras tributarias oficiais por competencia.
package fiscal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrTaxRuleNotFound    = errors.New("regra fiscal nao encontrada para a competencia")
	ErrTaxRuleUnavailable = errors.New("consulta de regra fiscal indisponivel")
	ErrTaxRuleAmbiguous   = errors.New("mais de uma regra fiscal compativel")
)

// TaxRuleCriteria e montado pelo caso de uso a partir da sessao autenticada.
// Company/establishment e municipio nao devem ser aceitos diretamente de um
// corpo HTTP confiado; o municipio informado aqui deve vir dessa sessao/caso.
type TaxRuleCriteria struct {
	TaxType, Regime, ScopeKind              string
	NCM, CEST, CST, ClassTrib               string
	CFOP, ServiceCode, MunicipalityCode, UF string
	OperationKind, SimplesAnnex             string
	RBT12From, RBT12To                      *string
	BasisKind, RateUnit                     string
	// RBT12ActualCents, quando definido, restringe as regras de faixa do
	// Simples aquela cuja faixa [rbt12_from, rbt12_to) contem a receita bruta
	// dos ultimos 12 meses, em centavos. Regras sem faixa continuam elegiveis.
	RBT12ActualCents *int64
}

type TaxRule struct {
	ID                                               uuid.UUID
	TenantID                                         *uuid.UUID
	TaxType, Regime, ScopeKind                       string
	NCM, CEST, CST, ClassTrib                        string
	CFOP, ServiceCode, MunicipalityCode, UF          string
	OperationKind, SimplesAnnex                      string
	RBT12From, RBT12To                               *string
	BasisKind                                        string
	Rate                                             *string
	RateUnit                                         string
	DeductionAmount, BaseReductionPercent            *string
	ValidFrom                                        time.Time
	ValidTo                                          *time.Time
	SourceKind, SourceName, SourceURL, SourceVersion string
	SourcePublishedAt                                time.Time
	SourceHash                                       string
	ImportBatch                                      uuid.UUID
	Version                                          int
	Notes                                            *string
}

type Checker struct{ database *db.DB }

func NewChecker(database *db.DB) (*Checker, error) {
	if database == nil {
		return nil, errors.New("banco fiscal nulo")
	}
	return &Checker{database: database}, nil
}

func (c *Checker) LookupTaxRule(ctx context.Context, criteria TaxRuleCriteria, competence time.Time) (TaxRule, error) {
	var rule TaxRule
	if competence.IsZero() {
		return rule, fmt.Errorf("%w: competencia invalida", ErrTaxRuleNotFound)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return rule, fmt.Errorf("%w: %v", ErrTaxRuleUnavailable, err)
	}
	var queryErr error
	err = c.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		const query = `
WITH compatible AS (
		   SELECT id, tenant_id, tax_type, regime, scope_kind, COALESCE(ncm, '') AS ncm, COALESCE(cest, '') AS cest, COALESCE(cst, '') AS cst, COALESCE(class_trib, '') AS class_trib,
		           COALESCE(cfop, '') AS cfop, COALESCE(service_code, '') AS service_code, COALESCE(municipality_code, '') AS municipality_code, COALESCE(uf, '') AS uf, COALESCE(operation_kind, '') AS operation_kind, COALESCE(simples_annex, '') AS simples_annex,
		           rbt12_from::text, rbt12_to::text, basis_kind, rate::text, COALESCE(rate_unit, '') AS rate_unit,
           deduction_amount::text, base_reduction_percent::text, valid_from, valid_to,
           source_kind, source_name, source_url, source_version, source_published_at,
           source_hash, import_batch, version, notes
      FROM fiscal.tax_rule
     WHERE valid_from <= $1::date
       AND (valid_to IS NULL OR $1::date < valid_to)
       AND ($2 = '' OR tax_type = $2) AND ($3 = '' OR regime = $3)
       AND ($4 = '' OR scope_kind = $4) AND ($5 = '' OR ncm = $5)
       AND ($6 = '' OR cest = $6) AND ($7 = '' OR cst = $7)
       AND ($8 = '' OR class_trib = $8) AND ($9 = '' OR cfop = $9)
       AND ($10 = '' OR service_code = $10) AND ($11 = '' OR municipality_code = $11)
		       AND ($12 = '' OR uf = $12) AND ($13 = '' OR operation_kind = $13)
		       AND ($14 = '' OR simples_annex = $14)
       AND ($15 = '' OR COALESCE(rbt12_from::text, '') = $15)
       AND ($16 = '' OR COALESCE(rbt12_to::text, '') = $16)
       AND ($17 = '' OR basis_kind = $17) AND ($18 = '' OR rate_unit = $18)
       AND ($19::numeric IS NULL OR (
           (rbt12_from IS NULL OR rbt12_from * 100 <= $19::numeric)
           AND (rbt12_to IS NULL OR $19::numeric < rbt12_to * 100)
       ))
), selected AS (
    SELECT * FROM compatible WHERE tenant_id = current_setting('app.tenant_id')::uuid
    UNION ALL
    SELECT * FROM compatible
     WHERE tenant_id IS NULL
       AND NOT EXISTS (SELECT 1 FROM compatible WHERE tenant_id = current_setting('app.tenant_id')::uuid)
)
		       SELECT id, tenant_id, tax_type, regime, scope_kind, ncm, cest, cst, class_trib,
		       cfop, service_code, municipality_code, uf, operation_kind, simples_annex,
       rbt12_from, rbt12_to, basis_kind, rate, rate_unit, deduction_amount,
       base_reduction_percent, valid_from, valid_to, source_kind, source_name,
       source_url, source_version, source_published_at, source_hash, import_batch,
       version, notes
  FROM selected
 ORDER BY id
 LIMIT 2`
		args := []any{competence.UTC().Format("2006-01-02"), criteria.TaxType, criteria.Regime, criteria.ScopeKind,
			criteria.NCM, criteria.CEST, criteria.CST, criteria.ClassTrib, criteria.CFOP, criteria.ServiceCode,
			criteria.MunicipalityCode, criteria.UF, criteria.OperationKind, criteria.SimplesAnnex,
			stringValue(criteria.RBT12From), stringValue(criteria.RBT12To), criteria.BasisKind, criteria.RateUnit,
			int64Value(criteria.RBT12ActualCents)}
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("consultar fiscal.tax_rule: %w", err)
		}
		defer rows.Close()
		var candidates []TaxRule
		for rows.Next() {
			var item TaxRule
			if err := rows.Scan(&item.ID, &item.TenantID, &item.TaxType, &item.Regime, &item.ScopeKind,
				&item.NCM, &item.CEST, &item.CST, &item.ClassTrib, &item.CFOP, &item.ServiceCode,
				&item.MunicipalityCode, &item.UF, &item.OperationKind, &item.SimplesAnnex,
				&item.RBT12From, &item.RBT12To, &item.BasisKind, &item.Rate, &item.RateUnit,
				&item.DeductionAmount, &item.BaseReductionPercent, &item.ValidFrom, &item.ValidTo,
				&item.SourceKind, &item.SourceName, &item.SourceURL, &item.SourceVersion,
				&item.SourcePublishedAt, &item.SourceHash, &item.ImportBatch, &item.Version, &item.Notes); err != nil {
				return fmt.Errorf("ler regra fiscal: %w", err)
			}
			candidates = append(candidates, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterar regras fiscais: %w", err)
		}
		var pickErr error
		rule, pickErr = chooseRule(candidates, tenantID)
		return pickErr
	})
	if err != nil {
		queryErr = err
		if errors.Is(err, ErrTaxRuleNotFound) || errors.Is(err, ErrTaxRuleAmbiguous) {
			return rule, err
		}
		return rule, fmt.Errorf("%w: %v", ErrTaxRuleUnavailable, queryErr)
	}
	return rule, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int64Value(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func chooseRule(candidates []TaxRule, tenantID uuid.UUID) (TaxRule, error) {
	if len(candidates) == 0 {
		return TaxRule{}, ErrTaxRuleNotFound
	}
	var tenantRules, globalRules []TaxRule
	for _, candidate := range candidates {
		if candidate.TenantID != nil && *candidate.TenantID == tenantID {
			tenantRules = append(tenantRules, candidate)
		} else if candidate.TenantID == nil {
			globalRules = append(globalRules, candidate)
		}
	}
	if len(tenantRules) == 1 {
		return tenantRules[0], nil
	}
	if len(tenantRules) > 1 || len(globalRules) > 1 {
		return TaxRule{}, ErrTaxRuleAmbiguous
	}
	if len(globalRules) == 1 {
		return globalRules[0], nil
	}
	return TaxRule{}, ErrTaxRuleNotFound
}

func effectiveAt(from time.Time, to *time.Time, competence time.Time) bool {
	return !competence.Before(from) && (to == nil || competence.Before(*to))
}
