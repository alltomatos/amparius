package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/alltomatos/amparius/internal/platform/observability"
)

const metricsShutdownTimeout = 5 * time.Second

// startMetricsServer sobe o endpoint de scrape do worker. Endereco vazio
// desabilita. O listener e aberto antes da goroutine para que falha de bind
// derrube o bootstrap em vez de esconder um worker sem metricas.
func startMetricsServer(ctx context.Context, logger *slog.Logger, addr string, metrics *observability.Metrics) error {
	if addr == "" {
		logger.Info("metricas do worker desabilitadas")
		return nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ouvir metricas em %s: %w", addr, err)
	}
	server := &http.Server{
		Handler:           metricsHandler(metrics),
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Info("metricas do worker ouvindo", "addr", listener.Addr().String())
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("servidor de metricas falhou", "erro", err.Error())
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), metricsShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return nil
}

// metricsHandler monta o mux de observabilidade do worker. Nao expoe payload,
// DSN nem qualquer dado de negocio: apenas a exposicao Prometheus e liveness.
func metricsHandler(metrics *observability.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}
