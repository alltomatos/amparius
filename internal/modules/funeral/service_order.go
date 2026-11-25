package funeral

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrServiceOrderNotFound    = errors.New("ordem de servico nao encontrada")
	ErrServiceOrderInvalid     = errors.New("ordem de servico invalida")
	ErrServiceOrderStatus      = errors.New("status de ordem de servico invalido")
	ErrServiceOrderTransition  = errors.New("transicao de ordem de servico invalida")
	ErrContractNotActive       = errors.New("contrato nao esta ativo")
	ErrServiceOrderIdempotency = errors.New("chave de idempotencia da ordem reutilizada com conteudo diferente")
)

const (
	ServiceOrderOpen       = "open"
	ServiceOrderInProgress = "in_progress"
	ServiceOrderCompleted  = "completed"
	ServiceOrderCancelled  = "cancelled"
)

type ServiceOrderCreate struct {
	CompanyID      uuid.UUID
	ContractID     uuid.UUID
	Kind           string
	Description    string
	ScheduledFor   *time.Time
	IdempotencyKey string
}

type ServiceOrderPatch struct {
	ID                uuid.UUID
	Description       *string
	Status            string
	ScheduledFor      *time.Time
	ScheduledForSet   bool
	ClearScheduledFor bool
	IdempotencyKey    string
}

type ServiceOrder struct {
	ID, TenantID, CompanyID, ContractID uuid.UUID
	Kind, Description, Status           string
	ScheduledFor, CompletedAt           *time.Time
	IdempotencyKey                      string
	Version                             int
	CreatedAt, UpdatedAt                time.Time
	StatusHistory                       []ServiceOrderStatusChange
}

type ServiceOrderStatusChange struct {
	ID, TenantID, ServiceOrderID uuid.UUID
	FromStatus, ToStatus         string
	Reason                       *string
	ChangedAt                    time.Time
}

type ServiceOrderService interface {
	CreateServiceOrder(context.Context, ServiceOrderCreate) (ServiceOrder, error)
	ListServiceOrders(context.Context, uuid.UUID) ([]ServiceOrder, error)
	GetServiceOrderByID(context.Context, uuid.UUID) (ServiceOrder, error)
	UpdateServiceOrder(context.Context, ServiceOrderPatch) (ServiceOrder, error)
	CompleteServiceOrder(context.Context, uuid.UUID, string) (ServiceOrder, error)
}

func (r *Repository) CreateServiceOrder(ctx context.Context, input ServiceOrderCreate) (ServiceOrder, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return ServiceOrder{}, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return ServiceOrder{}, err
	}
	if input.CompanyID != uuid.Nil && input.CompanyID != companyID {
		return ServiceOrder{}, ErrTenantMismatch
	}
	input.CompanyID = companyID
	if err := validateServiceOrderCreate(input); err != nil {
		return ServiceOrder{}, err
	}
	if r.eventStore == nil {
		return ServiceOrder{}, ErrOutboxUnavailable
	}
	hash, err := serviceOrderCreateHash(input)
	if err != nil {
		return ServiceOrder{}, err
	}
	var result ServiceOrder
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var existingID uuid.UUID
		var existingHash string
		err := tx.QueryRow(ctx, `SELECT id, request_hash FROM funeral.service_order WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&existingID, &existingHash)
		if err == nil {
			if existingHash != hash {
				return ErrServiceOrderIdempotency
			}
			return loadServiceOrderTx(ctx, tx, tenantID, companyID, existingID, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var contractStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM funeral.contract WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenantID, companyID, input.ContractID).Scan(&contractStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrContractNotFound
			}
			return err
		}
		if contractStatus != ContractActive {
			return ErrContractNotActive
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO funeral.service_order
			(tenant_id,company_id,contract_id,kind,description,scheduled_for,idempotency_key,request_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (tenant_id,idempotency_key) DO NOTHING RETURNING id`, tenantID, companyID, input.ContractID,
			input.Kind, input.Description, input.ScheduledFor, input.IdempotencyKey, hash).Scan(&id); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT id, request_hash FROM funeral.service_order WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&id, &existingHash); err != nil {
				return err
			}
			if existingHash != hash {
				return ErrServiceOrderIdempotency
			}
			return loadServiceOrderTx(ctx, tx, tenantID, companyID, id, &result)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.service_order_status_history (tenant_id,service_order_id,to_status) VALUES ($1,$2,$3)`, tenantID, id, ServiceOrderOpen); err != nil {
			return err
		}
		payloadMap := map[string]any{"service_order_id": id.String(), "contract_id": input.ContractID.String(), "company_id": companyID.String(), "status": ServiceOrderOpen}
		if scheduledFor := formatTime(input.ScheduledFor); scheduledFor != "" {
			payloadMap["scheduled_for"] = scheduledFor
		}
		payload, err := json.Marshal(payloadMap)
		if err != nil {
			return err
		}
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{TenantID: tenantID, AggregateType: "funeral.service_order", AggregateID: id, EventType: "funeral.service_order.created", Version: 1, Payload: payload, IdempotencyKey: "funeral.service_order.create:" + hash[:32]}); err != nil {
			return err
		}
		return loadServiceOrderTx(ctx, tx, tenantID, companyID, id, &result)
	})
	if errors.Is(err, ErrServiceOrderIdempotency) || errors.Is(err, ErrContractNotFound) || errors.Is(err, ErrContractNotActive) {
		return ServiceOrder{}, err
	}
	if err != nil {
		return ServiceOrder{}, fmt.Errorf("abrir ordem de servico: %w", err)
	}
	return result, nil
}

func (r *Repository) ListServiceOrders(ctx context.Context, contractID uuid.UUID) ([]ServiceOrder, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if contractID == uuid.Nil {
		return nil, ErrContractNotFound
	}
	var result []ServiceOrder
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var contractExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.contract WHERE tenant_id=$1 AND company_id=$2 AND id=$3)`, tenantID, companyID, contractID).Scan(&contractExists); err != nil {
			return err
		}
		if !contractExists {
			return ErrContractNotFound
		}
		rows, err := tx.Query(ctx, `SELECT id FROM funeral.service_order WHERE tenant_id=$1 AND company_id=$2 AND contract_id=$3 ORDER BY created_at,id`, tenantID, companyID, contractID)
		if err != nil {
			return err
		}
		defer rows.Close()
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		for _, id := range ids {
			var order ServiceOrder
			if err := loadServiceOrderTx(ctx, tx, tenantID, companyID, id, &order); err != nil {
				return err
			}
			result = append(result, order)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listar ordens de servico: %w", err)
	}
	return result, nil
}

func (r *Repository) GetServiceOrderByID(ctx context.Context, id uuid.UUID) (ServiceOrder, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return ServiceOrder{}, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return ServiceOrder{}, err
	}
	if id == uuid.Nil {
		return ServiceOrder{}, ErrServiceOrderNotFound
	}
	var result ServiceOrder
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return loadServiceOrderTx(ctx, tx, tenantID, companyID, id, &result)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceOrder{}, ErrServiceOrderNotFound
	}
	if err != nil {
		return ServiceOrder{}, fmt.Errorf("buscar ordem de servico: %w", err)
	}
	return result, nil
}

func (r *Repository) UpdateServiceOrder(ctx context.Context, input ServiceOrderPatch) (ServiceOrder, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return ServiceOrder{}, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return ServiceOrder{}, err
	}
	if err := validateServiceOrderPatch(input); err != nil {
		return ServiceOrder{}, err
	}
	return r.mutateServiceOrder(ctx, tenantID, companyID, input.ID, "update", input.IdempotencyKey, serviceOrderPatchHash(input), func(ctx context.Context, tx pgx.Tx, current ServiceOrder) error {
		if current.Status == ServiceOrderCompleted || current.Status == ServiceOrderCancelled {
			return ErrServiceOrderTransition
		}
		if input.Status != "" {
			if !validServiceOrderTransition(current.Status, input.Status) {
				return ErrServiceOrderTransition
			}
			completedAt := any(nil)
			if input.Status == ServiceOrderCompleted {
				completedAt = time.Now().UTC()
			}
			if _, err := tx.Exec(ctx, `UPDATE funeral.service_order SET status=$1, completed_at=$2, version=version+1, updated_at=now() WHERE tenant_id=$3 AND company_id=$4 AND id=$5`, input.Status, completedAt, tenantID, companyID, input.ID); err != nil {
				return err
			}
		}
		var scheduled any
		if input.ScheduledForSet {
			if !input.ClearScheduledFor && input.ScheduledFor != nil {
				scheduled = input.ScheduledFor
			}
		} else {
			scheduled = current.ScheduledFor
		}
		description := current.Description
		if input.Description != nil {
			description = strings.TrimSpace(*input.Description)
		}
		if input.Status != "" {
			_, err := tx.Exec(ctx, `UPDATE funeral.service_order SET description=$1, scheduled_for=$2 WHERE tenant_id=$3 AND company_id=$4 AND id=$5`, description, scheduled, tenantID, companyID, input.ID)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE funeral.service_order SET description=$1, scheduled_for=$2, version=version+1, updated_at=now() WHERE tenant_id=$3 AND company_id=$4 AND id=$5`, description, scheduled, tenantID, companyID, input.ID)
		return err
	}, serviceOrderUpdateEventType(input))
}

func (r *Repository) CompleteServiceOrder(ctx context.Context, id uuid.UUID, key string) (ServiceOrder, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return ServiceOrder{}, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return ServiceOrder{}, err
	}
	key = strings.TrimSpace(key)
	if key == "" || len([]rune(key)) > 160 {
		return ServiceOrder{}, ErrServiceOrderInvalid
	}
	hash := hashText("complete")
	return r.mutateServiceOrder(ctx, tenantID, companyID, id, "complete", key, hash, func(ctx context.Context, tx pgx.Tx, current ServiceOrder) error {
		if current.Status != ServiceOrderOpen && current.Status != ServiceOrderInProgress {
			return ErrServiceOrderTransition
		}
		_, err := tx.Exec(ctx, `UPDATE funeral.service_order SET status=$1, completed_at=now(), version=version+1, updated_at=now() WHERE tenant_id=$2 AND company_id=$3 AND id=$4`, ServiceOrderCompleted, tenantID, companyID, id)
		return err
	}, "funeral.service_order.completed")
}

func (r *Repository) mutateServiceOrder(ctx context.Context, tenantID, companyID, id uuid.UUID, operation, key, hash string, apply func(context.Context, pgx.Tx, ServiceOrder) error, eventType string) (ServiceOrder, error) {
	if r.eventStore == nil {
		return ServiceOrder{}, ErrOutboxUnavailable
	}
	var result ServiceOrder
	err := r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var orderID uuid.UUID
		var storedHash string
		err := tx.QueryRow(ctx, `SELECT service_order_id, request_hash FROM funeral.service_order_mutation WHERE tenant_id=$1 AND operation=$2 AND idempotency_key=$3`, tenantID, operation, key).Scan(&orderID, &storedHash)
		if err == nil {
			if storedHash != hash {
				return ErrServiceOrderIdempotency
			}
			return loadServiceOrderTx(ctx, tx, tenantID, companyID, orderID, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT 1 FROM funeral.service_order WHERE tenant_id=$1 AND company_id=$2 AND id=$3 FOR UPDATE`, tenantID, companyID, id).Scan(new(int)); err != nil {
			return err
		}
		var current ServiceOrder
		if err := loadServiceOrderTx(ctx, tx, tenantID, companyID, id, &current); err != nil {
			return err
		}
		if err := apply(ctx, tx, current); err != nil {
			return err
		}
		fromStatus := current.Status
		var toStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM funeral.service_order WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenantID, companyID, id).Scan(&toStatus); err != nil {
			return err
		}
		if toStatus != fromStatus {
			if _, err := tx.Exec(ctx, `INSERT INTO funeral.service_order_status_history (tenant_id,service_order_id,from_status,to_status) VALUES ($1,$2,$3,$4)`, tenantID, id, fromStatus, toStatus); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.service_order_mutation (tenant_id,service_order_id,operation,idempotency_key,request_hash) VALUES ($1,$2,$3,$4,$5)`, tenantID, id, operation, key, hash); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{"service_order_id": id.String(), "contract_id": current.ContractID.String(), "company_id": companyID.String(), "from_status": fromStatus, "to_status": toStatus})
		if err != nil {
			return err
		}
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{TenantID: tenantID, AggregateType: "funeral.service_order", AggregateID: id, EventType: eventType, Version: 1, Payload: payload, IdempotencyKey: "funeral.service_order." + operation + ":" + id.String() + ":" + hash[:32]}); err != nil {
			return err
		}
		return loadServiceOrderTx(ctx, tx, tenantID, companyID, id, &result)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ServiceOrder{}, ErrServiceOrderNotFound
	case errors.Is(err, ErrContractNotFound):
		return ServiceOrder{}, err
	case errors.Is(err, ErrServiceOrderIdempotency), errors.Is(err, ErrServiceOrderTransition):
		return ServiceOrder{}, err
	case err != nil:
		return ServiceOrder{}, fmt.Errorf("alterar ordem de servico: %w", err)
	}
	return result, nil
}

func loadServiceOrderTx(ctx context.Context, tx pgx.Tx, tenantID, companyID, id uuid.UUID, result *ServiceOrder) error {
	var scheduled, completed *time.Time
	if err := tx.QueryRow(ctx, `SELECT id,tenant_id,company_id,contract_id,kind,description,status,scheduled_for,completed_at,idempotency_key,version,created_at,updated_at FROM funeral.service_order WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenantID, companyID, id).Scan(&result.ID, &result.TenantID, &result.CompanyID, &result.ContractID, &result.Kind, &result.Description, &result.Status, &scheduled, &completed, &result.IdempotencyKey, &result.Version, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return err
	}
	result.ScheduledFor, result.CompletedAt = scheduled, completed
	result.StatusHistory = nil
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,service_order_id,COALESCE(from_status,''),to_status,reason,changed_at FROM funeral.service_order_status_history WHERE tenant_id=$1 AND service_order_id=$2 ORDER BY changed_at,id`, tenantID, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item ServiceOrderStatusChange
		var reason *string
		if err := rows.Scan(&item.ID, &item.TenantID, &item.ServiceOrderID, &item.FromStatus, &item.ToStatus, &reason, &item.ChangedAt); err != nil {
			return err
		}
		item.Reason = reason
		result.StatusHistory = append(result.StatusHistory, item)
	}
	return rows.Err()
}

func validateServiceOrderCreate(input ServiceOrderCreate) error {
	if input.CompanyID == uuid.Nil || input.ContractID == uuid.Nil || strings.TrimSpace(input.Kind) == "" || len([]rune(input.Kind)) > 100 || len(input.Description) > 2000 || strings.TrimSpace(input.IdempotencyKey) == "" || len([]rune(input.IdempotencyKey)) > 160 {
		return ErrServiceOrderInvalid
	}
	return nil
}
func validateServiceOrderPatch(input ServiceOrderPatch) error {
	if input.ID == uuid.Nil || strings.TrimSpace(input.IdempotencyKey) == "" || len([]rune(input.IdempotencyKey)) > 160 || (input.Description == nil && input.Status == "" && !input.ScheduledForSet) {
		return ErrServiceOrderInvalid
	}
	if input.Status != "" && !validServiceOrderStatus(input.Status) {
		return ErrServiceOrderStatus
	}
	if input.Description != nil && len([]rune(*input.Description)) > 2000 {
		return ErrServiceOrderInvalid
	}
	return nil
}
func serviceOrderCreateHash(input ServiceOrderCreate) (string, error) {
	return hashJSON(struct {
		CompanyID, ContractID uuid.UUID
		Kind, Description     string
		ScheduledFor          string
	}{input.CompanyID, input.ContractID, input.Kind, input.Description, formatTime(input.ScheduledFor)})
}
func serviceOrderPatchHash(input ServiceOrderPatch) string {
	scheduled := formatTime(input.ScheduledFor)
	return hashText(fmt.Sprintf("%s|%s|%s|%t|%t", valueText(input.Description), input.Status, scheduled, input.ScheduledForSet, input.ClearScheduledFor))
}

func serviceOrderUpdateEventType(input ServiceOrderPatch) string {
	if input.Status != "" {
		return "funeral.service_order.status_changed"
	}
	return "funeral.service_order.updated"
}

func validServiceOrderStatus(status string) bool {
	return status == ServiceOrderOpen || status == ServiceOrderInProgress || status == ServiceOrderCompleted || status == ServiceOrderCancelled
}

func validServiceOrderTransition(current, next string) bool {
	if current == next {
		return true
	}
	return (current == ServiceOrderOpen && (next == ServiceOrderInProgress || next == ServiceOrderCompleted || next == ServiceOrderCancelled)) ||
		(current == ServiceOrderInProgress && (next == ServiceOrderCompleted || next == ServiceOrderCancelled))
}
func hashJSON(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return hashText(string(b)), nil
}
func hashText(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}
func valueText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

var _ ServiceOrderService = (*Repository)(nil)
