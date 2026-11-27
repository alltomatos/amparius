package controlplane

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrPlatformTenantNotFound indica tenant sem registro de licenças na view.
	ErrPlatformTenantNotFound = errors.New("tenant não encontrado no control plane")
	// ErrPlatformReportUnavailable indica view de reporting ausente ou defasada:
	// o contrato é falhar fechado, nunca devolver número estimado (ADR 0040 §6).
	ErrPlatformReportUnavailable = errors.New("view de reporting de plataforma indisponível")
	// ErrPlatformInvalidCursor indica cursor malformado.
	ErrPlatformInvalidCursor = errors.New("cursor inválido")
)

// TenantSummary é a visão agregada de um tenant no control plane. Só ids
// opacos e contagens: sem PII e sem payload fiscal.
type TenantSummary struct {
	TenantID          uuid.UUID
	ModulesWithQuota  int
	QuotaTotal        int
	AssignmentsTotal  int
	AssignmentsActive int
	UserSeats         *int
	UserUsed          *int
	LastEventAt       *time.Time
}

type TenantSummaryPage struct {
	Items      []TenantSummary
	NextCursor string
}

// LicensePool é a cota de um módulo e o uso vigente.
type LicensePool struct {
	TenantID   uuid.UUID
	ModuleCode string
	Quota      int
	InUse      int
	Version    int64
	UpdatedAt  time.Time
}

// PlatformAssignment é uma licença atribuída a uma empresa (CNPJ).
type PlatformAssignment struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	CompanyID  uuid.UUID
	ModuleCode string
	Status     string
	ValidFrom  time.Time
	ValidTo    *time.Time
	Version    int64
	UpdatedAt  time.Time
}

type PlatformAssignmentPage struct {
	Items      []PlatformAssignment
	NextCursor string
}

// PlatformEvent é uma linha da auditoria de entitlement (append-only).
type PlatformEvent struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	ModuleCode string
	CompanyID  uuid.UUID
	ActorID    uuid.UUID
	Action     string
	Origin     string
	At         time.Time
}

type PlatformEventPage struct {
	Items      []PlatformEvent
	NextCursor string
}

// PlatformReader lê as views `reporting.platform_*` do control plane. É a única
// leitura cross-tenant permitida; nunca consulta tabelas de módulo.
type PlatformReader struct{ database *db.DB }

func NewPlatformReader(database *db.DB) (*PlatformReader, error) {
	if database == nil {
		return nil, errors.New("banco do control plane obrigatório")
	}
	return &PlatformReader{database: database}, nil
}

func (r *PlatformReader) ListTenants(ctx context.Context, cursor string, limit int) (TenantSummaryPage, error) {
	after, err := decodeUUIDCursor(cursor)
	if err != nil {
		return TenantSummaryPage{}, err
	}
	limit = clampLimit(limit)
	var items []TenantSummary
	err = r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id, modules_with_quota, quota_total, assignments_total, assignments_active,
			       user_seats, user_used, last_event_at
			  FROM reporting.platform_tenant_summary
			 WHERE ($1::uuid IS NULL OR tenant_id > $1)
			 ORDER BY tenant_id
			 LIMIT $2`, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item TenantSummary
			if err := rows.Scan(&item.TenantID, &item.ModulesWithQuota, &item.QuotaTotal, &item.AssignmentsTotal,
				&item.AssignmentsActive, &item.UserSeats, &item.UserUsed, &item.LastEventAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return TenantSummaryPage{}, classifyReportError(err)
	}
	page := TenantSummaryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeUUIDCursor(page.Items[limit-1].TenantID)
	}
	return page, nil
}

func (r *PlatformReader) GetTenant(ctx context.Context, tenantID uuid.UUID) (TenantSummary, error) {
	var item TenantSummary
	err := r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT tenant_id, modules_with_quota, quota_total, assignments_total, assignments_active,
			       user_seats, user_used, last_event_at
			  FROM reporting.platform_tenant_summary
			 WHERE tenant_id = $1`, tenantID).Scan(&item.TenantID, &item.ModulesWithQuota, &item.QuotaTotal,
			&item.AssignmentsTotal, &item.AssignmentsActive, &item.UserSeats, &item.UserUsed, &item.LastEventAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TenantSummary{}, ErrPlatformTenantNotFound
	}
	if err != nil {
		return TenantSummary{}, classifyReportError(err)
	}
	return item, nil
}

func (r *PlatformReader) ListLicensePools(ctx context.Context, tenantID uuid.UUID) ([]LicensePool, error) {
	items := []LicensePool{}
	err := r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant_id, module_code, quota, in_use, version, updated_at
			  FROM reporting.platform_license_pool
			 WHERE tenant_id = $1
			 ORDER BY module_code`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LicensePool
			if err := rows.Scan(&item.TenantID, &item.ModuleCode, &item.Quota, &item.InUse, &item.Version, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, classifyReportError(err)
	}
	return items, nil
}

func (r *PlatformReader) ListAssignments(ctx context.Context, tenantID uuid.UUID, cursor string, limit int) (PlatformAssignmentPage, error) {
	after, err := decodeUUIDCursor(cursor)
	if err != nil {
		return PlatformAssignmentPage{}, err
	}
	limit = clampLimit(limit)
	var items []PlatformAssignment
	err = r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, company_id, module_code, status, valid_from, valid_to, version, updated_at
			  FROM reporting.platform_license_assignment
			 WHERE tenant_id = $1 AND ($2::uuid IS NULL OR id > $2)
			 ORDER BY id
			 LIMIT $3`, tenantID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item PlatformAssignment
			if err := rows.Scan(&item.ID, &item.TenantID, &item.CompanyID, &item.ModuleCode, &item.Status,
				&item.ValidFrom, &item.ValidTo, &item.Version, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return PlatformAssignmentPage{}, classifyReportError(err)
	}
	page := PlatformAssignmentPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeUUIDCursor(page.Items[limit-1].ID)
	}
	return page, nil
}

// ListEvents devolve a auditoria mais recente primeiro (uuidv7 é ordenável no tempo).
func (r *PlatformReader) ListEvents(ctx context.Context, tenantID uuid.UUID, cursor string, limit int) (PlatformEventPage, error) {
	before, err := decodeUUIDCursor(cursor)
	if err != nil {
		return PlatformEventPage{}, err
	}
	limit = clampLimit(limit)
	var items []PlatformEvent
	err = r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, module_code, company_id, actor_id, action, origin, "at"
			  FROM reporting.platform_entitlement_event
			 WHERE tenant_id = $1 AND ($2::uuid IS NULL OR id < $2)
			 ORDER BY id DESC
			 LIMIT $3`, tenantID, before, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item PlatformEvent
			if err := rows.Scan(&item.ID, &item.TenantID, &item.ModuleCode, &item.CompanyID, &item.ActorID,
				&item.Action, &item.Origin, &item.At); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return PlatformEventPage{}, classifyReportError(err)
	}
	page := PlatformEventPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeUUIDCursor(page.Items[limit-1].ID)
	}
	return page, nil
}

func clampLimit(limit int) int {
	if limit < 1 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func encodeUUIDCursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id.String()))
}

func decodeUUIDCursor(cursor string) (*uuid.UUID, error) {
	if strings.TrimSpace(cursor) == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, ErrPlatformInvalidCursor
	}
	id, err := uuid.Parse(string(payload))
	if err != nil || id == uuid.Nil {
		return nil, ErrPlatformInvalidCursor
	}
	return &id, nil
}

// classifyReportError converte view ausente/defasada em erro fechado.
func classifyReportError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42P01", "42703", "3F000", "42501": // tabela/coluna/schema ausente, sem permissão
			return fmt.Errorf("%w: %s", ErrPlatformReportUnavailable, pgErr.Code)
		}
	}
	return fmt.Errorf("consultar reporting de plataforma: %w", err)
}
