package party

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	KindPerson     = "person"
	KindCompany    = "company"
	StatusActive   = "active"
	StatusInactive = "inactive"
)

var ErrNotFound = errors.New("party não encontrada")

// ErrTenantNotFound classifica o tenant inexistente referenciado pela party.
var ErrTenantNotFound = errors.New("tenant não encontrado")

// ErrSocialNameNotApplicable indica que a party não aceita nome social, como
// pessoa jurídica (ADR 0013).
var ErrSocialNameNotApplicable = errors.New("nome social não se aplica a esta party")

// Party mantém o nome civil em Name e o nome social opcional em SocialName.
// SocialName nil significa ausência; nunca recebe cópia automática de Name.
type Party struct {
	ID, TenantID                 uuid.UUID
	Kind, Name, Document, Status string
	SocialName                   *string
}

// CivilName devolve o nome civil persistido, sem fallback de nome social.
func (p Party) CivilName() string {
	return p.Name
}

// DisplayName é a semântica de apresentação do ADR 0013: para pessoa com nome
// social não vazio usa o nome social; caso contrário (e sempre para pessoa
// jurídica) usa o nome civil.
func (p Party) DisplayName() string {
	if p.Kind != KindPerson || p.SocialName == nil {
		return p.Name
	}
	if social := strings.TrimSpace(*p.SocialName); social != "" {
		return social
	}
	return p.Name
}

type Repository struct{ database *db.DB }

func NewRepository(database *db.DB) (*Repository, error) {
	if database == nil {
		return nil, errors.New("banco nulo")
	}
	return &Repository{database: database}, nil
}

func (r *Repository) Create(ctx context.Context, party Party) (Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Party{}, err
	}
	if party.TenantID != uuid.Nil && party.TenantID != tenantID {
		return Party{}, errors.New("tenant da party diverge do contexto")
	}
	party.TenantID = tenantID
	party.SocialName = normalizeSocialName(party.Kind, party.SocialName)
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO party.party (tenant_id, kind, name, document, status)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5)
			RETURNING id`, tenantID, party.Kind, party.Name, party.Document, party.Status).Scan(&party.ID); err != nil {
			return err
		}
		if party.Kind != KindPerson {
			return nil
		}
		_, err := tx.Exec(ctx, `INSERT INTO party.person (tenant_id, party_id, social_name)
			VALUES ($1, $2, $3)`, tenantID, party.ID, party.SocialName)
		return err
	})
	if err != nil {
		if isTenantForeignKeyViolation(err) {
			return Party{}, fmt.Errorf("%w: %w", ErrTenantNotFound, err)
		}
		return Party{}, fmt.Errorf("criar party: %w", err)
	}
	return party, nil
}

// UpdateSocialName altera somente o nome social da pessoa no tenant do
// contexto, preservando nome civil, documento e identificadores. Nil ou valor
// vazio limpa para NULL. Pessoa jurídica é recusada. Version e updated_at de
// party.person e party.party avançam na mesma transação.
func (r *Repository) UpdateSocialName(ctx context.Context, id uuid.UUID, socialName *string) (Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Party{}, err
	}
	if id == uuid.Nil {
		return Party{}, ErrNotFound
	}
	normalized := normalizeSocialName(KindPerson, socialName)

	var result Party
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id, tenant_id, kind, name, COALESCE(document, ''), status
			FROM party.party WHERE id = $1 AND tenant_id = $2 FOR UPDATE`, id, tenantID).
			Scan(&result.ID, &result.TenantID, &result.Kind, &result.Name, &result.Document, &result.Status); err != nil {
			return err
		}
		if result.Kind != KindPerson {
			return ErrSocialNameNotApplicable
		}
		if _, err := tx.Exec(ctx, `INSERT INTO party.person (tenant_id, party_id, social_name)
			VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, party_id) DO UPDATE
			SET social_name = EXCLUDED.social_name,
			    version = party.person.version + 1,
			    updated_at = now()`, tenantID, id, normalized); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE party.party
			SET version = version + 1, updated_at = now()
			WHERE tenant_id = $1 AND id = $2`, tenantID, id)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Party{}, ErrNotFound
	case errors.Is(err, ErrSocialNameNotApplicable):
		return Party{}, ErrSocialNameNotApplicable
	case err != nil:
		return Party{}, fmt.Errorf("atualizar nome social: %w", err)
	}
	result.SocialName = normalized
	return result, nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Party{}, err
	}
	var result Party
	var social pgtype.Text
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT p.id, p.tenant_id, p.kind, p.name, COALESCE(p.document, ''), p.status, pe.social_name
			FROM party.party p
			LEFT JOIN party.person pe
			  ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
			WHERE p.id = $1 AND p.tenant_id = $2`, id, tenantID).
			Scan(&result.ID, &result.TenantID, &result.Kind, &result.Name, &result.Document, &result.Status, &social)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrNotFound
	}
	if err != nil {
		return Party{}, fmt.Errorf("buscar party: %w", err)
	}
	result.SocialName = socialNameFromText(social)
	return result, nil
}

func (r *Repository) List(ctx context.Context, limit int) ([]Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var result []Party
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT p.id, p.tenant_id, p.kind, p.name, COALESCE(p.document, ''), p.status, pe.social_name
			FROM party.party p
			LEFT JOIN party.person pe
			  ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
			WHERE p.tenant_id = $1 ORDER BY p.id LIMIT $2`, tenantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Party
			var social pgtype.Text
			if err := rows.Scan(&item.ID, &item.TenantID, &item.Kind, &item.Name, &item.Document, &item.Status, &social); err != nil {
				return err
			}
			item.SocialName = socialNameFromText(social)
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar parties: %w", err)
	}
	return result, nil
}

// Search procura parties do tenant por nome civil, nome social ou documento.
// A consulta retorna apenas dados já autorizados pelo tenant; não cria cadastro.
func (r *Repository) Search(ctx context.Context, query string, limit int) ([]Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > maxPartyNameLength {
		return nil, ErrInvalidSearch
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var result []Party
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT p.id, p.tenant_id, p.kind, p.name, COALESCE(p.document, ''), p.status, pe.social_name
			FROM party.party p
			LEFT JOIN party.person pe ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
			WHERE p.tenant_id = $1 AND (p.name ILIKE '%' || $2 || '%' OR COALESCE(pe.social_name, '') ILIKE '%' || $2 || '%' OR COALESCE(p.document, '') ILIKE '%' || $2 || '%')
			ORDER BY p.updated_at DESC, p.id LIMIT $3`, tenantID, query, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Party
			var social pgtype.Text
			if err := rows.Scan(&item.ID, &item.TenantID, &item.Kind, &item.Name, &item.Document, &item.Status, &social); err != nil {
				return err
			}
			item.SocialName = socialNameFromText(social)
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("buscar parties: %w", err)
	}
	return result, nil
}

func normalizeSocialName(kind string, socialName *string) *string {
	if kind != KindPerson || socialName == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*socialName)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func socialNameFromText(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	trimmed := strings.TrimSpace(value.String)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isTenantForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "party_tenant_id_fkey"
}
