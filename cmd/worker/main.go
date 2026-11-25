// Comando worker é o ponto de entrada dos consumidores de fila e jobs do SaaS.
//
// Ele drena a outbox transacional por tenant (OUT-01B): descobre tenants com
// evento elegível, reivindica com lease, entrega via handler real e registra
// sucesso/falha de forma idempotente. Nenhum segredo nem payload é logado.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/config"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/log"
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
		return fmt.Errorf("carregar configuração: %w", err)
	}
	workerCfg, err := workerConfigFrom(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("carregar configuração do worker: %w", err)
	}

	level, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("configurar log: %w", err)
	}
	logger := log.New(os.Stdout, level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("conectar ao PostgreSQL: %w", err)
	}
	defer database.Close()

	repository, err := outbox.NewRepository(database)
	if err != nil {
		return fmt.Errorf("criar repositório da outbox: %w", err)
	}

	handler := selectHandler(workerCfg.Handler)
	dispatcher, err := outbox.NewDispatcher(repository, handler, outbox.DispatcherConfig{
		Batch:      workerCfg.Batch,
		MaxTenants: workerCfg.MaxTenants,
		Lease:      workerCfg.Lease,
		Interval:   workerCfg.Interval,
		Logger:     logger,
	})
	if err != nil {
		return fmt.Errorf("criar dispatcher da outbox: %w", err)
	}

	// Só campos não sensíveis vão para o log de bootstrap: env, limites e
	// nome do handler. O DSN permanece apenas na conexão.
	logger.Info("worker iniciado", "env", cfg.Env, "config", workerCfg.String())
	err = dispatcher.Run(ctx)
	if err != nil && err != context.Canceled {
		return fmt.Errorf("ciclo da outbox: %w", err)
	}
	logger.Info("worker encerrado")
	return nil
}

// selectHandler escolhe o handler real de entrega ou a recusa explícita.
// "refuse" existe para ligar o binário sem efeito sem esvaziar a outbox: o
// ADR 0009 proíbe marcar eventos como processados sem efeito real. Qualquer
// valor diferente de "delivery" cai na recusa, nunca em noop silencioso.
func selectHandler(name string) outbox.Handler {
	if name == "delivery" {
		return outbox.NewDeliveryHandler()
	}
	return outbox.RefusingHandler{}
}
