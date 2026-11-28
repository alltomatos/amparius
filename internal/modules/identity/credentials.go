package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// loginLockoutThreshold e loginLockoutWindow implementam o bloqueio de
	// 5 tentativas/15 min decidido no ADR 0048 §6. O contador autoritativo é
	// failed_attempts/locked_until em identity.app_user: nunca falha aberto.
	loginLockoutThreshold = 5
	loginLockoutWindow    = 15 * time.Minute
)

var (
	// ErrCredentialTokenNotFound cobre token inexistente ou com finalidade
	// divergente (bootstrap vs. recovery): a resposta HTTP não distingue os
	// dois casos de um token já expirado, pois tokens são imprevisíveis.
	ErrCredentialTokenNotFound = errors.New("token de credencial não encontrado")
	ErrCredentialTokenExpired  = errors.New("token de credencial expirado")
	ErrCredentialTokenConsumed = errors.New("token de credencial já utilizado")
	ErrInviteRoleUnknown       = errors.New("papel de convite desconhecido")
)

// LoginCandidate é uma linha de identity.resolve_login_candidates (ADR 0048
// §5): nunca inclui email ou name.
type LoginCandidate struct {
	TenantID       uuid.UUID
	UserID         uuid.UUID
	PasswordHash   string
	PasswordAlgo   string
	Status         string
	FailedAttempts int
	LockedUntil    *time.Time
}

// Locked informa se o candidato está bloqueado neste instante.
func (c LoginCandidate) Locked() bool {
	return c.LockedUntil != nil && c.LockedUntil.After(time.Now().UTC())
}

// ResolveLoginCandidates chama a função SECURITY DEFINER que resolve o(s)
// tenant(s) do e-mail informado, sem exigir BYPASSRLS para amparius_app.
func (r *Repository) ResolveLoginCandidates(ctx context.Context, email string) ([]LoginCandidate, error) {
	var candidates []LoginCandidate
	err := r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id, user_id, password_hash, password_algo, status,
			       failed_attempts, locked_until
			  FROM identity.resolve_login_candidates($1)`, email)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var candidate LoginCandidate
			var passwordHash, passwordAlgo *string
			if err := rows.Scan(&candidate.TenantID, &candidate.UserID, &passwordHash,
				&passwordAlgo, &candidate.Status, &candidate.FailedAttempts, &candidate.LockedUntil); err != nil {
				return err
			}
			if passwordHash != nil {
				candidate.PasswordHash = *passwordHash
			}
			if passwordAlgo != nil {
				candidate.PasswordAlgo = *passwordAlgo
			}
			candidates = append(candidates, candidate)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("resolver candidatos de login: %w", err)
	}
	return candidates, nil
}

// RecordLoginFailure incrementa failed_attempts e aplica o bloqueio de 15
// minutos ao cruzar o limiar. ctx precisa carregar o tenant do candidato
// (db.WithTenant) antes da chamada.
func (r *Repository) RecordLoginFailure(ctx context.Context, userID uuid.UUID) error {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	return r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE identity.app_user
			   SET failed_attempts = failed_attempts + 1,
			       locked_until = CASE
			           WHEN failed_attempts + 1 >= $3
			               THEN now() + make_interval(mins => $4)
			           ELSE locked_until
			       END
			 WHERE tenant_id = $1 AND id = $2`,
			tenantID, userID, loginLockoutThreshold, int(loginLockoutWindow.Minutes()))
		return err
	})
}

// PasswordRehash carrega um novo hash a persistir no login bem-sucedido
// quando VerifyPassword sinaliza needsRehash (parâmetros argon2id atualizados).
type PasswordRehash struct {
	Hash string
	Algo string
}

// RecordLoginSuccess zera o contador de bloqueio e fixa auth_subject =
// id::text (o subject usado pelo emissor de tenant é o próprio UUID do
// usuário). rehash é opcional.
func (r *Repository) RecordLoginSuccess(ctx context.Context, userID uuid.UUID, rehash *PasswordRehash) error {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	return r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if rehash != nil {
			_, err := tx.Exec(ctx, `
				UPDATE identity.app_user
				   SET failed_attempts = 0, locked_until = NULL, auth_subject = id::text,
				       password_hash = $3, password_algo = $4, password_updated_at = now()
				 WHERE tenant_id = $1 AND id = $2`,
				tenantID, userID, rehash.Hash, rehash.Algo)
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE identity.app_user
			   SET failed_attempts = 0, locked_until = NULL, auth_subject = id::text
			 WHERE tenant_id = $1 AND id = $2`,
			tenantID, userID)
		return err
	})
}

// PasswordHashForUser lê o hash atual do usuário já autenticado, usado por
// POST /v1/auth/revoke para exigir reautenticação por senha. ctx precisa
// carregar o tenant já resolvido pelo middleware de autenticação.
func (r *Repository) PasswordHashForUser(ctx context.Context, userID uuid.UUID) (hash, algo string, err error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return "", "", err
	}
	var h, a *string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT password_hash, password_algo FROM identity.app_user
			 WHERE tenant_id = $1 AND id = $2`, tenantID, userID).Scan(&h, &a)
	})
	if err != nil {
		return "", "", fmt.Errorf("ler hash de senha: %w", err)
	}
	if h != nil {
		hash = *h
	}
	if a != nil {
		algo = *a
	}
	return hash, algo, nil
}

// IssueCredentialToken cria um token de bootstrap/recuperação de uso único.
// O valor cru só existe nesta chamada: o banco guarda apenas o hash.
func (r *Repository) IssueCredentialToken(ctx context.Context, userID uuid.UUID, purpose string, ttl time.Duration) (raw string, expiresAt time.Time, err error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	raw, hash, err := generateOpaqueToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().UTC().Add(ttl)
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO identity.credential_token (tenant_id, user_id, token_hash, purpose, expires_at)
			VALUES ($1, $2, $3, $4, $5)`,
			tenantID, userID, hash, purpose, expiresAt)
		return err
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("emitir token de credencial: %w", err)
	}
	return raw, expiresAt, nil
}

// CompleteCredential consome o token (bootstrap ou recovery) e ativa a nova
// senha em uma única transação no banco (identity.complete_credential),
// eliminando a corrida entre resolver e consumir o mesmo token.
func (r *Repository) CompleteCredential(ctx context.Context, rawToken, purpose, passwordHash, passwordAlgo string) (tenantID, userID uuid.UUID, err error) {
	tokenHash := hashOpaqueToken(rawToken)
	err = r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT tenant_id, user_id FROM identity.complete_credential($1, $2, $3, $4)`,
			tokenHash, purpose, passwordHash, passwordAlgo).Scan(&tenantID, &userID)
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, classifyCredentialTokenError(err)
	}
	return tenantID, userID, nil
}

func classifyCredentialTokenError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.Contains(pgErr.Message, "nao encontrado"):
			return fmt.Errorf("%w: %s", ErrCredentialTokenNotFound, pgErr.Message)
		case strings.Contains(pgErr.Message, "finalidade divergente"):
			return fmt.Errorf("%w: %s", ErrCredentialTokenNotFound, pgErr.Message)
		case strings.Contains(pgErr.Message, "ja consumido"):
			return fmt.Errorf("%w: %s", ErrCredentialTokenConsumed, pgErr.Message)
		case strings.Contains(pgErr.Message, "expirado"):
			return fmt.Errorf("%w: %s", ErrCredentialTokenExpired, pgErr.Message)
		}
	}
	return fmt.Errorf("concluir credencial: %w", err)
}

// CreateInvite cria o usuário convidado (pending_auth) com papéis e empresas
// vinculados via identity.create_invite (SECURITY DEFINER), já que
// identity.user_role/identity.user_company só concedem SELECT a
// amparius_app. ctx precisa carregar o tenant do convidante.
func (r *Repository) CreateInvite(ctx context.Context, email, name string, roleCodes []string, companyIDs []uuid.UUID) (uuid.UUID, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	var userID uuid.UUID
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT user_id FROM identity.create_invite($1, $2, $3, $4, $5)`,
			tenantID, email, name, roleCodes, companyIDs).Scan(&userID)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && strings.Contains(pgErr.Message, "papel desconhecido") {
			return uuid.Nil, fmt.Errorf("%w: %s", ErrInviteRoleUnknown, pgErr.Message)
		}
		return uuid.Nil, fmt.Errorf("criar convite: %w", err)
	}
	return userID, nil
}
