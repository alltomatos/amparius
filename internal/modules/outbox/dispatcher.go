package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

// Handler entrega um evento. Deve ser idempotente por (event_id, attempt) e
// devolver erros sem PII: a mensagem é persistida em last_error.
type Handler interface {
	Handle(ctx context.Context, event Event) error
}

// Store é a superfície do repositório usada pelo dispatcher. A interface
// permite testar log, métricas e ciclo sem PostgreSQL, sem afrouxar o contrato
// do repositório real.
type Store interface {
	PendingTenants(ctx context.Context, limit int) ([]uuid.UUID, error)
	ReapExpired(ctx context.Context) (int, error)
	Claim(ctx context.Context, limit int, lease time.Duration) ([]Event, error)
	Succeed(ctx context.Context, id uuid.UUID) (Event, error)
	Fail(ctx context.Context, id uuid.UUID, cause error, retryAfter time.Duration) (Event, error)
	Backlog(ctx context.Context) (Backlog, error)
}

// DispatcherConfig parametriza o ciclo de dispatch.
type DispatcherConfig struct {
	// Batch é o máximo de eventos reivindicados por tenant em um ciclo.
	Batch int
	// MaxTenants limita quantos tenants são visitados por ciclo.
	MaxTenants int
	// Lease é o prazo de visibilidade do claim antes de outro worker poder
	// reivindicar o evento.
	Lease time.Duration
	// Interval é a pausa entre ciclos em Run.
	Interval time.Duration
	// Logger recebe apenas identificadores; nunca payload.
	Logger *slog.Logger
	// Metrics recebe contadores, backlog e latências. Nil desabilita métricas.
	Metrics *observability.Metrics
}

// Result resume um ciclo de dispatch.
type Result struct {
	Tenants   int
	Reaped    int
	Claimed   int
	Succeeded int
	Retried   int
	Failed    int
	Backlog   Backlog
}

// Dispatcher drena a outbox por tenant com claim seguro e retry persistido.
type Dispatcher struct {
	store   Store
	handler Handler
	config  DispatcherConfig
}

// NewDispatcher valida as dependências e os limites do ciclo.
func NewDispatcher(store Store, handler Handler, config DispatcherConfig) (*Dispatcher, error) {
	if store == nil {
		return nil, errors.New("repositório de outbox nulo")
	}
	if handler == nil {
		return nil, errors.New("handler da outbox nulo")
	}
	if config.Batch == 0 {
		config.Batch = 50
	}
	if config.Batch < 0 || config.Batch > 1000 {
		return nil, fmt.Errorf("%w: batch entre 1 e 1000", ErrInvalidEvent)
	}
	if config.MaxTenants == 0 {
		config.MaxTenants = 100
	}
	if config.MaxTenants < 0 || config.MaxTenants > 1000 {
		return nil, fmt.Errorf("%w: max_tenants entre 1 e 1000", ErrInvalidEvent)
	}
	if config.Lease == 0 {
		config.Lease = 5 * time.Minute
	}
	if config.Lease < 0 {
		return nil, fmt.Errorf("%w: lease deve ser positivo", ErrInvalidEvent)
	}
	if config.Interval == 0 {
		config.Interval = 5 * time.Second
	}
	if config.Interval < 0 {
		return nil, fmt.Errorf("%w: intervalo deve ser positivo", ErrInvalidEvent)
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Dispatcher{store: store, handler: handler, config: config}, nil
}

// RunOnce executa um ciclo: expira leases vencidos e drena os tenants com
// evento elegível. Um erro em um tenant não impede os demais; os erros são
// agregados no retorno. Cada ciclo carrega um correlation id próprio e cada
// evento usa o próprio id como trace id nos logs.
func (d *Dispatcher) RunOnce(ctx context.Context) (Result, error) {
	started := time.Now()
	correlationID := observability.CorrelationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = observability.NewCorrelationID()
	}
	ctx = observability.WithCorrelationID(ctx, correlationID)

	result, err := d.runOnce(ctx, correlationID)
	elapsed := time.Since(started)

	if d.config.Metrics != nil {
		d.config.Metrics.AddCounter(MetricCyclesTotal, nil, 1)
		d.config.Metrics.Observe(MetricCycleDurationSeconds, nil, elapsed.Seconds())
		if err != nil {
			d.config.Metrics.AddCounter(MetricCycleErrorsTotal, nil, 1)
		}
	}

	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	attrs := []any{
		"correlation_id", correlationID,
		"tenants", result.Tenants,
		"reaped", result.Reaped,
		"claimed", result.Claimed,
		"succeeded", result.Succeeded,
		"retried", result.Retried,
		"failed", result.Failed,
		"backlog_pending", result.Backlog.Pending,
		"backlog_processing", result.Backlog.Processing,
		"backlog_failed", result.Backlog.Failed,
		"backlog_oldest_age_seconds", int64(result.Backlog.OldestPendingAge / time.Second),
		"duration_ms", elapsed.Milliseconds(),
	}
	if err != nil {
		attrs = append(attrs, "erro", RedactError(err))
	}
	d.config.Logger.Log(ctx, level, "outbox ciclo concluido", attrs...)
	return result, err
}

func (d *Dispatcher) runOnce(ctx context.Context, correlationID string) (Result, error) {
	var result Result
	var errs []error
	var backlogSamples []observability.Sample
	var oldestSamples []observability.Sample

	tenants, err := d.store.PendingTenants(ctx, d.config.MaxTenants)
	if err != nil {
		return result, err
	}
	result.Tenants = len(tenants)

	for _, tenant := range tenants {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		tenantCtx := db.WithTenant(ctx, tenant)

		backlog, backlogErr := d.store.Backlog(tenantCtx)
		if backlogErr != nil {
			if d.config.Metrics != nil {
				d.config.Metrics.AddCounter(MetricBacklogSampleErrorsTotal, nil, 1)
			}
			d.config.Logger.Warn("outbox falha ao amostrar backlog",
				"tenant_id", tenant.String(),
				"correlation_id", correlationID,
				"erro", RedactError(backlogErr),
			)
		} else {
			result.Backlog.Pending += backlog.Pending
			result.Backlog.Processing += backlog.Processing
			result.Backlog.Failed += backlog.Failed
			if backlog.OldestPendingAge > result.Backlog.OldestPendingAge {
				result.Backlog.OldestPendingAge = backlog.OldestPendingAge
			}
			for status, value := range map[string]int64{
				"pending":    backlog.Pending,
				"processing": backlog.Processing,
				"failed":     backlog.Failed,
			} {
				backlogSamples = append(backlogSamples, observability.Sample{
					Labels: observability.Labels{"tenant_id": tenant.String(), "status": status},
					Value:  float64(value),
				})
			}
			oldestSamples = append(oldestSamples, observability.Sample{
				Labels: observability.Labels{"tenant_id": tenant.String()},
				Value:  backlog.OldestPendingAge.Seconds(),
			})
		}

		reaped, err := d.store.ReapExpired(tenantCtx)
		if err != nil {
			errs = append(errs, err)
			d.config.Logger.Error("outbox falha ao expirar leases",
				"tenant_id", tenant.String(),
				"correlation_id", correlationID,
				"erro", RedactError(err),
			)
			continue
		}
		result.Reaped += reaped
		if reaped > 0 && d.config.Metrics != nil {
			d.config.Metrics.AddCounter(MetricEventsReapedTotal, nil, float64(reaped))
		}

		events, err := d.store.Claim(tenantCtx, d.config.Batch, d.config.Lease)
		if err != nil {
			errs = append(errs, err)
			d.config.Logger.Error("outbox falha ao reivindicar eventos",
				"tenant_id", tenant.String(),
				"correlation_id", correlationID,
				"erro", RedactError(err),
			)
			continue
		}
		result.Claimed += len(events)
		if len(events) > 0 && d.config.Metrics != nil {
			d.config.Metrics.AddCounter(MetricEventsClaimedTotal, nil, float64(len(events)))
		}
		for _, event := range events {
			d.deliver(tenantCtx, correlationID, event, &result, &errs)
		}
	}

	if d.config.Metrics != nil {
		d.config.Metrics.ReplaceGauges(MetricBacklogEvents, backlogSamples)
		d.config.Metrics.ReplaceGauges(MetricBacklogOldestAgeSeconds, oldestSamples)
	}
	if len(errs) > 0 {
		return result, errors.Join(errs...)
	}
	return result, nil
}

// Run executa um ciclo imediatamente e repete a cada Interval até o contexto
// terminar. O ciclo atual sempre termina antes do retorno.
func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.config.Interval)
	defer ticker.Stop()
	for {
		if _, err := d.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			d.config.Logger.Error("outbox ciclo falhou", "erro", RedactError(err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, correlationID string, event Event, result *Result, errs *[]error) {
	started := time.Now()
	err := handle(ctx, d.handler, event)
	elapsed := time.Since(started)
	if d.config.Metrics != nil {
		d.config.Metrics.Observe(MetricDeliveryDurationSeconds, nil, elapsed.Seconds())
	}

	attrs := []any{
		"tenant_id", event.TenantID.String(),
		"event_id", event.ID.String(),
		"trace_id", event.ID.String(),
		"event_type", event.EventType,
		"attempt", event.Attempts,
		"max_attempts", event.MaxAttempts,
		"correlation_id", correlationID,
		"duration_ms", elapsed.Milliseconds(),
	}

	if err != nil {
		failed, failErr := d.store.Fail(ctx, event.ID, err, Backoff(event.Attempts))
		if failErr != nil {
			*errs = append(*errs, failErr)
			d.config.Logger.Error("outbox falha ao registrar erro",
				append(attrs, "erro", RedactError(failErr))...)
			return
		}
		if failed.Status == "failed" {
			result.Failed++
			if d.config.Metrics != nil {
				d.config.Metrics.AddCounter(MetricEventsFailedTotal, nil, 1)
			}
			d.config.Logger.Error("outbox evento esgotou tentativas",
				append(attrs, "erro", RedactError(err))...)
			return
		}
		result.Retried++
		if d.config.Metrics != nil {
			d.config.Metrics.AddCounter(MetricEventsRetriedTotal, nil, 1)
		}
		d.config.Logger.Warn("outbox evento reagendado",
			append(attrs,
				"erro", RedactError(err),
				"next_attempt_at", failed.NextAttemptAt.UTC().Format(time.RFC3339Nano),
			)...)
		return
	}
	if _, err := d.store.Succeed(ctx, event.ID); err != nil {
		*errs = append(*errs, err)
		d.config.Logger.Error("outbox falha ao concluir",
			append(attrs, "erro", RedactError(err))...)
		return
	}
	result.Succeeded++
	if d.config.Metrics != nil {
		d.config.Metrics.AddCounter(MetricEventsSucceededTotal, nil, 1)
	}
	d.config.Logger.Debug("outbox evento entregue", attrs...)
}

func handle(ctx context.Context, handler Handler, event Event) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("handler entrou em panic: %v", recovered)
		}
	}()
	return handler.Handle(ctx, event)
}
