// Comando worker: ponto de entrada dos consumidores de fila e jobs do SaaS.
//
// Ele drena a outbox transacional por tenant (OUT-01B) e publica metricas
// Prometheus do backlog, retry, falha e latencia (OUT-01C). Nenhum segredo nem
// payload e logado.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/config"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/log"
	"github.com/alltomatos/amparius/internal/platform/observability"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "worker: erro fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("carregar configuracao: %w", err)
	}
	workerCfg, err := workerConfigFrom(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("carregar configuracao do worker: %w", err)
	}

	level, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("configurar log: %w", err)
	}
	workerID := observability.NewCorrelationID()
	logger := log.New(os.Stdout, level).With(
		"service", "worker",
		"env", cfg.Env,
		"worker_id", workerID,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	metrics := observability.NewMetrics()
	if err := startMetricsServer(ctx, logger, workerCfg.MetricsAddr, metrics); err != nil {
		return err
	}

	database, err := db.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("conectar ao PostgreSQL: %w", err)
	}
	defer database.Close()

	repository, err := outbox.NewRepository(database)
	if err != nil {
		return fmt.Errorf("criar repositorio da outbox: %w", err)
	}

	handler := selectHandler(workerCfg.Handler)
	dispatcher, err := outbox.NewDispatcher(repository, handler, outbox.DispatcherConfig{
		Batch:      workerCfg.Batch,
		MaxTenants: workerCfg.MaxTenants,
		Lease:      workerCfg.Lease,
		Interval:   workerCfg.Interval,
		Logger:     logger,
		Metrics:    metrics,
	})
	if err != nil {
		return fmt.Errorf("criar dispatcher da outbox: %w", err)
	}

	// So campos nao sensiveis vao para o log de bootstrap: env, limites e
	// nome do handler. O DSN permanece apenas na conexao.
	logger.Info("worker iniciado", "config", workerCfg.String())
	err = dispatcher.Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("ciclo da outbox: %w", err)
	}
	logger.Info("worker encerrado")
	return nil
}

// selectHandler escolhe o handler real de entrega ou a recusa explicita.
// "refuse" existe para ligar o binario sem efeito sem esvaziar a outbox: o
// ADR 0009 proibe marcar eventos como processados sem efeito real. Qualquer
// valor diferente de "delivery" cai na recusa, nunca em noop silencioso.
func selectHandler(name string) outbox.Handler {
	if name == "delivery" {
		return outbox.NewDeliveryHandler()
	}
	return outbox.RefusingHandler{}
}
