// Package httpserver monta o servidor HTTP do SaaS usando apenas net/http.
package httpserver

import (
	"log/slog"
	"net/http"
)

// Server agrega rotas e middlewares do serviço HTTP.
type Server struct {
	logger  *slog.Logger
	mux     *http.ServeMux
	handler http.Handler
}

// New constrói o servidor com as dependências injetadas.
func New(logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		logger: logger,
		mux:    http.NewServeMux(),
	}
	s.routes()
	s.handler = chain(
		s.mux,
		requestID,
		accessLog(logger),
		recoverer(logger),
	)
	return s
}

// Handler devolve o http.Handler completo (rotas + middlewares).
func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
}

// handleHealthz é o liveness probe: processo vivo responde 200.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz é o readiness probe. Nesta fase é um stub: ainda não há
// dependências conectadas (Postgres/Redis/RabbitMQ/MinIO).
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"checks": map[string]string{},
	})
}
