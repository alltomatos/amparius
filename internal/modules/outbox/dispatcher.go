package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
)

// Handler entrega um evento. Deve ser idempotente por (event_id, attempt) e
// devolver erros sem PII: a mensagem é persistida em last_error.
type Handler interface {
	Handle(ctx context.Context, event Event) error
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
}

// Result resume um ciclo de dispatch.
type Result struct {
	Tenants   int
	Reaped    int
	Claimed   int
	Succeeded int
	Retried   int
	Failed    int
}

// Dispatcher drena a outbox por tenant com claim seguro e retry persistido.
type Dispatcher struct {
	repository *Repository
	handler    Handler
	config     DispatcherConfig
}

// NewDispatcher valida as dependências e os limites do ciclo.
func NewDispatcher(repository *Repository, handler Handler, config DispatcherConfig) (*Dispatcher, error) {
	if repository == nil {
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
	return &Dispatcher{repository: repository, handler: handler, config: config}, nil
}

// RunOnce executa um ciclo: expira leases vencidos e drena os tenants com
// evento elegível. Um erro em um tenant não impede os demais; os erros são
// agregados no retorno.
func (d *Dispatcher) RunOnce(ctx context.Context) (Result, error) {
	var result Result
	var errs []error
	tenants, err := d.repository.PendingTenants(ctx, d.config.MaxTenants)
	if err != nil {
		return result, err
	}
	result.Tenants = len(tenants)
	for _, tenant := range tenants {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		tenantCtx := db.WithTenant(ctx, tenant)
		reaped, err := d.repository.ReapExpired(tenantCtx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		result.Reaped += reaped
		events, err := d.repository.Claim(tenantCtx, d.config.Batch, d.config.Lease)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		result.Claimed += len(events)
		for _, event := range events {
			d.deliver(tenantCtx, event, &result, &errs)
		}
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
			d.config.Logger.Error("outbox ciclo falhou", "erro", err.Error())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, event Event, result *Result, errs *[]error) {
	err := handle(ctx, d.handler, event)
	if err != nil {
		failed, failErr := d.repository.Fail(ctx, event.ID, err, Backoff(event.Attempts))
		if failErr != nil {
			*errs = append(*errs, failErr)
			d.config.Logger.Error("outbox falha ao registrar erro", "event_id", event.ID.String(), "tipo", event.EventType, "erro", failErr.Error())
			return
		}
		if failed.Status == "failed" {
			result.Failed++
			d.config.Logger.Error("outbox evento esgotou tentativas", "event_id", event.ID.String(), "tipo", event.EventType, "tentativas", failed.Attempts)
			return
		}
		result.Retried++
		d.config.Logger.Warn("outbox evento reagendado", "event_id", event.ID.String(), "tipo", event.EventType, "tentativa", failed.Attempts)
		return
	}
	if _, err := d.repository.Succeed(ctx, event.ID); err != nil {
		*errs = append(*errs, err)
		d.config.Logger.Error("outbox falha ao concluir", "event_id", event.ID.String(), "tipo", event.EventType, "erro", err.Error())
		return
	}
	result.Succeeded++
}

func handle(ctx context.Context, handler Handler, event Event) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("handler entrou em panic: %v", recovered)
		}
	}()
	return handler.Handle(ctx, event)
}
