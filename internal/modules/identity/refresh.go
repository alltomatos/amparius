package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrRefreshTokenNotFound cobre hash inexistente (token forjado ou já
// apagado por retenção, nunca ocorre nesta fatia sem purga).
var ErrRefreshTokenNotFound = errors.New("refresh token não encontrado")

// ErrRefreshTokenAlreadyRotated protege a corrida entre duas rotações
// concorrentes do mesmo token: só uma vence o UPDATE condicional.
var ErrRefreshTokenAlreadyRotated = errors.New("refresh token já rotacionado")

// RefreshRecord é uma linha de identity.resolve_refresh_token (ADR 0048 §7).
// A decisão entre REFRESH_INVALID e REFRESH_REUSE_DETECTED cabe a quem chama,
// a partir de RotatedAt/RevokedAt/ExpiresAt.
type RefreshRecord struct {
	TenantID  uuid.UUID
	ID        uuid.UUID
	UserID    uuid.UUID
	CompanyID uuid.UUID
	FamilyID  uuid.UUID
	ExpiresAt time.Time
	RotatedAt *time.Time
	RevokedAt *time.Time
}

// Expired informa se o token já passou da validade absoluta.
func (rec RefreshRecord) Expired() bool {
	return !rec.ExpiresAt.After(time.Now().UTC())
}

// Reused informa se o token já havia sido rotacionado (replay de um token
// substituído) — sinal de reuso que deve revogar a família inteira.
func (rec RefreshRecord) Reused() bool {
	return rec.RotatedAt != nil
}

// Revoked informa se a família já foi revogada (logout, reuso detectado ou
// revoke explícito).
func (rec RefreshRecord) Revoked() bool {
	return rec.RevokedAt != nil
}

// ResolveRefreshToken localiza o refresh token pelo hash, sem tenant
// conhecido ainda (mesmo padrão de resolve_login_candidates).
func (r *Repository) ResolveRefreshToken(ctx context.Context, rawToken string) (RefreshRecord, error) {
	hash := hashOpaqueToken(rawToken)
	var rec RefreshRecord
	err := r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT tenant_id, id, user_id, company_id, family_id, expires_at, rotated_at, revoked_at
			  FROM identity.resolve_refresh_token($1)`, hash).
			Scan(&rec.TenantID, &rec.ID, &rec.UserID, &rec.CompanyID, &rec.FamilyID, &rec.ExpiresAt, &rec.RotatedAt, &rec.RevokedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshRecord{}, ErrRefreshTokenNotFound
	}
	if err != nil {
		return RefreshRecord{}, fmt.Errorf("resolver refresh token: %w", err)
	}
	return rec, nil
}

// IssueRefreshToken cria o primeiro token de uma família nova (login,
// bootstrap/complete, recovery/complete). O valor cru só existe aqui.
// companyID é gravado para que um refresh sem companyId possa manter a
// mesma empresa da sessão (contrato auth-login-http-v1).
func (r *Repository) IssueRefreshToken(ctx context.Context, userID, companyID, familyID uuid.UUID, ttl time.Duration) (string, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return "", err
	}
	raw, hash, err := generateOpaqueToken()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(ttl)
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO identity.refresh_token (tenant_id, user_id, company_id, family_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			tenantID, userID, companyID, familyID, hash, expiresAt)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("emitir refresh token: %w", err)
	}
	return raw, nil
}

// RotateRefreshToken marca old como rotacionado e emite o próximo token da
// mesma família. O UPDATE condicional (rotated_at IS NULL AND revoked_at IS
// NULL) decide atomicamente quem vence uma corrida entre duas rotações
// concorrentes do mesmo token, sem depender de SELECT ... FOR UPDATE em
// outra transação. companyID é a empresa final da nova sessão: old.CompanyID
// quando o cliente não pediu troca, ou a empresa já validada pelo chamador.
func (r *Repository) RotateRefreshToken(ctx context.Context, old RefreshRecord, companyID uuid.UUID, ttl time.Duration) (string, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return "", err
	}
	if tenantID != old.TenantID {
		return "", ErrRefreshTokenNotFound
	}
	raw, hash, err := generateOpaqueToken()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(ttl)
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE identity.refresh_token
			   SET rotated_at = now()
			 WHERE tenant_id = $1 AND id = $2 AND rotated_at IS NULL AND revoked_at IS NULL`,
			tenantID, old.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrRefreshTokenAlreadyRotated
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO identity.refresh_token (tenant_id, user_id, company_id, family_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			tenantID, old.UserID, companyID, old.FamilyID, hash, expiresAt)
		return err
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// RevokeRefreshFamily revoga todos os tokens de uma família (reuso detectado
// ou logout da sessão). ctx precisa carregar o tenant do token resolvido.
func (r *Repository) RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID, reason string) error {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	return r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE identity.refresh_token
			   SET revoked_at = now(), revoked_reason = $3
			 WHERE tenant_id = $1 AND family_id = $2 AND revoked_at IS NULL`,
			tenantID, familyID, reason)
		return err
	})
}

// RevokeAllRefreshTokens revoga todas as famílias do usuário (POST
// /v1/auth/revoke, após reautenticação por senha).
func (r *Repository) RevokeAllRefreshTokens(ctx context.Context, userID uuid.UUID, reason string) error {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	return r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE identity.refresh_token
			   SET revoked_at = now(), revoked_reason = $3
			 WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
			tenantID, userID, reason)
		return err
	})
}
