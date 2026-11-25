package funeral

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrContractNotFound        = errors.New("contrato funerario nao encontrado")
	ErrCustomerNotFound        = errors.New("cliente do contrato nao encontrado")
	ErrDeceasedNotFound        = errors.New("falecido do contrato nao encontrado")
	ErrFamilyLinkNotFound      = errors.New("familiar do contrato nao encontrado")
	ErrCatalogItemNotFound     = errors.New("item do contrato nao encontrado")
	ErrInvalidContract         = errors.New("contrato funerario invalido")
	ErrInvalidContractStatus   = errors.New("status de contrato funerario invalido")
	ErrInvalidStatusTransition = errors.New("transicao de status de contrato funerario invalida")
	ErrIdempotencyConflict     = errors.New("chave de idempotencia reutilizada com conteudo diferente")
	ErrOutboxUnavailable       = errors.New("outbox indisponivel")
	ErrTenantMismatch          = errors.New("tenant da entidade diverge do contexto")
)

const (
	ContractDraft     = "draft"
	ContractActive    = "active"
	ContractCompleted = "completed"
	ContractCancelled = "cancelled"
)

// ContractItemCreate e um snapshot de preco do item de catalogo no contrato.
// Valores monetarios trafegam em centavos e nunca como float.
type ContractItemCreate struct {
	CatalogItemID   uuid.UUID `json:"catalog_item_id"`
	Quantity        int       `json:"quantity"`
	UnitAmountCents int64     `json:"unit_amount_cents"`
}

type ContractCreate struct {
	CompanyID       uuid.UUID
	CustomerPartyID uuid.UUID            `json:"customer_party_id"`
	DeceasedID      uuid.UUID            `json:"deceased_id"`
	FamilyLinkIDs   []uuid.UUID          `json:"family_link_ids"`
	Items           []ContractItemCreate `json:"items"`
	IdempotencyKey  string
}

type ContractItem struct {
	ID, TenantID, ContractID, CatalogItemID uuid.UUID
	ItemKind, Description, Unit             string
	Quantity                                int
	UnitAmountCents, TotalAmountCents       int64
}

type ContractStatusChange struct {
	ID, TenantID, ContractID uuid.UUID
	FromStatus, ToStatus     string
	Reason                   *string
	ChangedAt                time.Time
}

type Contract struct {
	ID, TenantID, CompanyID, CustomerPartyID, DeceasedID uuid.UUID
	Status                                               string
	TotalAmountCents                                     int64
	IdempotencyKey                                       string
	CreatedAt, UpdatedAt                                 time.Time
	Items                                                []ContractItem
	FamilyLinkIDs                                        []uuid.UUID
	StatusHistory                                        []ContractStatusChange
}

// ContractService e o contrato publico consumido pela camada HTTP.
type ContractService interface {
	CreateContract(context.Context, ContractCreate) (Contract, error)
	GetContractByID(context.Context, uuid.UUID) (Contract, error)
	ChangeContractStatus(context.Context, uuid.UUID, string, *string) (Contract, error)
}

// EventAppender permite manter agregado e outbox na mesma transacao.
type EventAppender interface {
	AppendTx(context.Context, pgx.Tx, outbox.Event) (outbox.Event, error)
}

type Repository struct {
	database   *db.DB
	eventStore EventAppender
}

func NewRepository(database *db.DB, eventStores ...EventAppender) (*Repository, error) {
	if database == nil {
		return nil, errors.New("banco funerario nulo")
	}
	var eventStore EventAppender
	if len(eventStores) > 0 {
		eventStore = eventStores[0]
	}
	return &Repository{database: database, eventStore: eventStore}, nil
}

func (r *Repository) CreateContract(ctx context.Context, input ContractCreate) (Contract, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Contract{}, err
	}
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return Contract{}, err
	}
	if input.CompanyID != uuid.Nil && input.CompanyID != companyID {
		return Contract{}, ErrTenantMismatch
	}
	input.CompanyID = companyID
	if err := validateCreate(input); err != nil {
		return Contract{}, err
	}
	if r.eventStore == nil {
		return Contract{}, ErrOutboxUnavailable
	}
	requestHash, err := contractRequestHash(input)
	if err != nil {
		return Contract{}, fmt.Errorf("hash do contrato: %w", err)
	}

	var result Contract
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Replay idempotente deve devolver o agregado mesmo que uma referencia
		// externa tenha sido desativada depois da criacao original.
		var existingID uuid.UUID
		var storedHash string
		existingErr := tx.QueryRow(ctx, `SELECT id, request_hash FROM funeral.contract WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&existingID, &storedHash)
		if existingErr == nil {
			if storedHash != requestHash {
				return ErrIdempotencyConflict
			}
			return loadContractTx(ctx, tx, tenantID, existingID, &result)
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}
		if err := validateReferences(ctx, tx, tenantID, input); err != nil {
			return err
		}
		totalAmount, err := centsToNumeric(totalCents(input.Items))
		if err != nil {
			return err
		}
		var insertedID uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO funeral.contract
				(tenant_id, company_id, customer_party_id, deceased_id, total_amount, idempotency_key, request_hash)
			VALUES ($1,$2,$3,$4,$5::numeric,$6,$7)
			ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
			RETURNING id`, tenantID, companyID, input.CustomerPartyID, input.DeceasedID,
			totalAmount, input.IdempotencyKey, requestHash).Scan(&insertedID)
		if errors.Is(err, pgx.ErrNoRows) {
			var storedHash string
			if err := tx.QueryRow(ctx, `SELECT id, request_hash FROM funeral.contract WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).
				Scan(&result.ID, &storedHash); err != nil {
				return err
			}
			if storedHash != requestHash {
				return ErrIdempotencyConflict
			}
			return loadContractTx(ctx, tx, tenantID, result.ID, &result)
		}
		if err != nil {
			return err
		}
		result = Contract{
			ID: insertedID, TenantID: tenantID, CompanyID: companyID,
			CustomerPartyID: input.CustomerPartyID, DeceasedID: input.DeceasedID,
			Status: ContractDraft, TotalAmountCents: totalCents(input.Items),
			IdempotencyKey: input.IdempotencyKey,
		}
		for _, item := range input.Items {
			var kind, name, unit string
			if err := tx.QueryRow(ctx, `SELECT kind, name, unit FROM catalog.catalog_item WHERE tenant_id=$1 AND id=$2 AND active`, tenantID, item.CatalogItemID).
				Scan(&kind, &name, &unit); err != nil {
				return err
			}
			unitAmount, err := centsToNumeric(item.UnitAmountCents)
			if err != nil {
				return err
			}
			itemAmount, err := centsToNumeric(itemTotal(item))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO funeral.contract_item
				(tenant_id,contract_id,catalog_item_id,item_kind,description,unit,quantity,unit_amount,total_amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8::numeric,$9::numeric)`, tenantID, insertedID,
				item.CatalogItemID, kind, name, unit, item.Quantity, unitAmount, itemAmount); err != nil {
				return err
			}
		}
		for _, familyLinkID := range input.FamilyLinkIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO funeral.contract_family_member (tenant_id,contract_id,family_link_id) VALUES ($1,$2,$3)`, tenantID, insertedID, familyLinkID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.contract_status_history (tenant_id,contract_id,to_status) VALUES ($1,$2,$3)`, tenantID, insertedID, ContractDraft); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"contract_id": insertedID.String(), "status": ContractDraft,
			"customer_party_id": input.CustomerPartyID.String(), "deceased_id": input.DeceasedID.String(),
			"total_amount_cents": totalCents(input.Items),
		})
		if err != nil {
			return err
		}
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{
			TenantID: tenantID, AggregateType: "funeral.contract", AggregateID: insertedID,
			EventType: "funeral.contract.created", Version: 1, Payload: payload,
			IdempotencyKey: "funeral.contract.create:" + input.IdempotencyKey,
		}); err != nil {
			return err
		}
		return loadContractTx(ctx, tx, tenantID, insertedID, &result)
	})
	if errors.Is(err, ErrCustomerNotFound) || errors.Is(err, ErrDeceasedNotFound) || errors.Is(err, ErrFamilyLinkNotFound) || errors.Is(err, ErrCatalogItemNotFound) || errors.Is(err, ErrIdempotencyConflict) {
		return Contract{}, err
	}
	if err != nil {
		return Contract{}, fmt.Errorf("criar contrato funerario: %w", err)
	}
	return result, nil
}

func (r *Repository) GetContractByID(ctx context.Context, id uuid.UUID) (Contract, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Contract{}, err
	}
	if _, err := db.CompanyFromContext(ctx); err != nil {
		return Contract{}, err
	}
	if id == uuid.Nil {
		return Contract{}, ErrContractNotFound
	}
	var result Contract
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return loadContractTx(ctx, tx, tenantID, id, &result)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Contract{}, ErrContractNotFound
	}
	if err != nil {
		return Contract{}, fmt.Errorf("buscar contrato funerario: %w", err)
	}
	return result, nil
}

func (r *Repository) ChangeContractStatus(ctx context.Context, id uuid.UUID, nextStatus string, reason *string) (Contract, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Contract{}, err
	}
	if _, err := db.CompanyFromContext(ctx); err != nil {
		return Contract{}, err
	}
	nextStatus = strings.TrimSpace(nextStatus)
	if !validStatus(nextStatus) {
		return Contract{}, ErrInvalidContractStatus
	}
	reason = normalizeText(reason)
	var result Contract
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		companyID, err := db.CompanyFromContext(ctx)
		if err != nil {
			return err
		}
		var current, hash string
		if err := tx.QueryRow(ctx, `SELECT status, idempotency_key FROM funeral.contract WHERE tenant_id=$1 AND company_id=$2 AND id=$3 FOR UPDATE`, tenantID, companyID, id).Scan(&current, &hash); err != nil {
			return err
		}
		if current == nextStatus {
			return loadContractTx(ctx, tx, tenantID, id, &result)
		}
		if !validTransition(current, nextStatus) {
			return ErrInvalidStatusTransition
		}
		if _, err := tx.Exec(ctx, `UPDATE funeral.contract SET status=$1, updated_at=now() WHERE tenant_id=$2 AND company_id=$3 AND id=$4`, nextStatus, tenantID, companyID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO funeral.contract_status_history (tenant_id,contract_id,from_status,to_status,reason) VALUES ($1,$2,$3,$4,$5)`, tenantID, id, current, nextStatus, reason); err != nil {
			return err
		}
		if r.eventStore == nil {
			return ErrOutboxUnavailable
		}
		payload, err := json.Marshal(map[string]any{"contract_id": id.String(), "from_status": current, "to_status": nextStatus})
		if err != nil {
			return err
		}
		key := "funeral.contract.status:" + id.String() + ":" + nextStatus
		if _, err := r.eventStore.AppendTx(ctx, tx, outbox.Event{TenantID: tenantID, AggregateType: "funeral.contract", AggregateID: id, EventType: "funeral.contract.status_changed", Version: 1, Payload: payload, IdempotencyKey: key}); err != nil {
			return err
		}
		return loadContractTx(ctx, tx, tenantID, id, &result)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Contract{}, ErrContractNotFound
	case errors.Is(err, ErrInvalidStatusTransition), errors.Is(err, ErrInvalidContractStatus), errors.Is(err, ErrOutboxUnavailable):
		return Contract{}, err
	case err != nil:
		return Contract{}, fmt.Errorf("alterar status do contrato funerario: %w", err)
	}
	return result, nil
}

func validateCreate(input ContractCreate) error {
	key := strings.TrimSpace(input.IdempotencyKey)
	if input.CompanyID == uuid.Nil || input.CustomerPartyID == uuid.Nil || input.DeceasedID == uuid.Nil || key == "" || len([]rune(key)) > 160 || len(input.Items) == 0 {
		return ErrInvalidContract
	}
	seenFamily := make(map[uuid.UUID]struct{}, len(input.FamilyLinkIDs))
	for _, id := range input.FamilyLinkIDs {
		if id == uuid.Nil {
			return ErrInvalidContract
		}
		if _, ok := seenFamily[id]; ok {
			return ErrInvalidContract
		}
		seenFamily[id] = struct{}{}
	}
	seenItems := make(map[uuid.UUID]struct{}, len(input.Items))
	for _, item := range input.Items {
		if item.CatalogItemID == uuid.Nil || item.Quantity <= 0 || item.UnitAmountCents < 0 {
			return ErrInvalidContract
		}
		if _, ok := seenItems[item.CatalogItemID]; ok {
			return ErrInvalidContract
		}
		seenItems[item.CatalogItemID] = struct{}{}
		if _, err := centsToNumeric(itemTotal(item)); err != nil {
			return ErrInvalidContract
		}
	}
	if _, err := centsToNumeric(totalCents(input.Items)); err != nil {
		return ErrInvalidContract
	}
	return nil
}

func validateReferences(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input ContractCreate) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM party.party WHERE tenant_id=$1 AND id=$2)`, tenantID, input.CustomerPartyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrCustomerNotFound
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM party.deceased WHERE tenant_id=$1 AND id=$2)`, tenantID, input.DeceasedID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrDeceasedNotFound
	}
	for _, id := range input.FamilyLinkIDs {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM party.family_link WHERE tenant_id=$1 AND id=$2 AND deceased_id=$3)`, tenantID, id, input.DeceasedID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrFamilyLinkNotFound
		}
	}
	for _, item := range input.Items {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM catalog.catalog_item WHERE tenant_id=$1 AND id=$2 AND active)`, tenantID, item.CatalogItemID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrCatalogItemNotFound
		}
	}
	return nil
}

func loadContractTx(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, result *Contract) error {
	companyID, err := db.CompanyFromContext(ctx)
	if err != nil {
		return err
	}
	var amount string
	if err = tx.QueryRow(ctx, `SELECT id,tenant_id,company_id,customer_party_id,deceased_id,status,total_amount::text,idempotency_key,created_at,updated_at FROM funeral.contract WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenantID, companyID, id).
		Scan(&result.ID, &result.TenantID, &result.CompanyID, &result.CustomerPartyID, &result.DeceasedID, &result.Status, &amount, &result.IdempotencyKey, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return err
	}
	result.TotalAmountCents, err = numericToCents(amount)
	if err != nil {
		return err
	}
	result.Items = nil
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,contract_id,catalog_item_id,item_kind,description,unit,quantity,unit_amount::text,total_amount::text FROM funeral.contract_item WHERE tenant_id=$1 AND contract_id=$2 ORDER BY id`, tenantID, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item ContractItem
		var unitAmount, totalAmount string
		if err := rows.Scan(&item.ID, &item.TenantID, &item.ContractID, &item.CatalogItemID, &item.ItemKind, &item.Description, &item.Unit, &item.Quantity, &unitAmount, &totalAmount); err != nil {
			return err
		}
		item.UnitAmountCents, err = numericToCents(unitAmount)
		if err != nil {
			return err
		}
		item.TotalAmountCents, err = numericToCents(totalAmount)
		if err != nil {
			return err
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	result.FamilyLinkIDs = nil
	rows, err = tx.Query(ctx, `SELECT family_link_id FROM funeral.contract_family_member WHERE tenant_id=$1 AND contract_id=$2 ORDER BY id`, tenantID, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var familyLinkID uuid.UUID
		if err := rows.Scan(&familyLinkID); err != nil {
			return err
		}
		result.FamilyLinkIDs = append(result.FamilyLinkIDs, familyLinkID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	result.StatusHistory = nil
	rows, err = tx.Query(ctx, `SELECT id,tenant_id,contract_id,COALESCE(from_status,''),to_status,reason,changed_at FROM funeral.contract_status_history WHERE tenant_id=$1 AND contract_id=$2 ORDER BY changed_at,id`, tenantID, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var change ContractStatusChange
		var reason pgtype.Text
		if err := rows.Scan(&change.ID, &change.TenantID, &change.ContractID, &change.FromStatus, &change.ToStatus, &reason, &change.ChangedAt); err != nil {
			return err
		}
		if reason.Valid {
			change.Reason = normalizeText(&reason.String)
		}
		result.StatusHistory = append(result.StatusHistory, change)
	}
	return rows.Err()
}

func contractRequestHash(input ContractCreate) (string, error) {
	type hashInput struct {
		CompanyID       uuid.UUID            `json:"company_id"`
		CustomerPartyID uuid.UUID            `json:"customer_party_id"`
		DeceasedID      uuid.UUID            `json:"deceased_id"`
		FamilyLinkIDs   []uuid.UUID          `json:"family_link_ids"`
		Items           []ContractItemCreate `json:"items"`
	}
	b, err := json.Marshal(hashInput{input.CompanyID, input.CustomerPartyID, input.DeceasedID, input.FamilyLinkIDs, input.Items})
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func validStatus(status string) bool {
	return status == ContractDraft || status == ContractActive || status == ContractCompleted || status == ContractCancelled
}

func validTransition(current, next string) bool {
	return (current == ContractDraft && (next == ContractActive || next == ContractCancelled)) || (current == ContractActive && (next == ContractCompleted || next == ContractCancelled))
}

func itemTotal(item ContractItemCreate) int64 {
	if item.Quantity <= 0 || item.UnitAmountCents < 0 || item.UnitAmountCents != 0 && int64(item.Quantity) > math.MaxInt64/item.UnitAmountCents {
		return math.MaxInt64
	}
	return int64(item.Quantity) * item.UnitAmountCents
}

func totalCents(items []ContractItemCreate) int64 {
	var total int64
	for _, item := range items {
		value := itemTotal(item)
		if value > math.MaxInt64-total {
			return math.MaxInt64
		}
		total += value
	}
	return total
}

func centsToNumeric(cents int64) (string, error) {
	if cents < 0 {
		return "", ErrInvalidContract
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100), nil
}

func numericToCents(value string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" || strings.HasPrefix(parts[0], "-") {
		return 0, ErrInvalidContract
	}
	frac := "00"
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) > 2 {
			return 0, ErrInvalidContract
		}
	}
	frac += strings.Repeat("0", 2-len(frac))
	var whole, fraction int64
	if _, err := fmt.Sscan(parts[0], &whole); err != nil || whole < 0 {
		return 0, ErrInvalidContract
	}
	if _, err := fmt.Sscan(frac, &fraction); err != nil || fraction < 0 || whole > (math.MaxInt64-fraction)/100 {
		return 0, ErrInvalidContract
	}
	return whole*100 + fraction, nil
}

func normalizeText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
