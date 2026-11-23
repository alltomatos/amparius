// Package db centraliza o acesso PostgreSQL com o contexto de tenant obrigatório.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrTenantRequired = errors.New("tenant ausente no contexto")

type tenantContextKey struct{}

func WithTenant(ctx context.Context, tenantID uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenantID)
}

func TenantFromContext(ctx context.Context) (uuid.UUID, error) {
	tenantID, ok := ctx.Value(tenantContextKey{}).(uuid.UUID)
	if !ok || tenantID == uuid.Nil {
		return uuid.Nil, ErrTenantRequired
	}
	return tenantID, nil
}

type Querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type DB struct{ pool *pgxpool.Pool }

func New(ctx context.Context, dsn string) (*DB, error) {
	if dsn == "" {
		return nil, errors.New("DSN do PostgreSQL vazio")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("criar pool PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("conectar ao PostgreSQL: %w", err)
	}
	return &DB{pool: pool}, nil
}

func NewFromPool(pool *pgxpool.Pool) (*DB, error) {
	if pool == nil {
		return nil, errors.New("pool PostgreSQL nulo")
	}
	return &DB{pool: pool}, nil
}

func (db *DB) Close() { db.pool.Close() }

func (db *DB) WithTenantTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	tenantID, err := TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("iniciar transação: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("definir tenant da transação: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("confirmar transação: %w", err)
	}
	return nil
}
