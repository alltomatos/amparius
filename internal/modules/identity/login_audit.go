package identity

import (
	"context"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LoginAuditResult enumera os valores aceitos por identity.login_audit.result
// (migration 0041). Nunca inclui email, senha, token ou IP (ADR 0048 §11).
type LoginAuditResult string

const (
	LoginAuditSuccess            LoginAuditResult = "success"
	LoginAuditInvalidCredentials LoginAuditResult = "invalid_credentials"
	LoginAuditLocked             LoginAuditResult = "locked"
	LoginAuditPendingAuth        LoginAuditResult = "pending_auth"
	LoginAuditRefreshReuseDetect LoginAuditResult = "refresh_reuse_detected"
	LoginAuditLogout             LoginAuditResult = "logout"
	LoginAuditRevoke             LoginAuditResult = "revoke"
)

// WriteLoginAudit grava uma linha de auditoria sem PII. ctx precisa carregar
// o tenant resolvido; só é chamada quando exatamente um tenant foi resolvido
// para a tentativa (ADR 0048 §5/§11).
func (r *Repository) WriteLoginAudit(ctx context.Context, userID uuid.UUID, result LoginAuditResult) error {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	return r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO identity.login_audit (tenant_id, user_id, result)
			VALUES ($1, $2, $3)`,
			tenantID, userID, string(result))
		return err
	})
}
