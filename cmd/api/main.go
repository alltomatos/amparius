// Comando api é o binário HTTP do SaaS do Amparius.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alltomatos/amparius/internal/modules/catalog"
	"github.com/alltomatos/amparius/internal/modules/controlplane"
	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/modules/finance"
	"github.com/alltomatos/amparius/internal/modules/funeral"
	"github.com/alltomatos/amparius/internal/modules/identity"
	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/modules/party"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/config"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/alltomatos/amparius/internal/platform/log"
	"github.com/alltomatos/amparius/internal/platform/readiness"
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

	handler, closeHandler, err := buildHandler(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer closeHandler()

	server := &http.Server{
		Addr:              cfg.HTTPAddr(),
		Handler:           handler,
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

// buildHandler monta o handler HTTP a partir da configuração carregada, sem
// abrir nenhum listener de rede — é o mesmo caminho que run() usa para
// montar o servidor, exposto em separado para ser exercido por teste pelo
// entry point normal (cmd/api) sem depender de porta real nem de sinal de
// SO. O fechamento das dependências abertas aqui (pools de Postgres) é
// devolvido em closeAll em vez de defer, porque um defer dentro desta função
// fecharia os pools antes do handler devolvido ser efetivamente usado.
func buildHandler(ctx context.Context, cfg config.Config, logger *slog.Logger) (http.Handler, func(), error) {
	readinessChecker, closeReadiness, err := newReadiness(ctx, cfg)
	if err != nil {
		return nil, func() {}, fmt.Errorf("configurar readiness: %w", err)
	}
	closers := []func(){closeReadiness}
	closeAll := func() {
		for _, closeDependency := range closers {
			closeDependency()
		}
	}

	devOnboardingEnabled := cfg.Env == "development" && cfg.EnableDevOnboarding
	var onboardingHandler http.Handler
	var partiesHandler http.Handler
	var deceasedHandler http.Handler
	var familyLinksHandler http.Handler
	var catalogHandler http.Handler
	var financeHandler http.Handler
	var funeralHandler http.Handler
	var authenticatedRoutes []httpserver.AuthenticatedRoute
	if devOnboardingEnabled {
		database, err := db.New(ctx, cfg.PostgresDSN)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("conectar PostgreSQL: %w", err)
		}
		closers = append(closers, database.Close)

		repository, err := identity.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de identidade: %w", err)
		}
		onboardingHandler = identity.NewDevelopmentHTTPHandler(repository)

		partyRepository, err := party.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de party: %w", err)
		}
		partiesHandler = party.NewDevelopmentHTTPHandler(partyRepository)
		deceasedHandler = party.NewDevelopmentDeceasedHTTPHandler(partyRepository)
		familyLinksHandler = party.NewDevelopmentFamilyLinkHTTPHandler(partyRepository)

		catalogRepository, err := catalog.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de catálogo: %w", err)
		}
		catalogHandler = catalog.NewDevelopmentHTTPHandler(catalogRepository)

		financeRepository, err := finance.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório financeiro: %w", err)
		}
		financeHandler = finance.NewDevelopmentHTTPHandler(financeRepository)
		outboxRepository, err := outbox.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de outbox: %w", err)
		}
		funeralRepository, err := funeral.NewRepository(database, outboxRepository)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório funerário: %w", err)
		}

		var checker funeral.AccessChecker
		if cfg.ControlPlaneDSN != "" {
			controlDatabase, controlErr := db.New(ctx, cfg.ControlPlaneDSN)
			if controlErr == nil {
				closers = append(closers, controlDatabase.Close)
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
		optionalHandler := funeral.NewDevelopmentHTTPHandlerWithScheduling(checker, funeralRepository, funeralRepository)
		funeralHandler = httpserver.DevelopmentTenantCompanyContext(optionalHandler, repository)
	} else {
		// Sem control plane, o pool conecta sob demanda. Os probes ficam
		// disponíveis durante uma falha de conexão com a célula.
		openCell := db.NewLazy
		if cfg.ControlPlaneDSN != "" {
			openCell = db.New
		}
		database, err := openCell(ctx, cfg.PostgresDSN)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("configurar PostgreSQL da célula: %w", err)
		}
		closers = append(closers, database.Close)

		partyRepository, err := party.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de party: %w", err)
		}
		if cfg.ControlPlaneDSN != "" {
			repository, err := identity.NewRepository(database)
			if err != nil {
				closeAll()
				return nil, func() {}, fmt.Errorf("criar repositório de identidade: %w", err)
			}
			controlDatabase, controlErr := db.New(ctx, cfg.ControlPlaneDSN)
			if controlErr != nil {
				closeAll()
				return nil, func() {}, fmt.Errorf("conectar PostgreSQL do control plane: %w", controlErr)
			}
			closers = append(closers, controlDatabase.Close)
			entitlementService, entitlementErr := entitlement.NewService(controlDatabase, entitlement.WithCompanyResolver(repository))
			if entitlementErr != nil {
				closeAll()
				return nil, func() {}, fmt.Errorf("criar serviço de entitlement: %w", entitlementErr)
			}
			controlHandler := controlplane.NewHTTPHandler(repository, entitlementService)
			authenticatedRoutes = append(authenticatedRoutes,
				httpserver.AuthenticatedRoute{Pattern: "GET /v1/context", Permission: platformauth.PermissionContextRead, Handler: http.HandlerFunc(controlHandler.Context)},
				httpserver.AuthenticatedRoute{Pattern: "GET /v1/companies", Permission: platformauth.PermissionContextRead, Handler: http.HandlerFunc(controlHandler.Companies)},
				httpserver.AuthenticatedRoute{Pattern: "GET /v1/companies/{companyId}/entitlements", Permission: platformauth.PermissionEntitlementRead, Handler: http.HandlerFunc(controlHandler.Entitlements)},
				httpserver.AuthenticatedRoute{Pattern: "GET /v1/companies/{companyId}/entitlements/{module}", Permission: platformauth.PermissionEntitlementRead, Handler: http.HandlerFunc(controlHandler.EntitlementDetail)},
				httpserver.AuthenticatedRoute{Pattern: "GET /v1/companies/{companyId}/entitlements/{module}/audit", Permission: platformauth.PermissionEntitlementRead, Handler: http.HandlerFunc(controlHandler.EntitlementAudit)},
				httpserver.AuthenticatedRoute{Pattern: "GET /v1/entitlements/{entitlementId}/audit", Permission: platformauth.PermissionEntitlementRead, Handler: http.HandlerFunc(controlHandler.EntitlementAuditByID)},
				httpserver.AuthenticatedRoute{Pattern: "PUT /v1/companies/{companyId}/entitlements/{module}", Permission: platformauth.PermissionEntitlementWrite, Handler: http.HandlerFunc(controlHandler.GrantEntitlement)},
				httpserver.AuthenticatedRoute{Pattern: "PATCH /v1/companies/{companyId}/entitlements/{module}", Permission: platformauth.PermissionEntitlementWrite, Handler: http.HandlerFunc(controlHandler.PatchEntitlement)},
			)
		}
		partyHandler := party.NewProductionHTTPHandler(partyRepository)
		authenticatedRoutes = append(authenticatedRoutes,
			httpserver.AuthenticatedRoute{Pattern: "POST /v1/parties", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(partyHandler.Create)},
			httpserver.AuthenticatedRoute{Pattern: "GET /v1/parties", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(partyHandler.Search)},
		)
	}

	handler := httpserver.New(logger, httpserver.Options{
		Readiness: readinessChecker,
		// O provedor OIDC/MFA ainda não foi escolhido nem conectado ao
		// bootstrap. Não usar um autenticador sintético aqui: auth.Require
		// deve devolver 503 nas rotas autenticadas até essa configuração.
		AuthenticatedRoutes:    authenticatedRoutes,
		DevelopmentMode:        devOnboardingEnabled,
		DevelopmentOnboarding:  onboardingHandler,
		DevelopmentParties:     partiesHandler,
		DevelopmentDeceased:    deceasedHandler,
		DevelopmentFamilyLinks: familyLinksHandler,
		DevelopmentCatalog:     catalogHandler,
		DevelopmentFinance:     financeHandler,
		DevelopmentFuneral:     funeralHandler,
	}).Handler()

	return handler, closeAll, nil
}

func newReadiness(ctx context.Context, cfg config.Config) (*readiness.Checker, func(), error) {
	postgres, err := readiness.NewPostgres(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, func() {}, err
	}
	dependencies := []readiness.Dependency{
		postgres,
		readiness.NewRedis(cfg.RedisAddr),
		readiness.NewRabbitMQ(cfg.RabbitMQURL),
	}
	objectStorage, objectStorageErr := readiness.NewS3(ctx, readiness.S3Config{
		Endpoint:     cfg.ObjectStorageEndpoint,
		Region:       cfg.ObjectStorageRegion,
		Bucket:       cfg.ObjectStorageBucket,
		AccessKey:    cfg.ObjectStorageAccessKey,
		SecretKey:    cfg.ObjectStorageSecretKey,
		UsePathStyle: cfg.ObjectStorageUsePathStyle,
	})
	if objectStorageErr != nil {
		postgres.Close()
		return nil, func() {}, fmt.Errorf("configurar object storage: %w", objectStorageErr)
	}
	dependencies = append(dependencies, objectStorage)
	closers := []func(){postgres.Close}
	if cfg.ControlPlaneDSN != "" {
		controlPostgres, controlErr := readiness.NewPostgres(ctx, cfg.ControlPlaneDSN)
		if controlErr != nil {
			postgres.Close()
			return nil, func() {}, fmt.Errorf("configurar PostgreSQL do control plane: %w", controlErr)
		}
		dependencies = append(dependencies, controlPostgres)
		closers = append(closers, controlPostgres.Close)
	}
	closeAll := func() {
		for _, closeDependency := range closers {
			closeDependency()
		}
	}
	return readiness.New(dependencies, cfg.ReadinessTimeout), closeAll, nil
}
