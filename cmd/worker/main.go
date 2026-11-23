// Comando worker é o ponto de entrada dos consumidores de fila e jobs do SaaS.
//
// Nesta fase é apenas um esqueleto: registra o início, mantém o processo vivo
// e encerra de forma graciosa ao receber sinal.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alltomatos/amparius/internal/platform/config"
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

	level, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("configurar log: %w", err)
	}
	logger := log.New(os.Stdout, level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fila ainda não existe: o stub apenas sinaliza vida periodicamente.
	const heartbeat = 30 * time.Second
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	logger.Info("worker iniciado", "env", cfg.Env, "heartbeat", heartbeat.String())

	for {
		select {
		case <-ctx.Done():
			logger.Info("sinal de término recebido, encerrando worker")
			return nil
		case <-ticker.C:
			logger.Debug("worker ocioso: nenhuma fila registrada")
		}
	}
}
