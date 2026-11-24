// Package outbox implementa a outbox transacional: registro idempotente do
// evento, claim com lease por tenant, retry persistido e trilha append-only de
// tentativas. Não depende de broker; o RabbitMQ, quando existir, consome esta
// tabela.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrNotFound indica evento ausente para o tenant do contexto.
	ErrNotFound = errors.New("evento de outbox não encontrado")
	// ErrInvalidEvent indica evento rejeitado na validação local.
	ErrInvalidEvent = errors.New("evento de outbox inválido")
	// ErrIdempotencyConflict indica chave reutilizada com outro conteúdo.
	ErrIdempotencyConflict = errors.New("idempotency_key reutilizada com conteúdo diferente")
	// ErrTenantMismatch indica entidade de outro tenant no contexto.
	ErrTenantMismatch = errors.New("tenant da entidade diverge do contexto")
	// ErrStaleEvent indica evento fora de processamento para a transição pedida.
	ErrStaleEvent = errors.New("evento não está em processamento")
)

const (
	// DefaultMaxAttempts é o teto padrão de tentativas de entrega.
	DefaultMaxAttempts = 10
	// maxErrorRunes limita o last_error persistido.
	maxErrorRunes = 1000
	// backoffBase e backoffMax definem o crescimento do retry.
	backoffBase = 2 * time.Second
	backoffMax  = time.Hour
)

var eventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Event é o evento de domínio persistido na outbox.
type Event struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	AggregateType  string
	AggregateID    uuid.UUID
	EventType      string
	Version        int
	Payload        json.RawMessage
	IdempotencyKey string
	Status         string
	Attempts       int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LastError      *string
	CreatedAt      time.Time
	ProcessedAt    *time.Time
}

// Repository acessa a outbox com tenant obrigatório, exceto PendingTenants.
type Repository struct{ database *db.DB }

// NewRepository valida a dependência e devolve o repositório.
func NewRepository(database *db.DB) (*Repository, error) {
	if database == nil {
		return nil, errors.New("banco de outbox nulo")
	}
	return &Repository{database: database}, nil
}

const eventColumns = `id,tenant_id,aggregate_type,aggregate_id,event_type,event_version,payload,
	idempotency_key,status,attempts,max_attempts,next_attempt_at,last_error,created_at,processed_at`

// Append registra o evento na outbox. É idempotente por (tenant, chave): um
// replay devolve o evento original e nunca duplica a linha. Reutilizar a chave
// com aggregate, tipo, versão ou payload diferente falha com
// ErrIdempotencyConflict.
func (r *Repository) Append(ctx context.Context, event Event) (Event, error) {
	tenant, err := tenantFor(ctx, event.TenantID)
	if err != nil {
		return Event{}, err
	}
	event.TenantID = tenant
	if event.MaxAttempts == 0 {
		event.MaxAttempts = DefaultMaxAttempts
	}
	if err := validateEvent(event); err != nil {
		return Event{}, err
	}
	var result Event
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var nextAttempt any
		if !event.NextAttemptAt.IsZero() {
			nextAttempt = event.NextAttemptAt.UTC()
		}
		row := tx.QueryRow(ctx, `
INSERT INTO outbox.event
	(tenant_id,aggregate_type,aggregate_id,event_type,event_version,payload,idempotency_key,max_attempts,next_attempt_at)
VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,COALESCE($9,now()))
ON CONFLICT (tenant_id,idempotency_key) DO NOTHING
RETURNING `+eventColumns,
			tenant, event.AggregateType, event.AggregateID, event.EventType, event.Version,
			[]byte(event.Payload), event.IdempotencyKey, event.MaxAttempts, nextAttempt)
		if err := scanEvent(row, &result); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var same bool
		err := tx.QueryRow(ctx, `
SELECT (aggregate_type=$2 AND aggregate_id=$3 AND event_type=$4 AND event_version=$5 AND payload=$6::jsonb)
FROM outbox.event WHERE tenant_id=$1 AND idempotency_key=$7`,
			tenant, event.AggregateType, event.AggregateID, event.EventType, event.Version,
			[]byte(event.Payload), event.IdempotencyKey).Scan(&same)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !same {
			return ErrIdempotencyConflict
		}
		return scanEvent(tx.QueryRow(ctx, `SELECT `+eventColumns+` FROM outbox.event WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, event.IdempotencyKey), &result)
	})
	if err != nil {
		return Event{}, fmt.Errorf("registrar evento na outbox: %w", err)
	}
	return result, nil
}

// GetByID busca o evento do tenant do contexto.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (Event, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return Event{}, err
	}
	return r.getOne(ctx, tenant, `id=$2`, id)
}

// GetByIdempotencyKey busca o evento do tenant pela chave de idempotência.
func (r *Repository) GetByIdempotencyKey(ctx context.Context, key string) (Event, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return Event{}, err
	}
	return r.getOne(ctx, tenant, `idempotency_key=$2`, key)
}

func (r *Repository) getOne(ctx context.Context, tenant uuid.UUID, predicate string, arg any) (Event, error) {
	var result Event
	err := r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return scanEvent(tx.QueryRow(ctx, `SELECT `+eventColumns+` FROM outbox.event WHERE tenant_id=$1 AND `+predicate, tenant, arg), &result)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("consultar evento da outbox: %w", err)
	}
	return result, nil
}

// Claim reivindica até limit eventos elegíveis do tenant do contexto e registra
// a trilha "claimed". O lease vai em next_attempt_at: um worker que morre deixa
// o evento reivindicável de novo depois do prazo. Cada claim incrementa
// attempts em uma unidade.
func (r *Repository) Claim(ctx context.Context, limit int, lease time.Duration) ([]Event, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("%w: limite de claim inválido", ErrInvalidEvent)
	}
	if lease <= 0 {
		return nil, fmt.Errorf("%w: lease de claim inválido", ErrInvalidEvent)
	}
	events := []Event{}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
WITH claimable AS (
	SELECT id FROM outbox.event
	WHERE tenant_id=$1
		AND status IN ('pending','processing')
		AND next_attempt_at <= now()
		AND attempts < max_attempts
	ORDER BY next_attempt_at, created_at, id
	FOR UPDATE SKIP LOCKED
	LIMIT $2
), claimed AS (
	UPDATE outbox.event e
	SET status='processing',
		attempts=e.attempts+1,
		next_attempt_at=now()+make_interval(secs => $3::double precision)
	FROM claimable c
	WHERE e.id=c.id AND e.tenant_id=$1
	RETURNING `+prefixed("e", eventColumns)+`
)
SELECT `+eventColumns+` FROM claimed ORDER BY id`, tenant, limit, lease.Seconds())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Event
			if err := scanEvent(rows, &item); err != nil {
				return err
			}
			events = append(events, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(events))
		for _, item := range events {
			ids = append(ids, item.ID)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO outbox.event_attempt (tenant_id,event_id,attempt,outcome)
SELECT tenant_id, id, attempts, 'claimed' FROM outbox.event
WHERE tenant_id=$1 AND id = ANY($2::uuid[])
ON CONFLICT (tenant_id,event_id,attempt,outcome) DO NOTHING`, tenant, ids)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("reivindicar eventos da outbox: %w", err)
	}
	return events, nil
}

// Succeed fecha o evento como processado e registra a trilha "succeeded".
func (r *Repository) Succeed(ctx context.Context, id uuid.UUID) (Event, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return Event{}, err
	}
	var result Event
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := scanEvent(tx.QueryRow(ctx, `
UPDATE outbox.event
SET status='processed', processed_at=now(), last_error=NULL
WHERE tenant_id=$1 AND id=$2 AND status='processing'
RETURNING `+eventColumns, tenant, id), &result)
		if errors.Is(err, pgx.ErrNoRows) {
			return classifyMiss(ctx, tx, tenant, id)
		}
		if err != nil {
			return err
		}
		return auditAttempt(ctx, tx, tenant, result.ID, result.Attempts, "succeeded", nil)
	})
	if err != nil {
		return Event{}, fmt.Errorf("concluir evento da outbox: %w", err)
	}
	return result, nil
}

// Fail registra a falha, aplica backoff e devolve o evento para a fila. Quando
// attempts atingiu max_attempts, o evento vira failed terminal. A trilha
// registra "retry" ou "failed".
func (r *Repository) Fail(ctx context.Context, id uuid.UUID, cause error, retryAfter time.Duration) (Event, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return Event{}, err
	}
	if retryAfter < 0 {
		retryAfter = 0
	}
	text := errorText(cause)
	var result Event
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var attempts, maxAttempts int
		err := tx.QueryRow(ctx, `SELECT status, attempts, max_attempts FROM outbox.event WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&status, &attempts, &maxAttempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "processing" {
			return ErrStaleEvent
		}
		outcome, next := "retry", retryAfter
		nextStatus := "pending"
		if attempts >= maxAttempts {
			outcome, nextStatus, next = "failed", "failed", 0
		}
		if err := scanEvent(tx.QueryRow(ctx, `
UPDATE outbox.event
SET status=$3, next_attempt_at=now()+make_interval(secs => $4::double precision), last_error=$5
WHERE tenant_id=$1 AND id=$2 AND status='processing'
RETURNING `+eventColumns, tenant, id, nextStatus, next.Seconds(), text), &result); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrStaleEvent
			}
			return err
		}
		return auditAttempt(ctx, tx, tenant, result.ID, result.Attempts, outcome, &text)
	})
	if err != nil {
		return Event{}, fmt.Errorf("registrar falha do evento da outbox: %w", err)
	}
	return result, nil
}

// ReapExpired fecha leases vencidos que já esgotaram as tentativas. É a rede de
// segurança para worker que morreu depois do último claim permitido.
func (r *Repository) ReapExpired(ctx context.Context) (int, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return 0, err
	}
	var reaped int
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
WITH expired AS (
	UPDATE outbox.event
	SET status='failed', next_attempt_at=now(), last_error=COALESCE(last_error, 'lease expirou sem conclusao')
	WHERE tenant_id=$1
		AND status='processing'
		AND next_attempt_at <= now()
		AND attempts >= max_attempts
	RETURNING id, attempts
), audit AS (
	INSERT INTO outbox.event_attempt (tenant_id,event_id,attempt,outcome,error)
	SELECT $1, id, attempts, 'failed', 'lease expirou sem conclusao' FROM expired
	ON CONFLICT (tenant_id,event_id,attempt,outcome) DO NOTHING
)
SELECT count(*) FROM expired`, tenant).Scan(&reaped)
	})
	if err != nil {
		return 0, fmt.Errorf("expirar leases da outbox: %w", err)
	}
	return reaped, nil
}

// PendingTenants lista tenant_id com evento elegível. É a única operação
// cross-tenant: a função do banco devolve só identificadores, e o claim real
// continua sob RLS com WithTenantTx.
func (r *Repository) PendingTenants(ctx context.Context, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 1000 {
		return nil, fmt.Errorf("%w: limite de tenants inválido", ErrInvalidEvent)
	}
	tenants := []uuid.UUID{}
	err := r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id FROM outbox.pending_tenants($1)`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tenant uuid.UUID
			if err := rows.Scan(&tenant); err != nil {
				return err
			}
			tenants = append(tenants, tenant)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar tenants com outbox pendente: %w", err)
	}
	return tenants, nil
}

// Backoff devolve o atraso exponencial do retry: 2s na primeira falha até 1h.
func Backoff(attempts int) time.Duration {
	if attempts <= 1 {
		return backoffBase
	}
	shift := attempts - 1
	if shift > 20 {
		return backoffMax
	}
	delay := backoffBase << shift
	if delay <= 0 || delay > backoffMax {
		return backoffMax
	}
	return delay
}

func classifyMiss(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT true FROM outbox.event WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrStaleEvent
}

func auditAttempt(ctx context.Context, tx pgx.Tx, tenant, event uuid.UUID, attempt int, outcome string, cause *string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO outbox.event_attempt (tenant_id,event_id,attempt,outcome,error)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tenant_id,event_id,attempt,outcome) DO NOTHING`, tenant, event, attempt, outcome, cause)
	return err
}

type rowScanner interface{ Scan(...any) error }

func scanEvent(row rowScanner, event *Event) error {
	var payload []byte
	if err := row.Scan(&event.ID, &event.TenantID, &event.AggregateType, &event.AggregateID,
		&event.EventType, &event.Version, &payload, &event.IdempotencyKey, &event.Status,
		&event.Attempts, &event.MaxAttempts, &event.NextAttemptAt, &event.LastError,
		&event.CreatedAt, &event.ProcessedAt); err != nil {
		return err
	}
	event.Payload = json.RawMessage(payload)
	return nil
}

func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = alias + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ",")
}

func tenantFor(ctx context.Context, entity uuid.UUID) (uuid.UUID, error) {
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if entity != uuid.Nil && entity != tenant {
		return uuid.Nil, ErrTenantMismatch
	}
	return tenant, nil
}

func validateEvent(event Event) error {
	if strings.TrimSpace(event.AggregateType) == "" || event.AggregateID == uuid.Nil {
		return fmt.Errorf("%w: aggregate_type/aggregate_id obrigatórios", ErrInvalidEvent)
	}
	if !eventTypePattern.MatchString(event.EventType) {
		return fmt.Errorf("%w: event_type deve seguir <modulo>.<entidade>.<acao>", ErrInvalidEvent)
	}
	if event.Version <= 0 {
		return fmt.Errorf("%w: event_version deve ser positivo", ErrInvalidEvent)
	}
	var object map[string]json.RawMessage
	if len(event.Payload) == 0 || json.Unmarshal(event.Payload, &object) != nil || object == nil {
		return fmt.Errorf("%w: payload deve ser um objeto JSON", ErrInvalidEvent)
	}
	key := strings.TrimSpace(event.IdempotencyKey)
	if key == "" || len(key) > 200 {
		return fmt.Errorf("%w: idempotency_key obrigatória com até 200 caracteres", ErrInvalidEvent)
	}
	if event.MaxAttempts <= 0 || event.MaxAttempts > 1000 {
		return fmt.Errorf("%w: max_attempts deve estar entre 1 e 1000", ErrInvalidEvent)
	}
	if event.Status != "" && event.Status != "pending" {
		return fmt.Errorf("%w: evento só nasce pending", ErrInvalidEvent)
	}
	return nil
}

func errorText(err error) string {
	if err == nil {
		return "erro sem mensagem"
	}
	text := strings.TrimSpace(err.Error())
	if text == "" {
		return "erro sem mensagem"
	}
	if runes := []rune(text); len(runes) > maxErrorRunes {
		text = string(runes[:maxErrorRunes])
	}
	return text
}
