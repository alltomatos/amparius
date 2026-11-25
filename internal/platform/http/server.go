// Package httpserver monta o servidor HTTP do SaaS usando apenas net/http.
package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/alltomatos/amparius/internal/platform/auth"
)

// Server agrega rotas e middlewares do serviço HTTP.
type Server struct {
	logger  *slog.Logger
	mux     *http.ServeMux
	handler http.Handler
}

// Options controla dependências opcionais do servidor HTTP.
type Options struct {
	// Authenticator protege rotas de produção com OIDC, MFA e RBAC. Se uma
	// rota autenticada for registrada sem esta dependência, ela falha fechado.
	Authenticator auth.Gate
	// AuthenticatedRoutes são handlers de produção já prontos para receber o
	// contexto confiável tenant/company composto pelo middleware.
	AuthenticatedRoutes []AuthenticatedRoute
	// DevelopmentMode habilita rotas explicitamente marcadas como dev-only.
	DevelopmentMode bool
	// DevelopmentOnboarding é o handler de POST /_dev/onboarding.
	DevelopmentOnboarding http.Handler
	// DevelopmentParties é o handler de POST /_dev/parties e GET /_dev/parties/{id}.
	DevelopmentParties http.Handler
	// DevelopmentDeceased é o handler de POST /_dev/deceased e GET /_dev/deceased/{id}.
	DevelopmentDeceased http.Handler
	// DevelopmentFamilyLinks é o handler de POST /_dev/family-links e
	// GET /_dev/family-links/{id}.
	DevelopmentFamilyLinks http.Handler
	// DevelopmentCatalog é o handler das rotas dev-only de catálogo:
	// itens, listas de preços e preços.
	DevelopmentCatalog http.Handler
	// DevelopmentFinance é o handler da jornada financeira mínima dev-only.
	DevelopmentFinance http.Handler
	// DevelopmentFuneral é o handler da superfície mínima do módulo opcional.
	DevelopmentFuneral http.Handler
}

// AuthenticatedRoute associa uma rota a uma permissão RBAC.
type AuthenticatedRoute struct {
	Pattern    string
	Permission auth.Permission
	Handler    http.Handler
}

// New constrói o servidor com as dependências injetadas.
func New(logger *slog.Logger, options ...Options) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		logger: logger,
		mux:    http.NewServeMux(),
	}
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	s.routes(opts)
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

func (s *Server) routes(options Options) {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	for _, route := range options.AuthenticatedRoutes {
		if route.Pattern == "" || route.Handler == nil {
			continue
		}
		s.mux.Handle(route.Pattern, auth.Require(options.Authenticator, route.Permission, s.logger)(route.Handler))
	}
	if options.DevelopmentMode && options.DevelopmentOnboarding != nil {
		s.mux.Handle("/_dev/onboarding", options.DevelopmentOnboarding)
	}
	if options.DevelopmentMode && options.DevelopmentParties != nil {
		s.mux.Handle("/_dev/parties", options.DevelopmentParties)
		s.mux.Handle("/_dev/parties/{id}", options.DevelopmentParties)
	}
	if options.DevelopmentMode && options.DevelopmentDeceased != nil {
		s.mux.Handle("/_dev/deceased", options.DevelopmentDeceased)
		s.mux.Handle("/_dev/deceased/{id}", options.DevelopmentDeceased)
	}
	if options.DevelopmentMode && options.DevelopmentFamilyLinks != nil {
		s.mux.Handle("/_dev/family-links", options.DevelopmentFamilyLinks)
		s.mux.Handle("/_dev/family-links/{id}", options.DevelopmentFamilyLinks)
	}
	if options.DevelopmentMode && options.DevelopmentCatalog != nil {
		s.mux.Handle("/_dev/catalog/items", options.DevelopmentCatalog)
		s.mux.Handle("/_dev/catalog/items/{id}", options.DevelopmentCatalog)
		s.mux.Handle("/_dev/catalog/price-lists", options.DevelopmentCatalog)
		s.mux.Handle("/_dev/catalog/price-lists/{id}", options.DevelopmentCatalog)
		s.mux.Handle("/_dev/catalog/prices", options.DevelopmentCatalog)
		s.mux.Handle("/_dev/catalog/prices/{id}", options.DevelopmentCatalog)
	}
	if options.DevelopmentMode && options.DevelopmentFinance != nil {
		for _, pattern := range []string{
			"/_dev/finance/accounts",
			"/_dev/finance/accounts/{id}",
			"/_dev/finance/titles",
			"/_dev/finance/titles/{id}",
			"/_dev/finance/titles/{id}/installments",
			"/_dev/finance/installments",
			"/_dev/finance/installments/{id}",
			"/_dev/finance/payments",
			"/_dev/finance/cash-sessions",
			"/_dev/finance/cash-sessions/{id}",
			"/_dev/finance/cash-movements",
			"/_dev/finance/ledger-entries",
		} {
			s.mux.Handle(pattern, options.DevelopmentFinance)
		}
	}
	if options.DevelopmentMode && options.DevelopmentFuneral != nil {
		s.mux.Handle("/_dev/funeral/access", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/contracts", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/contracts/{id}", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/contracts/{id}/status", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/contracts/{id}/service-orders", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/contracts/{id}/journey", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/service-orders/{id}", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/service-orders/{id}/complete", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/service-orders/{id}/status", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/locations", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/resources", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/schedules", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/schedules/{id}", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/schedules/{id}/cancel", options.DevelopmentFuneral)
		s.mux.Handle("/_dev/funeral/contracts/{id}/schedules", options.DevelopmentFuneral)
	}
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
