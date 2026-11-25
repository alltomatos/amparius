package funeral

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrLocationNotFound      = errors.New("local funerario nao encontrado")
	ErrResourceNotFound      = errors.New("recurso funerario nao encontrado")
	ErrScheduleNotFound      = errors.New("agenda funeraria nao encontrada")
	ErrScheduleInvalid       = errors.New("agenda funeraria invalida")
	ErrScheduleConflict      = errors.New("conflito de agenda funeraria")
	ErrScheduleTransition    = errors.New("transicao de agenda funeraria invalida")
	ErrScheduleIdempotency   = errors.New("chave de idempotencia da agenda reutilizada com conteudo diferente")
	ErrScheduleContractState = errors.New("contrato nao esta ativo para agendamento")
	ErrScheduleTimeZone      = errors.New("timezone da agenda invalido")
)

const (
	ScheduleKindWake   = "wake"
	ScheduleKindBurial = "burial"

	ScheduleScheduled = "scheduled"
	ScheduleConfirmed = "confirmed"
	ScheduleCancelled = "cancelled"
	ScheduleCompleted = "completed"
)

type LocationCreate struct {
	CompanyID      uuid.UUID
	Name           string
	Kind           string
	TimeZone       string
	IdempotencyKey string
}

type Location struct {
	ID, TenantID, CompanyID uuid.UUID
	Name, Kind, TimeZone    string
	IdempotencyKey          string
	CreatedAt, UpdatedAt    time.Time
}

type ResourceCreate struct {
	CompanyID      uuid.UUID
	LocationID     uuid.UUID
	Name           string
	Kind           string
	IdempotencyKey string
}

type Resource struct {
	ID, TenantID, CompanyID, LocationID uuid.UUID
	Name, Kind                          string
	Active                              bool
	IdempotencyKey                      string
	CreatedAt, UpdatedAt                time.Time
}

type ScheduleCreate struct {
	CompanyID      uuid.UUID
	ContractID     uuid.UUID
	ServiceOrderID *uuid.UUID
	Kind           string
	LocationID     uuid.UUID
	ResourceID     *uuid.UUID
	StartsAt       time.Time
	EndsAt         time.Time
	TimeZone       string
	IdempotencyKey string
}

type SchedulePatch struct {
	ID             uuid.UUID
	LocationID     *uuid.UUID
	ResourceID     *uuid.UUID
	ClearResource  bool
	StartsAt       *time.Time
	EndsAt         *time.Time
	TimeZone       *string
	IdempotencyKey string
}

type Schedule struct {
	ID, TenantID, CompanyID, ContractID uuid.UUID
	ServiceOrderID                      *uuid.UUID
	Kind                                string
	LocationID                          uuid.UUID
	ResourceID                          *uuid.UUID
	StartsAt, EndsAt                    time.Time
	TimeZone                            string
	Status                              string
	IdempotencyKey                      string
	Version                             int
	CreatedAt, UpdatedAt                time.Time
	StatusHistory                       []ScheduleStatusChange
}

type ScheduleStatusChange struct {
	ID, TenantID, ScheduleID uuid.UUID
	FromStatus, ToStatus     string
	Reason                   *string
	ChangedAt                time.Time
}

type SchedulingService interface {
	CreateLocation(context.Context, LocationCreate) (Location, error)
	ListLocations(context.Context) ([]Location, error)
	CreateResource(context.Context, ResourceCreate) (Resource, error)
	ListResources(context.Context, uuid.UUID) ([]Resource, error)
	CreateSchedule(context.Context, ScheduleCreate) (Schedule, error)
	ListSchedules(context.Context, uuid.UUID) ([]Schedule, error)
	GetScheduleByID(context.Context, uuid.UUID) (Schedule, error)
	UpdateSchedule(context.Context, SchedulePatch) (Schedule, error)
	CancelSchedule(context.Context, uuid.UUID, string) (Schedule, error)
}

func (r *Repository) CreateLocation(ctx context.Context, input LocationCreate) (Location, error) {
	tenantID, companyID, err := scheduleScope(ctx, input.CompanyID)
	if err != nil {
		return Location{}, err
	}
	input.CompanyID = companyID
	if err := validateLocationCreate(input); err != nil {
		return Location{}, err
	}
	hash := hashText(fmt.Sprintf("%s|%s|%s", input.Name, input.Kind, input.TimeZone))
	var result Location
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		var storedHash string
		err := tx.QueryRow(ctx, `SELECT id,request_hash FROM funeral.location WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&id, &storedHash)
		if err == nil {
			if storedHash != hash {
				return ErrScheduleIdempotency
			}
			return loadLocationTx(ctx, tx, tenantID, companyID, id, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO funeral.location (tenant_id,company_id,name,kind,time_zone,idempotency_key,request_hash) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (tenant_id,idempotency_key) DO NOTHING RETURNING id`, tenantID, companyID, input.Name, input.Kind, input.TimeZone, input.IdempotencyKey, hash).Scan(&id); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT id,request_hash FROM funeral.location WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&id, &storedHash); err != nil {
				return err
			}
			if storedHash != hash {
				return ErrScheduleIdempotency
			}
		}
		return loadLocationTx(ctx, tx, tenantID, companyID, id, &result)
	})
	if errors.Is(err, ErrScheduleIdempotency) {
		return Location{}, err
	}
	if err != nil {
		return Location{}, fmt.Errorf("criar local funerario: %w", err)
	}
	return result, nil
}

func (r *Repository) ListLocations(ctx context.Context) ([]Location, error) {
	tenantID, companyID, err := scheduleScope(ctx, uuid.Nil)
	if err != nil {
		return nil, err
	}
	var result []Location
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM funeral.location WHERE tenant_id=$1 AND company_id=$2 ORDER BY name,id`, tenantID, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			var item Location
			if err := loadLocationTx(ctx, tx, tenantID, companyID, id, &item); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar locais funerarios: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateResource(ctx context.Context, input ResourceCreate) (Resource, error) {
	tenantID, companyID, err := scheduleScope(ctx, input.CompanyID)
	if err != nil {
		return Resource{}, err
	}
	input.CompanyID = companyID
	if err := validateResourceCreate(input); err != nil {
		return Resource{}, err
	}
	hash := hashText(fmt.Sprintf("%s|%s|%s", input.LocationID, input.Name, input.Kind))
	var result Resource
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var locationExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.location WHERE tenant_id=$1 AND company_id=$2 AND id=$3)`, tenantID, companyID, input.LocationID).Scan(&locationExists); err != nil {
			return err
		}
		if !locationExists {
			return ErrLocationNotFound
		}
		var id uuid.UUID
		var storedHash string
		err := tx.QueryRow(ctx, `SELECT id,request_hash FROM funeral.resource WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&id, &storedHash)
		if err == nil {
			if storedHash != hash {
				return ErrScheduleIdempotency
			}
			return loadResourceTx(ctx, tx, tenantID, companyID, id, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO funeral.resource (tenant_id,company_id,location_id,name,kind,idempotency_key,request_hash) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (tenant_id,idempotency_key) DO NOTHING RETURNING id`, tenantID, companyID, input.LocationID, input.Name, input.Kind, input.IdempotencyKey, hash).Scan(&id); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT id,request_hash FROM funeral.resource WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&id, &storedHash); err != nil {
				return err
			}
			if storedHash != hash {
				return ErrScheduleIdempotency
			}
		}
		return loadResourceTx(ctx, tx, tenantID, companyID, id, &result)
	})
	if errors.Is(err, ErrScheduleIdempotency) {
		return Resource{}, err
	}
	if errors.Is(err, ErrLocationNotFound) {
		return Resource{}, err
	}
	if err != nil {
		return Resource{}, fmt.Errorf("criar recurso funerario: %w", err)
	}
	return result, nil
}

func (r *Repository) ListResources(ctx context.Context, locationID uuid.UUID) ([]Resource, error) {
	tenantID, companyID, err := scheduleScope(ctx, uuid.Nil)
	if err != nil {
		return nil, err
	}
	if locationID == uuid.Nil {
		return nil, ErrLocationNotFound
	}
	var result []Resource
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.location WHERE tenant_id=$1 AND company_id=$2 AND id=$3)`, tenantID, companyID, locationID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrLocationNotFound
		}
		rows, err := tx.Query(ctx, `SELECT id FROM funeral.resource WHERE tenant_id=$1 AND company_id=$2 AND location_id=$3 AND active ORDER BY name,id`, tenantID, companyID, locationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			var item Resource
			if err := loadResourceTx(ctx, tx, tenantID, companyID, id, &item); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar recursos funerarios: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateSchedule(ctx context.Context, input ScheduleCreate) (Schedule, error) {
	tenantID, companyID, err := scheduleScope(ctx, input.CompanyID)
	if err != nil {
		return Schedule{}, err
	}
	input.CompanyID = companyID
	if err := validateScheduleCreate(input); err != nil {
		return Schedule{}, err
	}
	if r.eventStore == nil {
		return Schedule{}, ErrOutboxUnavailable
	}
	hash, err := scheduleCreateHash(input)
	if err != nil {
		return Schedule{}, err
	}
	input.StartsAt = input.StartsAt.UTC()
	input.EndsAt = input.EndsAt.UTC()
	var result Schedule
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		var storedHash string
		err := tx.QueryRow(ctx, `SELECT id,request_hash FROM funeral.schedule WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&id, &storedHash)
		if err == nil {
			if storedHash != hash {
				return ErrScheduleIdempotency
			}
			return loadScheduleTx(ctx, tx, tenantID, companyID, id, &result)
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
			return ErrScheduleContractState
		}
		if input.Kind != ScheduleKindWake && input.Kind != ScheduleKindBurial {
			return ErrScheduleInvalid
		}
		var locationExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.location WHERE tenant_id=$1 AND company_id=$2 AND id=$3)`, tenantID, companyID, input.LocationID).Scan(&locationExists); err != nil {
			return err
		}
		if !locationExists {
			return ErrLocationNotFound
		}
		if input.ResourceID != nil {
			var resourceExists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.resource WHERE tenant_id=$1 AND company_id=$2 AND location_id=$3 AND id=$4 AND active)`, tenantID, companyID, input.LocationID, *input.ResourceID).Scan(&resourceExists); err != nil {
				return err
			}
			if !resourceExists {
				return ErrResourceNotFound
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO funeral.schedule (tenant_id,company_id,contract_id,service_order_id,kind,location_id,resource_id,starts_at,ends_at,time_zone,idempotency_key,request_hash) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (tenant_id,idempotency_key) DO NOTHING RETURNING id`, tenantID, companyID, input.ContractID, input.ServiceOrderID, input.Kind, input.LocationID, input.ResourceID, input.StartsAt, input.EndsAt, input.TimeZone, input.IdempotencyKey, hash).Scan(&id); err != nil {
			if isScheduleConflict(err) {
				return ErrScheduleConflict
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT id,request_hash FROM funeral.schedule WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&id, &storedHash); err != nil {
				return err
			}
			if storedHash != hash {
				return ErrScheduleIdempotency
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.schedule_status_history (tenant_id,schedule_id,to_status) VALUES ($1,$2,$3)`, tenantID, id, ScheduleScheduled); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{"schedule_id": id.String(), "contract_id": input.ContractID.String(), "company_id": companyID.String(), "kind": input.Kind, "location_id": input.LocationID.String(), "resource_id": optionalUUIDText(input.ResourceID), "starts_at": input.StartsAt.Format(time.RFC3339Nano), "ends_at": input.EndsAt.Format(time.RFC3339Nano), "time_zone": input.TimeZone, "status": ScheduleScheduled})
		if err != nil {
			return err
		}
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{TenantID: tenantID, AggregateType: "funeral.schedule", AggregateID: id, EventType: "funeral.schedule.created", Version: 1, Payload: payload, IdempotencyKey: "funeral.schedule.create:" + hash[:32]}); err != nil {
			return err
		}
		return loadScheduleTx(ctx, tx, tenantID, companyID, id, &result)
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrScheduleIdempotency), errors.Is(err, ErrScheduleConflict), errors.Is(err, ErrContractNotFound), errors.Is(err, ErrScheduleContractState), errors.Is(err, ErrScheduleInvalid), errors.Is(err, ErrScheduleTimeZone), errors.Is(err, ErrLocationNotFound), errors.Is(err, ErrResourceNotFound):
			return Schedule{}, err
		default:
			return Schedule{}, fmt.Errorf("criar agenda funeraria: %w", err)
		}
	}
	return result, nil
}

func (r *Repository) ListSchedules(ctx context.Context, contractID uuid.UUID) ([]Schedule, error) {
	tenantID, companyID, err := scheduleScope(ctx, uuid.Nil)
	if err != nil {
		return nil, err
	}
	if contractID == uuid.Nil {
		return nil, ErrContractNotFound
	}
	var result []Schedule
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.contract WHERE tenant_id=$1 AND company_id=$2 AND id=$3)`, tenantID, companyID, contractID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrContractNotFound
		}
		rows, err := tx.Query(ctx, `SELECT id FROM funeral.schedule WHERE tenant_id=$1 AND company_id=$2 AND contract_id=$3 ORDER BY starts_at,id`, tenantID, companyID, contractID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			var item Schedule
			if err := loadScheduleTx(ctx, tx, tenantID, companyID, id, &item); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar agenda funeraria: %w", err)
	}
	return result, nil
}

func (r *Repository) GetScheduleByID(ctx context.Context, id uuid.UUID) (Schedule, error) {
	tenantID, companyID, err := scheduleScope(ctx, uuid.Nil)
	if err != nil {
		return Schedule{}, err
	}
	if id == uuid.Nil {
		return Schedule{}, ErrScheduleNotFound
	}
	var result Schedule
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return loadScheduleTx(ctx, tx, tenantID, companyID, id, &result)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, ErrScheduleNotFound
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("buscar agenda funeraria: %w", err)
	}
	return result, nil
}

func (r *Repository) UpdateSchedule(ctx context.Context, input SchedulePatch) (Schedule, error) {
	tenantID, companyID, err := scheduleScope(ctx, uuid.Nil)
	if err != nil {
		return Schedule{}, err
	}
	if err := validateSchedulePatch(input); err != nil {
		return Schedule{}, err
	}
	hash := mutationHash(input.ID, "update", schedulePatchHash(input))
	return r.mutateSchedule(ctx, tenantID, companyID, input.ID, "update", input.IdempotencyKey, hash, func(ctx context.Context, tx pgx.Tx, current Schedule) error {
		if current.Status != ScheduleScheduled {
			return ErrScheduleTransition
		}
		locationID, resourceID := current.LocationID, current.ResourceID
		if input.LocationID != nil {
			locationID = *input.LocationID
		}
		if input.ClearResource {
			resourceID = nil
		} else if input.ResourceID != nil {
			resourceID = input.ResourceID
		}
		startsAt, endsAt, timeZone := current.StartsAt, current.EndsAt, current.TimeZone
		if input.StartsAt != nil {
			startsAt = input.StartsAt.UTC()
		}
		if input.EndsAt != nil {
			endsAt = input.EndsAt.UTC()
		}
		if input.TimeZone != nil {
			timeZone = strings.TrimSpace(*input.TimeZone)
		}
		if err := validateScheduleInterval(startsAt, endsAt, timeZone); err != nil {
			return err
		}
		var locationExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.location WHERE tenant_id=$1 AND company_id=$2 AND id=$3)`, tenantID, companyID, locationID).Scan(&locationExists); err != nil {
			return err
		}
		if !locationExists {
			return ErrLocationNotFound
		}
		if resourceID != nil {
			var resourceExists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.resource WHERE tenant_id=$1 AND company_id=$2 AND location_id=$3 AND id=$4 AND active)`, tenantID, companyID, locationID, *resourceID).Scan(&resourceExists); err != nil {
				return err
			}
			if !resourceExists {
				return ErrResourceNotFound
			}
		}
		_, err := tx.Exec(ctx, `UPDATE funeral.schedule SET location_id=$1,resource_id=$2,starts_at=$3,ends_at=$4,time_zone=$5,version=version+1,updated_at=now() WHERE tenant_id=$6 AND company_id=$7 AND id=$8`, locationID, resourceID, startsAt, endsAt, timeZone, tenantID, companyID, input.ID)
		if isScheduleConflict(err) {
			return ErrScheduleConflict
		}
		return err
	}, "funeral.schedule.updated")
}

func (r *Repository) CancelSchedule(ctx context.Context, id uuid.UUID, key string) (Schedule, error) {
	return r.CancelScheduleWithReason(ctx, id, key, "")
}

// ConfirmSchedule confirma uma reserva agendada. A operação é idempotente por
// (tenant, operação, Idempotency-Key) e mantém o recurso reservado.
func (r *Repository) ConfirmSchedule(ctx context.Context, id uuid.UUID, key string) (Schedule, error) {
	return r.ConfirmScheduleWithReason(ctx, id, key, "")
}

// ConfirmScheduleWithReason confirma uma reserva e registra o motivo no
// histórico append-only. O motivo nunca é publicado na outbox.
func (r *Repository) ConfirmScheduleWithReason(ctx context.Context, id uuid.UUID, key, reason string) (Schedule, error) {
	tenantID, companyID, err := scheduleScope(ctx, uuid.Nil)
	if err != nil {
		return Schedule{}, err
	}
	key = strings.TrimSpace(key)
	reason = strings.TrimSpace(reason)
	if key == "" || len([]rune(key)) > 200 || len([]rune(reason)) > 500 || id == uuid.Nil {
		return Schedule{}, ErrScheduleInvalid
	}
	hash := mutationHash(id, "confirm", hashText(reason))
	return r.mutateSchedule(ctx, tenantID, companyID, id, "confirm", key, hash, func(ctx context.Context, tx pgx.Tx, current Schedule) error {
		if current.Status != ScheduleScheduled {
			return ErrScheduleTransition
		}
		_, err := tx.Exec(ctx, `UPDATE funeral.schedule SET status=$1,version=version+1,updated_at=now() WHERE tenant_id=$2 AND company_id=$3 AND id=$4`, ScheduleConfirmed, tenantID, companyID, id)
		return err
	}, "funeral.schedule.confirmed", reason)
}

// CancelScheduleWithReason cancela uma reserva e registra o motivo no
// histórico append-only. O motivo nunca é publicado na outbox.
func (r *Repository) CancelScheduleWithReason(ctx context.Context, id uuid.UUID, key, reason string) (Schedule, error) {
	tenantID, companyID, err := scheduleScope(ctx, uuid.Nil)
	if err != nil {
		return Schedule{}, err
	}
	key = strings.TrimSpace(key)
	reason = strings.TrimSpace(reason)
	if key == "" || len([]rune(key)) > 200 || len([]rune(reason)) > 500 || id == uuid.Nil {
		return Schedule{}, ErrScheduleInvalid
	}
	hash := mutationHash(id, "cancel", hashText(reason))
	return r.mutateSchedule(ctx, tenantID, companyID, id, "cancel", key, hash, func(ctx context.Context, tx pgx.Tx, current Schedule) error {
		if current.Status != ScheduleScheduled && current.Status != ScheduleConfirmed {
			return ErrScheduleTransition
		}
		_, err := tx.Exec(ctx, `UPDATE funeral.schedule SET status=$1,version=version+1,updated_at=now() WHERE tenant_id=$2 AND company_id=$3 AND id=$4`, ScheduleCancelled, tenantID, companyID, id)
		return err
	}, "funeral.schedule.cancelled", reason)
}

func (r *Repository) mutateSchedule(ctx context.Context, tenantID, companyID, id uuid.UUID, operation, key, hash string, apply func(context.Context, pgx.Tx, Schedule) error, eventType string, reasons ...string) (Schedule, error) {
	if r.eventStore == nil {
		return Schedule{}, ErrOutboxUnavailable
	}
	var result Schedule
	err := r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var existingID uuid.UUID
		var storedHash string
		err := tx.QueryRow(ctx, `SELECT schedule_id,request_hash FROM funeral.schedule_mutation WHERE tenant_id=$1 AND operation=$2 AND idempotency_key=$3`, tenantID, operation, key).Scan(&existingID, &storedHash)
		if err == nil {
			if storedHash != hash {
				return ErrScheduleIdempotency
			}
			return loadScheduleTx(ctx, tx, tenantID, companyID, existingID, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM funeral.schedule WHERE tenant_id=$1 AND company_id=$2 AND id=$3 FOR UPDATE`, tenantID, companyID, id).Scan(&existingID); err != nil {
			return err
		}
		// A second caller with the same key can have passed the first lookup
		// while the winner was committing. Recheck after the aggregate lock so
		// it replays instead of applying the transition twice.
		err = tx.QueryRow(ctx, `SELECT schedule_id,request_hash FROM funeral.schedule_mutation WHERE tenant_id=$1 AND operation=$2 AND idempotency_key=$3`, tenantID, operation, key).Scan(&existingID, &storedHash)
		if err == nil {
			if storedHash != hash {
				return ErrScheduleIdempotency
			}
			return loadScheduleTx(ctx, tx, tenantID, companyID, existingID, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var current Schedule
		if err := loadScheduleTx(ctx, tx, tenantID, companyID, id, &current); err != nil {
			return err
		}
		if err := apply(ctx, tx, current); err != nil {
			return err
		}
		var toStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM funeral.schedule WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenantID, companyID, id).Scan(&toStatus); err != nil {
			return err
		}
		if toStatus != current.Status {
			var reason any
			if len(reasons) > 0 && reasons[0] != "" {
				reason = reasons[0]
			}
			if _, err := tx.Exec(ctx, `INSERT INTO funeral.schedule_status_history (tenant_id,schedule_id,from_status,to_status,reason) VALUES ($1,$2,$3,$4,$5)`, tenantID, id, current.Status, toStatus, reason); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.schedule_mutation (tenant_id,schedule_id,operation,idempotency_key,request_hash) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (tenant_id,operation,idempotency_key) DO NOTHING`, tenantID, id, operation, key, hash); err != nil {
			return err
		}
		var mutationScheduleID uuid.UUID
		var mutationHashValue string
		if err := tx.QueryRow(ctx, `SELECT schedule_id,request_hash FROM funeral.schedule_mutation WHERE tenant_id=$1 AND operation=$2 AND idempotency_key=$3`, tenantID, operation, key).Scan(&mutationScheduleID, &mutationHashValue); err != nil {
			return err
		}
		if mutationScheduleID != id || mutationHashValue != hash {
			return ErrScheduleIdempotency
		}
		payloadMap := map[string]any{"schedule_id": id.String(), "contract_id": current.ContractID.String(), "company_id": companyID.String(), "from_status": current.Status, "to_status": toStatus}
		if operation == "update" {
			var updated Schedule
			if err := loadScheduleTx(ctx, tx, tenantID, companyID, id, &updated); err != nil {
				return err
			}
			payloadMap["kind"] = updated.Kind
			payloadMap["location_id"] = updated.LocationID.String()
			payloadMap["resource_id"] = optionalUUIDText(updated.ResourceID)
			payloadMap["starts_at"] = updated.StartsAt.UTC().Format(time.RFC3339Nano)
			payloadMap["ends_at"] = updated.EndsAt.UTC().Format(time.RFC3339Nano)
			payloadMap["time_zone"] = updated.TimeZone
		}
		payload, err := json.Marshal(payloadMap)
		if err != nil {
			return err
		}
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{TenantID: tenantID, AggregateType: "funeral.schedule", AggregateID: id, EventType: eventType, Version: 1, Payload: payload, IdempotencyKey: "funeral.schedule." + operation + ":" + id.String() + ":" + hash[:32]}); err != nil {
			return err
		}
		return loadScheduleTx(ctx, tx, tenantID, companyID, id, &result)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Schedule{}, ErrScheduleNotFound
	case errors.Is(err, ErrScheduleIdempotency), errors.Is(err, ErrScheduleConflict), errors.Is(err, ErrScheduleTransition), errors.Is(err, ErrScheduleInvalid), errors.Is(err, ErrScheduleTimeZone), errors.Is(err, ErrLocationNotFound), errors.Is(err, ErrResourceNotFound):
		return Schedule{}, err
	case err != nil:
		return Schedule{}, fmt.Errorf("alterar agenda funeraria: %w", err)
	default:
		return result, nil
	}
}

func mutationHash(id uuid.UUID, operation, requestHash string) string {
	return hashText(id.String() + "|" + operation + "|" + requestHash)
}

func scheduleScope(ctx context.Context, requestedCompany uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if requestedCompany != uuid.Nil && requestedCompany != companyID {
		return uuid.Nil, uuid.Nil, ErrTenantMismatch
	}
	return tenantID, companyID, nil
}

func validateLocationCreate(input LocationCreate) error {
	if input.CompanyID == uuid.Nil || strings.TrimSpace(input.Name) == "" || len([]rune(input.Name)) > 200 || strings.TrimSpace(input.Kind) == "" || len([]rune(input.Kind)) > 80 || strings.TrimSpace(input.IdempotencyKey) == "" || len([]rune(input.IdempotencyKey)) > 200 {
		return ErrScheduleInvalid
	}
	if _, err := time.LoadLocation(strings.TrimSpace(input.TimeZone)); err != nil {
		return ErrScheduleTimeZone
	}
	return nil
}

func validateResourceCreate(input ResourceCreate) error {
	if input.CompanyID == uuid.Nil || input.LocationID == uuid.Nil || strings.TrimSpace(input.Name) == "" || len([]rune(input.Name)) > 200 || strings.TrimSpace(input.Kind) == "" || len([]rune(input.Kind)) > 80 || strings.TrimSpace(input.IdempotencyKey) == "" || len([]rune(input.IdempotencyKey)) > 200 {
		return ErrScheduleInvalid
	}
	return nil
}

func validateScheduleCreate(input ScheduleCreate) error {
	if input.CompanyID == uuid.Nil || input.ContractID == uuid.Nil || input.LocationID == uuid.Nil || strings.TrimSpace(input.Kind) == "" || (input.ResourceID != nil && *input.ResourceID == uuid.Nil) || strings.TrimSpace(input.IdempotencyKey) == "" || len([]rune(input.IdempotencyKey)) > 200 {
		return ErrScheduleInvalid
	}
	return validateScheduleInterval(input.StartsAt, input.EndsAt, input.TimeZone)
}

func validateSchedulePatch(input SchedulePatch) error {
	if input.ID == uuid.Nil || strings.TrimSpace(input.IdempotencyKey) == "" || len([]rune(input.IdempotencyKey)) > 200 || (input.LocationID == nil && input.ResourceID == nil && !input.ClearResource && input.StartsAt == nil && input.EndsAt == nil && input.TimeZone == nil) {
		return ErrScheduleInvalid
	}
	if input.LocationID != nil && *input.LocationID == uuid.Nil {
		return ErrScheduleInvalid
	}
	if input.ResourceID != nil && *input.ResourceID == uuid.Nil {
		return ErrScheduleInvalid
	}
	return nil
}

func validateScheduleInterval(startsAt, endsAt time.Time, timeZone string) error {
	if strings.TrimSpace(timeZone) == "" {
		return ErrScheduleTimeZone
	}
	if _, err := time.LoadLocation(strings.TrimSpace(timeZone)); err != nil {
		return ErrScheduleTimeZone
	}
	if startsAt.IsZero() || endsAt.IsZero() || !startsAt.Before(endsAt) {
		return ErrScheduleInvalid
	}
	return nil
}

func scheduleCreateHash(input ScheduleCreate) (string, error) {
	return hashJSON(struct {
		CompanyID, ContractID uuid.UUID
		ServiceOrderID        *uuid.UUID
		Kind                  string
		LocationID            uuid.UUID
		ResourceID            *uuid.UUID
		StartsAt, EndsAt      string
		TimeZone              string
	}{input.CompanyID, input.ContractID, input.ServiceOrderID, input.Kind, input.LocationID, input.ResourceID, input.StartsAt.UTC().Format(time.RFC3339Nano), input.EndsAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(input.TimeZone)})
}

func schedulePatchHash(input SchedulePatch) string {
	return hashText(fmt.Sprintf("%s|%s|%t|%s|%s|%s", input.LocationID, input.ResourceID, input.ClearResource, valueText(input.TimeZone), formatTime(input.StartsAt), formatTime(input.EndsAt)))
}

func isScheduleConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23P01"
}

func optionalUUIDText(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

func loadLocationTx(ctx context.Context, tx pgx.Tx, tenantID, companyID, id uuid.UUID, result *Location) error {
	return tx.QueryRow(ctx, `SELECT id,tenant_id,company_id,name,kind,time_zone,idempotency_key,created_at,updated_at FROM funeral.location WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenantID, companyID, id).Scan(&result.ID, &result.TenantID, &result.CompanyID, &result.Name, &result.Kind, &result.TimeZone, &result.IdempotencyKey, &result.CreatedAt, &result.UpdatedAt)
}

func loadResourceTx(ctx context.Context, tx pgx.Tx, tenantID, companyID, id uuid.UUID, result *Resource) error {
	return tx.QueryRow(ctx, `SELECT id,tenant_id,company_id,location_id,name,kind,active,idempotency_key,created_at,updated_at FROM funeral.resource WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenantID, companyID, id).Scan(&result.ID, &result.TenantID, &result.CompanyID, &result.LocationID, &result.Name, &result.Kind, &result.Active, &result.IdempotencyKey, &result.CreatedAt, &result.UpdatedAt)
}

func loadScheduleTx(ctx context.Context, tx pgx.Tx, tenantID, companyID, id uuid.UUID, result *Schedule) error {
	var serviceOrderID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id,tenant_id,company_id,contract_id,service_order_id,kind,location_id,resource_id,starts_at,ends_at,time_zone,status,idempotency_key,version,created_at,updated_at FROM funeral.schedule WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenantID, companyID, id).Scan(&result.ID, &result.TenantID, &result.CompanyID, &result.ContractID, &serviceOrderID, &result.Kind, &result.LocationID, &result.ResourceID, &result.StartsAt, &result.EndsAt, &result.TimeZone, &result.Status, &result.IdempotencyKey, &result.Version, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return err
	}
	result.ServiceOrderID = serviceOrderID
	result.StatusHistory = nil
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,schedule_id,COALESCE(from_status,''),to_status,reason,changed_at FROM funeral.schedule_status_history WHERE tenant_id=$1 AND schedule_id=$2 ORDER BY changed_at,id`, tenantID, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item ScheduleStatusChange
		if err := rows.Scan(&item.ID, &item.TenantID, &item.ScheduleID, &item.FromStatus, &item.ToStatus, &item.Reason, &item.ChangedAt); err != nil {
			return err
		}
		result.StatusHistory = append(result.StatusHistory, item)
	}
	return rows.Err()
}

var _ SchedulingService = (*Repository)(nil)
