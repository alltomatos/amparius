// Package entitlement verifica o acesso a módulos no control plane.
package entitlement

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	ModuleIdentity     = "identity"
	ModuleParty        = "party"
	ModuleCatalog      = "catalog"
	ModuleFinance      = "finance"
	ModuleFiscal       = "fiscal"
	ModuleSync         = "sync"
	ModuleReporting    = "reporting"
	ModuleFuneral      = "funeral"
	ModuleCemetery     = "cemetery"
	ModuleCremation    = "cremation"
	ModuleFloriculture = "floriculture"
	ModuleInventory    = "inventory"
	ModuleDocuments    = "documents"
	ModuleAI           = "ai"
)

const (
	moduleKindCore     = "core"
	moduleKindOptional = "optional"
)

var (
	ErrModuleDisabled         = errors.New("módulo desabilitado ou sem entitlement vigente")
	ErrEntitlementUnavailable = errors.New("entitlement indisponível")
)

// CompanyResolver confirma que a empresa pertence ao tenant autenticado.
//
// É implementado pelo módulo identity contra o banco da célula (identity.company
// sob RLS). O entitlement não importa identity: recebe apenas este contrato por
// injeção, de modo que a fronteira entre módulos seja preservada.
type CompanyResolver interface {
	CompanyBelongsToTenant(ctx context.Context, tenantID, companyID uuid.UUID) (bool, error)
}

// Option ajusta a construção do Checker e do Service.
type options struct {
	companyResolver CompanyResolver
}

type Option func(*options)

func newOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithCompanyResolver liga a verificação de que a empresa do contexto pertence
// ao tenant. Sem ele, o Checker confia apenas no contexto autenticado; com ele,
// uma empresa forjada no contexto falha mesmo que exista licença para o
// company_id informado.
func WithCompanyResolver(resolver CompanyResolver) Option {
	return func(o *options) { o.companyResolver = resolver }
}

// Checker consulta exclusivamente o PostgreSQL do control plane.
// O tenant e a empresa vêm do contexto autenticado; nunca do corpo HTTP.
type Checker struct {
	database        *db.DB
	companyResolver CompanyResolver
}

func NewChecker(database *db.DB, opts ...Option) (*Checker, error) {
	if database == nil {
		return nil, errors.New("banco do control plane nulo")
	}
	o := newOptions(opts)
	checker := &Checker{database: database, companyResolver: o.companyResolver}
	return checker, nil
}

// Check permite moduleCode para a empresa e o tenant fixados no contexto no
// instante at. A ausência de tenant ou empresa no contexto bloqueia.
func (c *Checker) Check(ctx context.Context, moduleCode string, at time.Time) error {
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	return c.CheckCompany(ctx, moduleCode, companyID, at)
}

// CheckCompany é o caminho interno para quem já resolveu a empresa de forma
// confiável (por exemplo, um worker que processa a empresa de um documento).
// O tenant continua vindo do contexto. Nunca passe companyID vindo do corpo.
func (c *Checker) CheckCompany(ctx context.Context, moduleCode string, companyID uuid.UUID, at time.Time) error {
	if moduleCode == "" || companyID == uuid.Nil || at.IsZero() {
		return fmt.Errorf("%w: parâmetros de enforcement inválidos", ErrModuleDisabled)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	if err := c.validateCompany(ctx, tenantID, companyID); err != nil {
		return err
	}

	var result error
	err = c.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var kind string
		var active bool
		err := tx.QueryRow(ctx, `
			SELECT kind, active
			FROM entitlement.module
			WHERE code = $1`, moduleCode).Scan(&kind, &active)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
			result = fmt.Errorf("%w: módulo %q inexistente ou inativo", ErrModuleDisabled, moduleCode)
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: consultar catálogo de módulos: %v", ErrEntitlementUnavailable, err)
		}
		if kind == moduleKindCore {
			result = nil
			return nil
		}
		if kind != moduleKindOptional {
			result = fmt.Errorf("%w: tipo de módulo desconhecido", ErrModuleDisabled)
			return nil
		}

		var assigned bool
		date := at.UTC().Format("2006-01-02")
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM entitlement.license_assignment
				WHERE tenant_id = current_setting('app.tenant_id')::uuid
				  AND module_code = $1
				  AND company_id = $2
				  AND status = 'active'
				  AND valid_from <= $3::date
				  AND (valid_to IS NULL OR valid_to >= $3::date)
			)`, moduleCode, companyID, date).Scan(&assigned)
		if err != nil {
			return fmt.Errorf("%w: consultar licença do módulo: %v", ErrEntitlementUnavailable, err)
		}
		if !assigned {
			result = fmt.Errorf("%w: licença ausente, vencida ou revogada", ErrModuleDisabled)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrEntitlementUnavailable) {
			return err
		}
		return fmt.Errorf("%w: consultar entitlement: %v", ErrEntitlementUnavailable, err)
	}
	return result
}

// validateCompany aplica a defesa em profundidade: se houver resolver, a
// empresa precisa pertencer ao tenant. Falha de leitura bloqueia como
// indisponibilidade; empresa de outro tenant bloqueia como módulo negado.
func (c *Checker) validateCompany(ctx context.Context, tenantID, companyID uuid.UUID) error {
	return validateCompanyWith(ctx, c.companyResolver, tenantID, companyID)
}

func validateCompanyWith(ctx context.Context, resolver CompanyResolver, tenantID, companyID uuid.UUID) error {
	if resolver == nil {
		return nil
	}
	ok, err := resolver.CompanyBelongsToTenant(ctx, tenantID, companyID)
	if err != nil {
		return fmt.Errorf("%w: validar empresa do tenant: %v", ErrEntitlementUnavailable, err)
	}
	if !ok {
		return fmt.Errorf("%w: empresa fora do tenant autenticado", ErrModuleDisabled)
	}
	return nil
}

// decide is the pure rule used by unit tests and documents the fail-closed rule.
func decide(kind string, active, assigned bool) error {
	if !active || (kind != moduleKindCore && kind != moduleKindOptional) {
		return ErrModuleDisabled
	}
	if kind == moduleKindOptional && !assigned {
		return ErrModuleDisabled
	}
	return nil
}
