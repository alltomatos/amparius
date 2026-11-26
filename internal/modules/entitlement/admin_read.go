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

type eventCursor struct {
	At time.Time `json:"at"`
	ID uuid.UUID `json:"id"`
}

// ListEventsPage lista a auditoria com cursor estável e filtros tenant-scoped.
func (s *Service) ListEventsPage(ctx context.Context, filter EventFilter, cursor string) (EventPage, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return EventPage{}, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var companyArg any
	if filter.CompanyID != nil {
		companyArg = *filter.CompanyID
	}
	var after *eventCursor
	if strings.TrimSpace(cursor) != "" {
		payload, decodeErr := base64.RawURLEncoding.DecodeString(cursor)
		if decodeErr != nil {
			return EventPage{}, ErrInvalidCursor
		}
		var value eventCursor
		if json.Unmarshal(payload, &value) != nil || value.ID == uuid.Nil || value.At.IsZero() {
			return EventPage{}, ErrInvalidCursor
		}
		after = &value
	}

	var events []Event
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var at any
		var id any
		if after != nil {
			at, id = after.At, after.ID
		}
		rows, err := tx.Query(ctx, `
			SELECT audit.id, audit.tenant_id, audit.module_code, audit.company_id,
			       audit.actor_id, audit.action, audit.origin, audit."at"
			  FROM (
				SELECT id, tenant_id, module_code, company_id, actor_id, action, origin, "at"
				  FROM entitlement.entitlement_event
				 WHERE tenant_id = $1
				   AND ($2 = '' OR module_code = $2)
				   AND ($3::uuid IS NULL OR company_id = $3)
				   AND ($4 = '' OR origin = $4)
				UNION ALL
				SELECT id, tenant_id, module_code, NULL::uuid, actor_id, 'quota_adjusted', 'admin', "at"
				  FROM entitlement.quota_event
				 WHERE tenant_id = $1
				   AND ($2 = '' OR module_code = $2)
				   AND ($4 = '' OR $4 = 'admin')
			  ) audit
			 WHERE ($5::timestamptz IS NULL OR (audit."at", audit.id) < ($5::timestamptz, $6::uuid))
			 ORDER BY audit."at" DESC, audit.id DESC
			 LIMIT $7`, tenantID, filter.ModuleCode, companyArg, filter.Origin, at, id, limit+1)
		if err != nil {
			return fmt.Errorf("%w: consultar auditoria: %w", ErrEntitlementUnavailable, err)
		}
		defer rows.Close()
		for rows.Next() {
			var event Event
			if err := rows.Scan(&event.ID, &event.TenantID, &event.ModuleCode, &event.CompanyID, &event.ActorID, &event.Action, &event.Origin, &event.At); err != nil {
				return fmt.Errorf("%w: ler auditoria: %w", ErrEntitlementUnavailable, err)
			}
			events = append(events, event)
		}
		return rows.Err()
	})
	if err != nil {
		return EventPage{}, classifyServiceError(err)
	}
	page := EventPage{Items: events}
	if len(events) > limit {
		page.Items = events[:limit]
		last := page.Items[limit-1]
		payload, err := json.Marshal(eventCursor{At: last.At, ID: last.ID})
		if err != nil {
			return EventPage{}, fmt.Errorf("gerar cursor de auditoria: %w", err)
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(payload)
	}
	return page, nil
}

// AssignmentByID resolve uma licença apenas dentro do tenant do contexto.
func (s *Service) AssignmentByID(ctx context.Context, assignmentID uuid.UUID) (Assignment, error) {
	if assignmentID == uuid.Nil {
		return Assignment{}, fmt.Errorf("%w: entitlementId obrigatório", ErrInvalidRequest)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Assignment{}, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	var result Assignment
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT id, tenant_id, module_code, company_id, valid_from, valid_to, status, version, updated_at
			  FROM entitlement.license_assignment
			 WHERE tenant_id = $1 AND id = $2`, tenantID, assignmentID).Scan(
			&result.ID, &result.TenantID, &result.ModuleCode, &result.CompanyID,
			&result.ValidFrom, &result.ValidTo, &result.Status, &result.Version, &result.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAssignmentNotFound
		}
		return err
	})
	if err != nil {
		return Assignment{}, classifyServiceError(err)
	}
	return result, nil
}
