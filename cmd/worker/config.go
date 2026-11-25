// Comando worker é o ponto de entrada dos consumidores de fila e jobs do SaaS.
package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/config"
)

// ErrInvalidWorkerConfig classifica falhas de configuração do worker.
var ErrInvalidWorkerConfig = errors.New("configuração do worker inválida")

const (
	defaultOutboxBatch      = 50
	defaultOutboxMaxTenants = 100
	defaultOutboxLease      = 5 * time.Minute
	defaultOutboxInterval   = 5 * time.Second
	defaultOutboxHandler    = "delivery"
	maxOutboxBatch          = 1000
	maxOutboxMaxTenants     = 1000
)

// workerConfig é a configuração não sensível do ciclo de dispatch. O DSN fica
// em config.Config e NUNCA é copiado para esta struct nem para String(): só o
// bootstrap abre a conexão, e nenhum log recebe a struct de config completa.
type workerConfig struct {
	Batch      int
	MaxTenants int
	Lease      time.Duration
	Interval   time.Duration
	Handler    string
}

// workerConfigFrom lê apenas variáveis não sensíveis. A validação garante que
// o lease cubra pelo menos um intervalo inteiro: caso contrário o evento seria
// reivindicado por outro worker antes de o ciclo atual terminar.
func workerConfigFrom(lookup config.LookupFunc) (workerConfig, error) {
	if lookup == nil {
		return workerConfig{}, fmt.Errorf("%w: lookup nulo", ErrInvalidWorkerConfig)
	}
	batch, err := intFrom(lookup, "OUTBOX_BATCH", defaultOutboxBatch, 1, maxOutboxBatch)
	if err != nil {
		return workerConfig{}, err
	}
	maxTenants, err := intFrom(lookup, "OUTBOX_MAX_TENANTS", defaultOutboxMaxTenants, 1, maxOutboxMaxTenants)
	if err != nil {
		return workerConfig{}, err
	}
	lease, err := durationFrom(lookup, "OUTBOX_LEASE", defaultOutboxLease)
	if err != nil {
		return workerConfig{}, err
	}
	interval, err := durationFrom(lookup, "OUTBOX_INTERVAL", defaultOutboxInterval)
	if err != nil {
		return workerConfig{}, err
	}
	if lease < interval {
		return workerConfig{}, fmt.Errorf("%w: OUTBOX_LEASE (%s) deve ser >= OUTBOX_INTERVAL (%s)", ErrInvalidWorkerConfig, lease, interval)
	}
	handler := defaultOutboxHandler
	if value, ok := lookup("OUTBOX_DELIVERY_HANDLER"); ok && strings.TrimSpace(value) != "" {
		handler = strings.ToLower(strings.TrimSpace(value))
	}
	switch handler {
	case "delivery", "refuse":
	default:
		return workerConfig{}, fmt.Errorf("%w: OUTBOX_DELIVERY_HANDLER desconhecido %q", ErrInvalidWorkerConfig, handler)
	}
	return workerConfig{
		Batch: batch, MaxTenants: maxTenants, Lease: lease, Interval: interval, Handler: handler,
	}, nil
}

// String devolve só campos não sensíveis, próprios para log de bootstrap.
func (c workerConfig) String() string {
	return fmt.Sprintf("batch=%d max_tenants=%d lease=%s interval=%s handler=%s",
		c.Batch, c.MaxTenants, c.Lease, c.Interval, c.Handler)
}

func intFrom(lookup config.LookupFunc, key string, fallback, min, max int) (int, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("%w: %s deve estar entre %d e %d, recebido %q", ErrInvalidWorkerConfig, key, min, max, value)
	}
	return parsed, nil
}

func durationFrom(lookup config.LookupFunc, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%w: %s deve ser duração positiva, recebido %q", ErrInvalidWorkerConfig, key, value)
	}
	return parsed, nil
}
