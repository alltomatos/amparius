package party

import (
	"context"
	"errors"
	"fmt"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	KindPerson     = "person"
	KindCompany    = "company"
	StatusActive   = "active"
	StatusInactive = "inactive"
)

var ErrNotFound = errors.New("party não encontrada")

type Party struct {
	ID, TenantID                 uuid.UUID
	Kind, Name, Document, Status string
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
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO party.party (tenant_id, kind, name, document, status)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5)
			RETURNING id`, tenantID, party.Kind, party.Name, party.Document, party.Status).Scan(&party.ID)
	})
	if err != nil {
		return Party{}, fmt.Errorf("criar party: %w", err)
	}
	return party, nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Party{}, err
	}
	var result Party
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, tenant_id, kind, name, COALESCE(document, ''), status
			FROM party.party WHERE id = $1 AND tenant_id = $2`, id, tenantID).
			Scan(&result.ID, &result.TenantID, &result.Kind, &result.Name, &result.Document, &result.Status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrNotFound
	}
	if err != nil {
		return Party{}, fmt.Errorf("buscar party: %w", err)
	}
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
		rows, err := tx.Query(ctx, `SELECT id, tenant_id, kind, name, COALESCE(document, ''), status
			FROM party.party WHERE tenant_id = $1 ORDER BY id LIMIT $2`, tenantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Party
			if err := rows.Scan(&item.ID, &item.TenantID, &item.Kind, &item.Name, &item.Document, &item.Status); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar parties: %w", err)
	}
	return result, nil
}
