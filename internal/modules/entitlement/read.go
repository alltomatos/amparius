package entitlement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidCursor = errors.New("cursor inválido")

type Entitlement struct {
	ID              *uuid.UUID
	TenantID        uuid.UUID
	CompanyID       uuid.UUID
	ModuleCode      string
	ModuleName      string
	Kind            string
	Status          string
	Quota           int
	QuotaConfigured bool
	Used            int
	ValidFrom       *time.Time
	ValidTo         *time.Time
	UpdatedAt       *time.Time
	Version         int64
	sortOrder       int
}

type EntitlementPage struct {
	Items      []Entitlement
	NextCursor string
}

type entitlementCursor struct {
	SortOrder int    `json:"sortOrder"`
	Code      string `json:"code"`
}

// ListForCompany devolve a fotografia de todos os módulos ativos do catálogo
// para uma empresa. Módulos core aparecem como active; opcionais sem licença
// aparecem como inactive. O tenant vem exclusivamente do contexto.
func (s *Service) ListForCompany(ctx context.Context, companyID uuid.UUID, at time.Time, cursor string, limit int) (EntitlementPage, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return EntitlementPage{}, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	if companyID == uuid.Nil || at.IsZero() {
		return EntitlementPage{}, fmt.Errorf("%w: empresa e data são obrigatórias", ErrInvalidRequest)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	after, err := decodeEntitlementCursor(cursor)
	if err != nil {
		return EntitlementPage{}, err
	}

	args := []any{tenantID, companyID, at.UTC().Format("2006-01-02")}
	whereCursor := ""
	if after != nil {
		whereCursor = " AND (m.sort_order, m.code) > ($4, $5)"
		args = append(args, after.SortOrder, after.Code)
	}
	args = append(args, limit+1)
	limitPlaceholder := fmt.Sprint(len(args))
	query := `
		SELECT m.code, m.name, m.kind, m.sort_order,
		       CASE
		         WHEN m.kind = 'core' THEN 'active'
		         WHEN a.id IS NULL THEN 'inactive'
		         WHEN a.status = 'active' AND a.valid_from > $3::date THEN 'inactive'
		         WHEN a.status = 'active' AND a.valid_to IS NOT NULL AND a.valid_to < $3::date THEN 'expired'
		         ELSE a.status
		       END AS status,
		       COALESCE(lp.quota, 0), lp.module_code IS NOT NULL,
		       COALESCE((
		         SELECT count(*)
		           FROM entitlement.license_assignment used
		          WHERE used.tenant_id = $1
		            AND used.module_code = m.code
		            AND used.status = 'active'
		            AND used.valid_from <= $3::date
		            AND (used.valid_to IS NULL OR used.valid_to >= $3::date)
		       ), 0),
		       a.id, a.valid_from, a.valid_to, COALESCE(a.version, 0), a.updated_at
		  FROM entitlement.module m
		  LEFT JOIN entitlement.license_pool lp
		    ON lp.tenant_id = $1 AND lp.module_code = m.code
		  LEFT JOIN LATERAL (
				SELECT la.id, la.status, la.valid_from, la.valid_to, la.version, la.updated_at
				  FROM entitlement.license_assignment la
				 WHERE la.tenant_id = $1
				   AND la.module_code = m.code
				   AND la.company_id = $2
				 ORDER BY la.created_at DESC, la.id DESC
				 LIMIT 1
		  ) a ON true
		 WHERE m.active` + whereCursor + `
		 ORDER BY m.sort_order ASC, m.code ASC
		 LIMIT $` + limitPlaceholder

	items := make([]Entitlement, 0, limit)
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Entitlement
			var sortOrder int
			if err := rows.Scan(
				&item.ModuleCode, &item.ModuleName, &item.Kind, &sortOrder,
				&item.Status, &item.Quota, &item.QuotaConfigured, &item.Used,
				&item.ID, &item.ValidFrom, &item.ValidTo, &item.Version, &item.UpdatedAt,
			); err != nil {
				return err
			}
			item.TenantID = tenantID
			item.CompanyID = companyID
			item.sortOrder = sortOrder
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return EntitlementPage{}, fmt.Errorf("%w: listar entitlements: %v", ErrEntitlementUnavailable, err)
	}

	page := EntitlementPage{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = items[:limit]
		page.NextCursor, err = encodeEntitlementCursor(entitlementCursor{SortOrder: last.sortOrder, Code: last.ModuleCode})
		if err != nil {
			return EntitlementPage{}, fmt.Errorf("gerar cursor de entitlements: %w", err)
		}
	}
	return page, nil
}

// GetForCompany devolve o detalhe de um módulo sem aceitar tenant ou empresa
// fora do contexto confiável. O catálogo continua sendo a fonte do detalhe de
// módulos core sem assignment.
func (s *Service) GetForCompany(ctx context.Context, companyID uuid.UUID, moduleCode string, at time.Time) (Entitlement, error) {
	if strings.TrimSpace(moduleCode) == "" {
		return Entitlement{}, fmt.Errorf("%w: módulo obrigatório", ErrInvalidRequest)
	}
	page, err := s.ListForCompany(ctx, companyID, at, "", 100)
	if err != nil {
		return Entitlement{}, err
	}
	for _, item := range page.Items {
		if item.ModuleCode == moduleCode {
			return item, nil
		}
	}
	return Entitlement{}, fmt.Errorf("%w: módulo %q", ErrAssignmentNotFound, moduleCode)
}

func encodeEntitlementCursor(cursor entitlementCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeEntitlementCursor(value string) (*entitlementCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var cursor entitlementCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.SortOrder < 1 || strings.TrimSpace(cursor.Code) == "" {
		return nil, ErrInvalidCursor
	}
	return &cursor, nil
}
