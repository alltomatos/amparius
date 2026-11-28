// Comando api é o binário HTTP do SaaS do Amparius.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	"github.com/alltomatos/amparius/internal/platform/audit"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/config"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/alltomatos/amparius/internal/platform/log"
	"github.com/alltomatos/amparius/internal/platform/observability"
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

	metrics := observability.NewMetrics()
	handler, closeHandler, err := buildHandler(ctx, cfg, logger, metrics)
	if err != nil {
		return err
	}
	defer closeHandler()
	if cfg.APIMetricsPort != "" {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", cfg.APIMetricsPort))
		if err != nil {
			return fmt.Errorf("ouvir métricas da API: %w", err)
		}
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", metrics.Handler())
		metricsServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
		defer metricsServer.Close()
		go func() {
			defer listener.Close()
			if err := metricsServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("servidor de métricas da API falhou", "erro", err.Error())
			}
		}()
	}

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

// handlerOption ajusta as opções HTTP antes de montar o servidor.
type handlerOption func(*httpserver.Options)

// buildHandler monta o handler HTTP a partir da configuração carregada, sem
// abrir nenhum listener de rede — é o mesmo caminho que run() usa para
// montar o servidor, exposto em separado para ser exercido por teste pelo
// entry point normal (cmd/api) sem depender de porta real nem de sinal de
// SO. O fechamento das dependências abertas aqui (pools de Postgres) é
// devolvido em closeAll em vez de defer, porque um defer dentro desta função
// fecharia os pools antes do handler devolvido ser efetivamente usado.
//
// Os handlerOption existem para a homologação injetar uma identidade de teste
// em ambiente descartável (ver homolog_control_plane_ui_test.go); o binário
// nunca os passa, então o bootstrap de produção continua fail-closed (503).
func buildHandler(ctx context.Context, cfg config.Config, logger *slog.Logger, metrics *observability.Metrics, overrides ...handlerOption) (http.Handler, func(), error) {
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
	var legalEntitiesHandler http.Handler
	var catalogHandler http.Handler
	var financeHandler http.Handler
	var funeralHandler http.Handler
	var authenticatedRoutes []httpserver.AuthenticatedRoute
	var platformRoutes []httpserver.PlatformRoute
	// Inject the approved tenant privacy policy when a provider is available.
	var policyProvider audit.PolicyProvider
	var identityRepository *identity.Repository
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
		identityRepository = repository
		onboardingHandler = identity.NewDevelopmentHTTPHandler(repository)

		partyRepository, err := party.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de party: %w", err)
		}
		partiesHandler = party.NewDevelopmentHTTPHandler(partyRepository)
		deceasedHandler = party.NewDevelopmentDeceasedHTTPHandler(partyRepository)
		familyLinksHandler = party.NewDevelopmentFamilyLinkHTTPHandler(partyRepository)
		legalEntitiesHandler = party.NewDevelopmentLegalEntityHTTPHandler(partyRepository)
		authenticatedRoutes = append(authenticatedRoutes, party.LegalEntityAuthenticatedRoutes(partyRepository, policyProvider)...)

		catalogRepository, err := catalog.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de catálogo: %w", err)
		}
		catalogHandler = catalog.NewDevelopmentHTTPHandler(catalogRepository)
		authenticatedRoutes = append(authenticatedRoutes, catalog.AuthenticatedRoutes(catalogRepository)...)

		financeRepository, err := finance.NewRepository(database, finance.WithCashSessionObservability(logger, metrics))
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório financeiro: %w", err)
		}
		financeHandler = finance.NewDevelopmentHTTPHandler(financeRepository)
		authenticatedRoutes = append(authenticatedRoutes, finance.AuthenticatedRoutes(financeRepository)...)
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

		authenticatedRoutes = append(authenticatedRoutes, funeral.AuthenticatedRoutes(funeralRepository)...)

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
		authenticatedRoutes = append(authenticatedRoutes, funeral.AgendaAuthenticatedRoute(funeralRepository, checker))
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
		repository, err := identity.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de identidade: %w", err)
		}
		identityRepository = repository
		var checker funeral.AccessChecker
		if cfg.ControlPlaneDSN != "" {
			controlDatabase, controlErr := db.New(ctx, cfg.ControlPlaneDSN)
			if controlErr != nil {
				closeAll()
				return nil, func() {}, fmt.Errorf("conectar PostgreSQL do control plane: %w", controlErr)
			}
			closers = append(closers, controlDatabase.Close)
			controlChecker, checkerErr := entitlement.NewChecker(controlDatabase, entitlement.WithCompanyResolver(repository))
			if checkerErr != nil {
				closeAll()
				return nil, func() {}, fmt.Errorf("criar verificador de entitlement: %w", checkerErr)
			}
			checker = controlChecker
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
			platformReader, readerErr := controlplane.NewPlatformReader(controlDatabase)
			if readerErr != nil {
				closeAll()
				return nil, func() {}, fmt.Errorf("criar leitor de plataforma: %w", readerErr)
			}
			// ADR 0040: /admin/v1 é uma superfície separada de /v1. Sem o
			// verificador OIDC/MFA de plataforma (pendente), o servidor não monta
			// PlatformAuthenticator e as rotas respondem 503 (falha fechada).
			adminHandler := controlplane.NewAdminHandler(platformReader, entitlementService, logger)
			platformRoutes = append(platformRoutes,
				httpserver.PlatformRoute{Pattern: "GET /admin/v1/tenants", Permission: platformauth.PermissionPlatformRead, Handler: adminHandler.Audited("tenants.list", adminHandler.Tenants)},
				httpserver.PlatformRoute{Pattern: "GET /admin/v1/tenants/{tenantId}/licenses", Permission: platformauth.PermissionPlatformRead, Handler: adminHandler.Audited("licenses.read", adminHandler.Licenses)},
				httpserver.PlatformRoute{Pattern: "GET /admin/v1/tenants/{tenantId}/entitlement-events", Permission: platformauth.PermissionPlatformRead, Handler: adminHandler.Audited("events.read", adminHandler.Events)},
				httpserver.PlatformRoute{Pattern: "PUT /admin/v1/tenants/{tenantId}/companies/{companyId}/entitlements/{module}", Permission: platformauth.PermissionPlatformWrite, Handler: adminHandler.Audited("entitlement.grant", adminHandler.Grant)},
				httpserver.PlatformRoute{Pattern: "POST /admin/v1/tenants/{tenantId}/companies/{companyId}/entitlements/{module}/revoke", Permission: platformauth.PermissionPlatformWrite, Handler: adminHandler.Audited("entitlement.revoke", adminHandler.Revoke)},
				httpserver.PlatformRoute{Pattern: "PUT /admin/v1/tenants/{tenantId}/modules/{module}/quota", Permission: platformauth.PermissionPlatformWrite, Handler: adminHandler.Audited("quota.set", adminHandler.SetQuota)},
			)
		}
		catalogRepository, err := catalog.NewRepository(database)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório de catálogo: %w", err)
		}
		authenticatedRoutes = append(authenticatedRoutes, catalog.AuthenticatedRoutes(catalogRepository)...)

		financeRepository, err := finance.NewRepository(database, finance.WithCashSessionObservability(logger, metrics))
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("criar repositório financeiro: %w", err)
		}
		authenticatedRoutes = append(authenticatedRoutes, finance.AuthenticatedRoutes(financeRepository)...)

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
		authenticatedRoutes = append(authenticatedRoutes, funeral.AuthenticatedRoutes(funeralRepository)...)
		authenticatedRoutes = append(authenticatedRoutes, funeral.AgendaAuthenticatedRoute(funeralRepository, checker))

		partyHandler := party.NewProductionHTTPHandler(partyRepository)
		deceasedHandler := party.NewProductionDeceasedHTTPHandler(partyRepository, policyProvider)
		familyLinkHandler := party.NewProductionFamilyLinkHTTPHandler(partyRepository, policyProvider)
		authenticatedRoutes = append(authenticatedRoutes,
			httpserver.AuthenticatedRoute{Pattern: "POST /v1/parties", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(partyHandler.Create)},
			httpserver.AuthenticatedRoute{Pattern: "GET /v1/parties", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(partyHandler.Search)},
			httpserver.AuthenticatedRoute{Pattern: "POST /v1/deceased", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(deceasedHandler.Create)},
			httpserver.AuthenticatedRoute{Pattern: "GET /v1/deceased/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(deceasedHandler.Get)},
			httpserver.AuthenticatedRoute{Pattern: "POST /v1/deceased/{deceasedId}/family-links", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(familyLinkHandler.Create)},
			httpserver.AuthenticatedRoute{Pattern: "GET /v1/family-links/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(familyLinkHandler.Get)},
		)
		authenticatedRoutes = append(authenticatedRoutes, party.LegalEntityAuthenticatedRoutes(partyRepository, policyProvider)...)
	}

	// Emissor próprio de tenant (ADR 0048/AUTH-01B): só monta quando a chave
	// de assinatura está configurada (EnvKeyProvider, lacuna documentada —
	// produção depende de cofre/KMS real). Sem ela, tenantAuthenticator e
	// loginService continuam nil e /v1/auth/* respondem 503 (fail-closed),
	// sem impedir o boot do processo.
	tenantAuthenticator, loginService := buildTenantAuthentication(cfg, identityRepository, logger)
	var publicRoutes []httpserver.PublicRoute
	var selfServiceRoutes []httpserver.AuthenticatedRoute
	if loginService != nil {
		trustedProxies, err := identity.NewTrustedProxies(cfg.AuthTrustedProxyCIDRs)
		if err != nil {
			// config.validate() já rejeita CIDR inválido no boot; chegar aqui
			// indica um bug de validação, não um AUTH_TRUSTED_PROXY_CIDRS do
			// operador. Falha fechado para o lado seguro (ignora o cabeçalho).
			logger.Warn("AUTH_TRUSTED_PROXY_CIDRS inválido após validação do boot, X-Forwarded-For será sempre ignorado no rate-limit de login", "erro", err)
			trustedProxies = nil
		}
		publicRoutes = identity.PublicLoginRoutes(loginService, trustedProxies)
		selfServiceRoutes = identity.SelfServiceLoginRoutes(loginService)
		authenticatedRoutes = append(authenticatedRoutes, identity.InviteRoutes(loginService)...)
	}

	options := httpserver.Options{
		Readiness: readinessChecker,
		// Authenticator cobre só o emissor próprio de tenant (/v1/auth/*,
		// RequireAuthenticated e demais rotas de negócio autenticadas). O
		// provedor OIDC/MFA de plataforma ainda não foi escolhido nem
		// conectado ao bootstrap: não usar um autenticador sintético aqui,
		// PlatformAuthenticator fica nil e auth.RequirePlatform devolve 503
		// em /admin/v1/* até essa configuração (ADR 0040).
		Authenticator:            tenantAuthenticator,
		AuthenticatedRoutes:      authenticatedRoutes,
		PlatformRoutes:           platformRoutes,
		PublicRoutes:             publicRoutes,
		SelfServiceRoutes:        selfServiceRoutes,
		DevelopmentMode:          devOnboardingEnabled,
		DevelopmentOnboarding:    onboardingHandler,
		DevelopmentParties:       partiesHandler,
		DevelopmentDeceased:      deceasedHandler,
		DevelopmentFamilyLinks:   familyLinksHandler,
		DevelopmentLegalEntities: legalEntitiesHandler,
		DevelopmentCatalog:       catalogHandler,
		DevelopmentFinance:       financeHandler,
		DevelopmentFuneral:       funeralHandler,
	}
	for _, override := range overrides {
		override(&options)
	}
	handler := httpserver.New(logger, options).Handler()

	return handler, closeAll, nil
}

// buildTenantAuthentication monta o emissor próprio de tenant (ADR 0048,
// AUTH-01B) a partir da chave de assinatura configurada no ambiente. Sem
// chave ou política RBAC válidas, devolve (nil, nil): o chamador deixa
// Authenticator/PublicRoutes/SelfServiceRoutes vazios e as rotas de /v1/auth
// e demais rotas autenticadas respondem 503, nunca um caminho permissivo.
func buildTenantAuthentication(cfg config.Config, repository *identity.Repository, logger *slog.Logger) (platformauth.Gate, *identity.LoginService) {
	if repository == nil {
		return nil, nil
	}
	keys, err := platformauth.NewEnvKeyProvider(cfg.AuthSigningKeySeed, cfg.AuthSigningKeyID, cfg.AuthPreviousSigningKeySeed, cfg.AuthPreviousSigningKeyID)
	if err != nil {
		logger.Warn("chave de assinatura do emissor de tenant indisponível, /v1/auth/* responde 503")
		return nil, nil
	}
	issuer, err := platformauth.NewTenantIssuer(keys, 0)
	if err != nil {
		logger.Warn("emissor de tenant indisponível, /v1/auth/* responde 503")
		return nil, nil
	}
	loginService := identity.NewLoginService(repository, issuer)
	policy, err := platformauth.DefaultRolePolicy()
	if err != nil {
		// Devolve loginService nil também: com a chave presente mas a política
		// RBAC indisponível, manter só login/refresh/bootstrap funcionando
		// (PublicRoutes) enquanto logout/revoke/invite ficam 503 por
		// Authenticator nil seria um boot parcial inconsistente. Correção #3
		// do parecer do Analista: /v1/auth/* fica inteiramente 503 (fallback
		// em httpserver, ver routes()) neste estado, nunca parcialmente de pé.
		logger.Warn("política RBAC padrão indisponível com chave de assinatura presente, /v1/auth/* inteiro responde 503 em vez de um boot parcial")
		return nil, nil
	}
	authenticator, err := platformauth.NewService(issuer, nil, repository, repository, policy, logger, platformauth.MFAPolicyDisabled)
	if err != nil {
		logger.Warn("autenticador de tenant indisponível com chave de assinatura presente, /v1/auth/* inteiro responde 503 em vez de um boot parcial")
		return nil, nil
	}
	return authenticator, loginService
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
