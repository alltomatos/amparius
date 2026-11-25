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
)

var (
	ErrJourneyInvalid       = errors.New("jornada funeraria invalida")
	ErrJourneyIdempotency   = errors.New("chave de idempotencia da jornada reutilizada com conteudo diferente")
	ErrJourneyContractState = errors.New("contrato nao esta ativo para a jornada")
	ErrJourneyServiceOrder  = errors.New("ordem de servico da jornada invalida")
	ErrJourneyLocation      = errors.New("local da jornada nao encontrado")
	ErrJourneyResource      = errors.New("recurso da jornada nao encontrado")
)

// FuneralJourneyCreate descreve a fatia operacional criada em uma unica
// transacao. O contrato ja deve existir e estar active.
type FuneralJourneyCreate struct {
	ContractID               uuid.UUID
	ServiceOrderKind         string
	ServiceOrderDescription  string
	ServiceOrderScheduledFor *time.Time
	ScheduleKind             string
	LocationID               uuid.UUID
	ResourceID               *uuid.UUID
	StartsAt                 time.Time
	EndsAt                   time.Time
	TimeZone                 string
	IdempotencyKey           string
}

// FuneralJourney liga os tres agregados exclusivamente por IDs opacos.
type FuneralJourney struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	CompanyID    uuid.UUID
	ContractID   uuid.UUID
	ServiceOrder ServiceOrder
	Schedule     Schedule
	CreatedAt    time.Time
}

type JourneyService interface {
	CreateFuneralJourney(context.Context, FuneralJourneyCreate) (FuneralJourney, error)
}

func (r *Repository) CreateFuneralJourney(ctx context.Context, input FuneralJourneyCreate) (FuneralJourney, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return FuneralJourney{}, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return FuneralJourney{}, err
	}
	input.ServiceOrderKind = strings.TrimSpace(input.ServiceOrderKind)
	input.ServiceOrderDescription = strings.TrimSpace(input.ServiceOrderDescription)
	input.ScheduleKind = strings.TrimSpace(input.ScheduleKind)
	input.TimeZone = strings.TrimSpace(input.TimeZone)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.ServiceOrderScheduledFor != nil {
		value := input.ServiceOrderScheduledFor.UTC()
		input.ServiceOrderScheduledFor = &value
	}
	input.StartsAt = input.StartsAt.UTC()
	input.EndsAt = input.EndsAt.UTC()
	if err := validateFuneralJourney(input); err != nil {
		return FuneralJourney{}, err
	}
	if r.eventStore == nil {
		return FuneralJourney{}, ErrOutboxUnavailable
	}

	requestHash, err := funeralJourneyHash(input)
	if err != nil {
		return FuneralJourney{}, fmt.Errorf("hash da jornada: %w", err)
	}
	serviceOrderID := uuid.New()
	scheduleID := uuid.New()
	journeyID := uuid.New()
	serviceOrderKey := "funeral.journey:" + input.IdempotencyKey + ":service-order"
	scheduleKey := "funeral.journey:" + input.IdempotencyKey + ":schedule"
	serviceOrderInput := ServiceOrderCreate{
		CompanyID: companyID, ContractID: input.ContractID, Kind: input.ServiceOrderKind,
		Description: input.ServiceOrderDescription, ScheduledFor: input.ServiceOrderScheduledFor,
		IdempotencyKey: serviceOrderKey,
	}
	serviceOrderHash, err := serviceOrderCreateHash(serviceOrderInput)
	if err != nil {
		return FuneralJourney{}, fmt.Errorf("hash da ordem da jornada: %w", err)
	}
	scheduleInput := ScheduleCreate{
		CompanyID: companyID, ContractID: input.ContractID, ServiceOrderID: &serviceOrderID,
		Kind: input.ScheduleKind, LocationID: input.LocationID, ResourceID: input.ResourceID,
		StartsAt: input.StartsAt, EndsAt: input.EndsAt, TimeZone: input.TimeZone,
		IdempotencyKey: scheduleKey,
	}
	scheduleHash, err := scheduleCreateHash(scheduleInput)
	if err != nil {
		return FuneralJourney{}, fmt.Errorf("hash da agenda da jornada: %w", err)
	}

	var result FuneralJourney
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if found, err := loadJourneyByKeyTx(ctx, tx, tenantID, input.IdempotencyKey, requestHash, companyID, &result); err != nil || found {
			return err
		}

		var contractStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM funeral.contract WHERE tenant_id=$1 AND company_id=$2 AND id=$3 FOR UPDATE`, tenantID, companyID, input.ContractID).Scan(&contractStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrContractNotFound
			}
			return err
		}
		if contractStatus != ContractActive {
			return ErrJourneyContractState
		}
		// O lock do contrato serializa duas jornadas concorrentes do mesmo
		// agregado; a segunda rele a chave depois de esperar o commit.
		if found, err := loadJourneyByKeyTx(ctx, tx, tenantID, input.IdempotencyKey, requestHash, companyID, &result); err != nil || found {
			return err
		}

		var locationExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.location WHERE tenant_id=$1 AND company_id=$2 AND id=$3)`, tenantID, companyID, input.LocationID).Scan(&locationExists); err != nil {
			return err
		}
		if !locationExists {
			return ErrJourneyLocation
		}
		if input.ResourceID != nil {
			var resourceExists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM funeral.resource WHERE tenant_id=$1 AND company_id=$2 AND location_id=$3 AND id=$4 AND active)`, tenantID, companyID, input.LocationID, *input.ResourceID).Scan(&resourceExists); err != nil {
				return err
			}
			if !resourceExists {
				return ErrJourneyResource
			}
		}

		if _, err := tx.Exec(ctx, `INSERT INTO funeral.service_order
			(id,tenant_id,company_id,contract_id,kind,description,scheduled_for,idempotency_key,request_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, serviceOrderID, tenantID, companyID,
			input.ContractID, input.ServiceOrderKind, input.ServiceOrderDescription,
			input.ServiceOrderScheduledFor, serviceOrderKey, serviceOrderHash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.service_order_status_history (tenant_id,service_order_id,to_status) VALUES ($1,$2,$3)`, tenantID, serviceOrderID, ServiceOrderOpen); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.schedule
			(id,tenant_id,company_id,contract_id,service_order_id,kind,location_id,resource_id,starts_at,ends_at,time_zone,idempotency_key,request_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, scheduleID, tenantID, companyID,
			input.ContractID, serviceOrderID, input.ScheduleKind, input.LocationID, input.ResourceID,
			input.StartsAt, input.EndsAt, input.TimeZone, scheduleKey, scheduleHash); err != nil {
			if isScheduleConflict(err) {
				return ErrScheduleConflict
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.schedule_status_history (tenant_id,schedule_id,to_status) VALUES ($1,$2,$3)`, tenantID, scheduleID, ScheduleScheduled); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.journey_mutation
			(id,tenant_id,contract_id,service_order_id,schedule_id,idempotency_key,request_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (tenant_id,idempotency_key) DO NOTHING`, journeyID, tenantID,
			input.ContractID, serviceOrderID, scheduleID, input.IdempotencyKey, requestHash); err != nil {
			return err
		}
		var storedID, storedOrderID, storedScheduleID uuid.UUID
		var storedHash string
		if err := tx.QueryRow(ctx, `SELECT id,service_order_id,schedule_id,request_hash FROM funeral.journey_mutation WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&storedID, &storedOrderID, &storedScheduleID, &storedHash); err != nil {
			return err
		}
		if storedHash != requestHash {
			return ErrJourneyIdempotency
		}
		journeyID, serviceOrderID, scheduleID = storedID, storedOrderID, storedScheduleID

		servicePayloadMap := map[string]any{"service_order_id": serviceOrderID.String(), "contract_id": input.ContractID.String(), "company_id": companyID.String(), "status": ServiceOrderOpen}
		if input.ServiceOrderScheduledFor != nil {
			servicePayloadMap["scheduled_for"] = input.ServiceOrderScheduledFor.Format(time.RFC3339Nano)
		}
		servicePayload, err := json.Marshal(servicePayloadMap)
		if err != nil {
			return err
		}
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{TenantID: tenantID, AggregateType: "funeral.service_order", AggregateID: serviceOrderID, EventType: "funeral.service_order.created", Version: 1, Payload: servicePayload, IdempotencyKey: "funeral.service_order.create:" + serviceOrderHash[:32]}); err != nil {
			return err
		}
		schedulePayload, err := json.Marshal(map[string]any{"schedule_id": scheduleID.String(), "contract_id": input.ContractID.String(), "service_order_id": serviceOrderID.String(), "company_id": companyID.String(), "kind": input.ScheduleKind, "location_id": input.LocationID.String(), "resource_id": optionalUUIDText(input.ResourceID), "starts_at": input.StartsAt.Format(time.RFC3339Nano), "ends_at": input.EndsAt.Format(time.RFC3339Nano), "time_zone": input.TimeZone, "status": ScheduleScheduled})
		if err != nil {
			return err
		}
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{TenantID: tenantID, AggregateType: "funeral.schedule", AggregateID: scheduleID, EventType: "funeral.schedule.created", Version: 1, Payload: schedulePayload, IdempotencyKey: "funeral.schedule.create:" + scheduleHash[:32]}); err != nil {
			return err
		}
		journeyPayload, err := json.Marshal(map[string]any{"journey_id": journeyID.String(), "contract_id": input.ContractID.String(), "service_order_id": serviceOrderID.String(), "schedule_id": scheduleID.String(), "company_id": companyID.String()})
		if err != nil {
			return err
		}
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{TenantID: tenantID, AggregateType: "funeral.journey", AggregateID: journeyID, EventType: "funeral.journey.created", Version: 1, Payload: journeyPayload, IdempotencyKey: "funeral.journey.create:" + requestHash[:32]}); err != nil {
			return err
		}
		return loadJourneyTx(ctx, tx, tenantID, companyID, journeyID, &result)
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrJourneyIdempotency), errors.Is(err, ErrJourneyInvalid), errors.Is(err, ErrJourneyContractState), errors.Is(err, ErrJourneyServiceOrder), errors.Is(err, ErrJourneyLocation), errors.Is(err, ErrJourneyResource), errors.Is(err, ErrContractNotFound), errors.Is(err, ErrScheduleConflict):
			return FuneralJourney{}, err
		default:
			return FuneralJourney{}, fmt.Errorf("criar jornada funeraria: %w", err)
		}
	}
	return result, nil
}

func validateFuneralJourney(input FuneralJourneyCreate) error {
	if input.ContractID == uuid.Nil || input.LocationID == uuid.Nil || input.ResourceID != nil && *input.ResourceID == uuid.Nil || input.IdempotencyKey == "" || len([]rune(input.IdempotencyKey)) > 160 {
		return ErrJourneyInvalid
	}
	if input.ServiceOrderKind == "" || len([]rune(input.ServiceOrderKind)) > 100 || len([]rune(input.ServiceOrderDescription)) > 2000 {
		return ErrJourneyInvalid
	}
	if input.ScheduleKind != ScheduleKindWake && input.ScheduleKind != ScheduleKindBurial {
		return ErrJourneyInvalid
	}
	if err := validateScheduleInterval(input.StartsAt, input.EndsAt, input.TimeZone); err != nil {
		return ErrJourneyInvalid
	}
	return nil
}

func funeralJourneyHash(input FuneralJourneyCreate) (string, error) {
	return hashJSON(struct {
		ContractID                                uuid.UUID
		ServiceOrderKind, ServiceOrderDescription string
		ServiceOrderScheduledFor                  string
		ScheduleKind                              string
		LocationID                                uuid.UUID
		ResourceID                                *uuid.UUID
		StartsAt, EndsAt                          string
		TimeZone                                  string
	}{input.ContractID, input.ServiceOrderKind, input.ServiceOrderDescription, formatTime(input.ServiceOrderScheduledFor), input.ScheduleKind, input.LocationID, input.ResourceID, input.StartsAt.Format(time.RFC3339Nano), input.EndsAt.Format(time.RFC3339Nano), input.TimeZone})
}

func loadJourneyByKeyTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key, requestHash string, companyID uuid.UUID, result *FuneralJourney) (bool, error) {
	var journeyID, contractID, serviceOrderID, scheduleID uuid.UUID
	var storedHash string
	err := tx.QueryRow(ctx, `SELECT id,contract_id,service_order_id,schedule_id,request_hash,created_at FROM funeral.journey_mutation WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, key).Scan(&journeyID, &contractID, &serviceOrderID, &scheduleID, &storedHash, &result.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if storedHash != requestHash {
		return true, ErrJourneyIdempotency
	}
	result.ID, result.TenantID, result.CompanyID, result.ContractID = journeyID, tenantID, companyID, contractID
	if err := loadServiceOrderTx(ctx, tx, tenantID, companyID, serviceOrderID, &result.ServiceOrder); err != nil {
		return true, err
	}
	if err := loadScheduleTx(ctx, tx, tenantID, companyID, scheduleID, &result.Schedule); err != nil {
		return true, err
	}
	return true, nil
}

func loadJourneyTx(ctx context.Context, tx pgx.Tx, tenantID, companyID, journeyID uuid.UUID, result *FuneralJourney) error {
	var contractID, serviceOrderID, scheduleID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT contract_id,service_order_id,schedule_id,created_at FROM funeral.journey_mutation WHERE tenant_id=$1 AND id=$2`, tenantID, journeyID).Scan(&contractID, &serviceOrderID, &scheduleID, &result.CreatedAt); err != nil {
		return err
	}
	result.ID, result.TenantID, result.CompanyID, result.ContractID = journeyID, tenantID, companyID, contractID
	if err := loadServiceOrderTx(ctx, tx, tenantID, companyID, serviceOrderID, &result.ServiceOrder); err != nil {
		return err
	}
	return loadScheduleTx(ctx, tx, tenantID, companyID, scheduleID, &result.Schedule)
}

var _ JourneyService = (*Repository)(nil)
