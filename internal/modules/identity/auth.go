package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrAuthenticatedSubjectNotFound evita distinguir, na borda HTTP, subject
// inexistente de vínculo desativado ou pertencente a outro tenant.
var ErrAuthenticatedSubjectNotFound = errors.New("subject autenticado não encontrado")

// ResolveSubject resolve a autorização persistida para um subject OIDC. A
// consulta exige o tenant no contexto e usa RLS; email e nome não são lidos e
// portanto não entram no caminho de autenticação nem em logs.
func (r *Repository) ResolveSubject(ctx context.Context, tenantID uuid.UUID, subject string) (platformauth.SubjectRecord, error) {
	if tenantID == uuid.Nil || strings.TrimSpace(subject) == "" || len(subject) > 255 {
		return platformauth.SubjectRecord{}, ErrAuthenticatedSubjectNotFound
	}
	ctxTenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return platformauth.SubjectRecord{}, err
	}
	if ctxTenant != tenantID {
		return platformauth.SubjectRecord{}, ErrAuthenticatedSubjectNotFound
	}

	var record platformauth.SubjectRecord
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT u.id, u.mfa_enabled,
			       COALESCE(array_agg(DISTINCT r.code ORDER BY r.code) FILTER (WHERE r.code IS NOT NULL), ARRAY[]::text[]),
			       COALESCE(array_agg(DISTINCT uc.company_id ORDER BY uc.company_id) FILTER (WHERE uc.company_id IS NOT NULL), ARRAY[]::uuid[])
			  FROM identity.app_user AS u
			  LEFT JOIN identity.user_role AS ur
			    ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id
			  LEFT JOIN identity.role AS r
			    ON r.tenant_id = ur.tenant_id AND r.id = ur.role_id
			  LEFT JOIN identity.user_company AS uc
			    ON uc.tenant_id = u.tenant_id AND uc.user_id = u.id
			 WHERE u.tenant_id = $1
			   AND u.auth_subject = $2
			   AND u.status = 'active'
		 GROUP BY u.id, u.mfa_enabled`, tenantID, subject).Scan(&record.UserID, &record.MFAEnabled, &record.Roles, &record.CompanyIDs)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return platformauth.SubjectRecord{}, ErrAuthenticatedSubjectNotFound
	}
	if err != nil {
		return platformauth.SubjectRecord{}, fmt.Errorf("consultar subject OIDC: %w", err)
	}
	return record, nil
}
