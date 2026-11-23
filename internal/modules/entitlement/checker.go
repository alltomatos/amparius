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

// Checker consulta exclusivamente o PostgreSQL do control plane.
// companyID deve vir do contexto autenticado/caso de uso; nunca do corpo HTTP.
type Checker struct {
	database *db.DB
}

func NewChecker(database *db.DB) (*Checker, error) {
	if database == nil {
		return nil, errors.New("banco do control plane nulo")
	}
	return &Checker{database: database}, nil
}

// Check permite moduleCode para a empresa no instante at.
// A ausência de tenant no contexto e qualquer falha de banco bloqueiam a operação.
func (c *Checker) Check(ctx context.Context, moduleCode string, companyID uuid.UUID, at time.Time) error {
	if moduleCode == "" || companyID == uuid.Nil || at.IsZero() {
		return fmt.Errorf("%w: parâmetros de enforcement inválidos", ErrModuleDisabled)
	}
	if _, err := db.TenantFromContext(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}

	var result error
	err := c.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
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
