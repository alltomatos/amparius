// Comando api é o binário HTTP do SaaS do Amparius.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alltomatos/amparius/internal/modules/catalog"
	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/modules/finance"
	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/modules/party"
	"github.com/alltomatos/amparius/internal/platform/config"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/alltomatos/amparius/internal/platform/log"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "api: erro fatal: %v\n", err)
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

	devOnboardingEnabled := cfg.Env == "development" && cfg.EnableDevOnboarding
	var onboardingHandler http.Handler
	var partiesHandler http.Handler
	var deceasedHandler http.Handler
	var familyLinksHandler http.Handler
	var catalogHandler http.Handler
	var financeHandler http.Handler
	var funeralHandler http.Handler
	if devOnboardingEnabled {
		database, err := db.New(ctx, cfg.PostgresDSN)
		if err != nil {
			return fmt.Errorf("conectar PostgreSQL: %w", err)
		}
		defer database.Close()

		repository, err := identity.NewRepository(database)
		if err != nil {
			return fmt.Errorf("criar repositório de identidade: %w", err)
		}
		onboardingHandler = identity.NewDevelopmentHTTPHandler(repository)

		partyRepository, err := party.NewRepository(database)
		if err != nil {
			return fmt.Errorf("criar repositório de party: %w", err)
		}
		partiesHandler = party.NewDevelopmentHTTPHandler(partyRepository)
		deceasedHandler = party.NewDevelopmentDeceasedHTTPHandler(partyRepository)
		familyLinksHandler = party.NewDevelopmentFamilyLinkHTTPHandler(partyRepository)

		catalogRepository, err := catalog.NewRepository(database)
		if err != nil {
			return fmt.Errorf("criar repositório de catálogo: %w", err)
		}
		catalogHandler = catalog.NewDevelopmentHTTPHandler(catalogRepository)

		financeRepository, err := finance.NewRepository(database)
		if err != nil {
			return fmt.Errorf("criar repositório financeiro: %w", err)
		}
		financeHandler = finance.NewDevelopmentHTTPHandler(financeRepository)

		var checker funeral.AccessChecker
		if cfg.ControlPlaneDSN != "" {
			controlDatabase, controlErr := db.New(ctx, cfg.ControlPlaneDSN)
			if controlErr == nil {
				defer controlDatabase.Close()
				controlChecker, checkerErr := entitlement.NewChecker(controlDatabase,
					entitlement.WithCompanyResolver(repository),
				)
				if checkerErr != nil {
					controlErr = checkerErr
				} else {
					checker = controlChecker
				}
			}
			if controlErr != nil {
				logger.Warn("control plane de entitlement indisponível")
			}
		}
		optionalHandler := funeral.NewDevelopmentHTTPHandler(checker)
		funeralHandler = httpserver.DevelopmentTenantCompanyContext(optionalHandler, repository)
	}

	server := &http.Server{
		Addr: cfg.HTTPAddr(),
		Handler: httpserver.New(logger, httpserver.Options{
			DevelopmentMode:        devOnboardingEnabled,
			DevelopmentOnboarding:  onboardingHandler,
			DevelopmentParties:     partiesHandler,
			DevelopmentDeceased:    deceasedHandler,
			DevelopmentFamilyLinks: familyLinksHandler,
			DevelopmentCatalog:     catalogHandler,
			DevelopmentFinance:     financeHandler,
			DevelopmentFuneral:     funeralHandler,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("api ouvindo", "addr", server.Addr, "env", cfg.Env)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("servidor http: %w", err)
	case <-ctx.Done():
		logger.Info("sinal de término recebido, encerrando")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("encerrar servidor http: %w", err)
	}
	if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("servidor http: %w", err)
	}

	logger.Info("api encerrada")
	return nil
}
