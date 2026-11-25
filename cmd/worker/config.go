// Package main: configuracao nao sensivel do worker.
package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/config"
)

// ErrInvalidWorkerConfig classifica falhas de configuracao do worker.
var ErrInvalidWorkerConfig = errors.New("configuracao do worker invalida")

const (
	defaultOutboxBatch       = 50
	defaultOutboxMaxTenants  = 100
	defaultOutboxLease       = 5 * time.Minute
	defaultOutboxInterval    = 5 * time.Second
	defaultOutboxHandler     = "delivery"
	defaultOutboxMetricsAddr = "127.0.0.1:9091"
	maxOutboxBatch           = 1000
	maxOutboxMaxTenants      = 1000
)

// workerConfig e a configuracao nao sensivel do ciclo de dispatch. O DSN fica
// em config.Config e NUNCA e copiado para esta struct nem para String(): so o
// bootstrap abre a conexao, e nenhum log recebe a struct de config completa.
type workerConfig struct {
	Batch       int
	MaxTenants  int
	Lease       time.Duration
	Interval    time.Duration
	Handler     string
	MetricsAddr string
}

// workerConfigFrom le apenas variaveis nao sensiveis. A validacao garante que
// o lease cubra pelo menos um intervalo inteiro: caso contrario o evento seria
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
	metricsAddr, err := metricsAddrFrom(lookup)
	if err != nil {
		return workerConfig{}, err
	}
	return workerConfig{
		Batch: batch, MaxTenants: maxTenants, Lease: lease, Interval: interval,
		Handler: handler, MetricsAddr: metricsAddr,
	}, nil
}

// String devolve so campos nao sensiveis, proprios para log de bootstrap.
func (c workerConfig) String() string {
	metrics := c.MetricsAddr
	if metrics == "" {
		metrics = "off"
	}
	return fmt.Sprintf("batch=%d max_tenants=%d lease=%s interval=%s handler=%s metrics_addr=%s",
		c.Batch, c.MaxTenants, c.Lease, c.Interval, c.Handler, metrics)
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
		return 0, fmt.Errorf("%w: %s deve ser duracao positiva, recebido %q", ErrInvalidWorkerConfig, key, value)
	}
	return parsed, nil
}

// metricsAddrFrom aceita host:porta ou um valor explicito de desativacao. O
// padrao escuta apenas em loopback para nao expor metricas na rede por acidente.
func metricsAddrFrom(lookup config.LookupFunc) (string, error) {
	value, ok := lookup("OUTBOX_METRICS_ADDR")
	if !ok || strings.TrimSpace(value) == "" {
		return defaultOutboxMetricsAddr, nil
	}
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "off", "disabled", "none", "-":
		return "", nil
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("%w: OUTBOX_METRICS_ADDR deve ser host:porta ou off, recebido %q", ErrInvalidWorkerConfig, value)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", fmt.Errorf("%w: OUTBOX_METRICS_ADDR com porta invalida %q", ErrInvalidWorkerConfig, value)
	}
	return net.JoinHostPort(host, strconv.Itoa(number)), nil
}
