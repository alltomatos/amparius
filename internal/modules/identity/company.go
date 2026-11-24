package identity

import (
	"context"
	"fmt"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CompanyBelongsToTenant confirma, sob RLS do tenant, que a empresa existe,
// não está excluída e pertence ao tenant informado.
//
// A assinatura casa com o contrato de validação esperado pelo enforcement de
// entitlement, sem que o módulo de entitlement dependa deste pacote:
//   - (false, nil) nega a empresa (inexistente, excluída ou de outro tenant);
//   - (_, err) é falha de leitura e deve bloquear (fail-closed) quem chama.
//
// O tenant também precisa estar no contexto (db.WithTenant) e igual ao
// argumento; a divergência nega em vez de vazar a existência da empresa.
func (r *Repository) CompanyBelongsToTenant(ctx context.Context, tenantID, companyID uuid.UUID) (bool, error) {
	if tenantID == uuid.Nil || companyID == uuid.Nil {
		return false, nil
	}
	ctxTenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return false, err
	}
	if ctxTenant != tenantID {
		return false, nil
	}

	var exists bool
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM identity.company
				 WHERE tenant_id = $1
				   AND id = $2
				   AND deleted_at IS NULL
			)`, tenantID, companyID).Scan(&exists)
	})
	if err != nil {
		return false, fmt.Errorf("consultar empresa do tenant: %w", err)
	}
	return exists, nil
}
