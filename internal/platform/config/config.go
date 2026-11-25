// Package config carrega a configuração da aplicação a partir do ambiente.
//
// Nesta fase nenhuma conexão é aberta: os DSNs e endereços são apenas
// carregados e validados. Valores padrão são de desenvolvimento local e
// apontam para o docker-compose do repositório.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// ErrInvalidConfig classifica falhas de configuração.
var ErrInvalidConfig = errors.New("configuração inválida")

const (
	defaultEnv             = "development"
	defaultHTTPPort        = "8080"
	defaultLogLevel        = "info"
	defaultShutdownTimeout = 15 * time.Second

	defaultPostgresDSN = "postgres://amparius:amparius@localhost:5432/amparius?sslmode=disable"
	defaultRedisAddr   = "localhost:6379"
	defaultRabbitMQURL = "amqp://amparius:amparius@localhost:5672/"
	defaultMinIOEnd    = "localhost:9000"
	defaultMinIOBucket = "amparius"
)

// Config é a configuração imutável da aplicação.
//
// Contém potenciais segredos (DSNs, chaves); não registre a struct inteira em
// log. Logue apenas os campos não sensíveis que interessarem.
type Config struct {
	Env                 string
	HTTPPort            string
	LogLevel            string
	ShutdownTimeout     time.Duration
	EnableDevOnboarding bool

	// Dependências externas: carregadas, ainda não conectadas.
	PostgresDSN string
	// ControlPlaneDSN é separado do banco da célula e só é usado pelo
	// enforcement de entitlement quando a superfície correspondente está ativa.
	ControlPlaneDSN string
	RedisAddr       string
	RabbitMQURL     string

	MinIOEndpoint  string
	MinIOAccessKey string
	MinIOSecretKey string
	MinIOBucket    string
	MinIOUseSSL    bool
}

// Load lê a configuração do ambiente do processo.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LookupFunc abstrai a leitura de variáveis de ambiente para testes.
type LookupFunc func(key string) (string, bool)

// LoadFrom lê a configuração a partir da função de lookup informada.
func LoadFrom(lookup LookupFunc) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("%w: função de lookup nula", ErrInvalidConfig)
	}

	shutdownTimeout, err := duration(lookup, "HTTP_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}

	useSSL, err := boolean(lookup, "MINIO_USE_SSL", false)
	if err != nil {
		return Config{}, err
	}

	enableDevOnboarding, err := boolean(lookup, "ENABLE_DEV_ONBOARDING", false)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Env:                 text(lookup, "APP_ENV", defaultEnv),
		HTTPPort:            text(lookup, "HTTP_PORT", defaultHTTPPort),
		LogLevel:            text(lookup, "LOG_LEVEL", defaultLogLevel),
		ShutdownTimeout:     shutdownTimeout,
		EnableDevOnboarding: enableDevOnboarding,

		PostgresDSN:     text(lookup, "POSTGRES_DSN", defaultPostgresDSN),
		ControlPlaneDSN: text(lookup, "AMPA_CONTROL_POSTGRES_DSN", ""),
		RedisAddr:       text(lookup, "REDIS_ADDR", defaultRedisAddr),
		RabbitMQURL:     text(lookup, "RABBITMQ_URL", defaultRabbitMQURL),

		MinIOEndpoint:  text(lookup, "MINIO_ENDPOINT", defaultMinIOEnd),
		MinIOAccessKey: text(lookup, "MINIO_ACCESS_KEY", ""),
		MinIOSecretKey: text(lookup, "MINIO_SECRET_KEY", ""),
		MinIOBucket:    text(lookup, "MINIO_BUCKET", defaultMinIOBucket),
		MinIOUseSSL:    useSSL,
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// HTTPAddr devolve o endereço de escuta do servidor HTTP.
func (c Config) HTTPAddr() string {
	return net.JoinHostPort("", c.HTTPPort)
}

func (c Config) validate() error {
	port, err := strconv.Atoi(c.HTTPPort)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%w: HTTP_PORT deve ser uma porta entre 1 e 65535, recebido %q", ErrInvalidConfig, c.HTTPPort)
	}
	if c.PostgresDSN == "" {
		return fmt.Errorf("%w: POSTGRES_DSN não pode ser vazio", ErrInvalidConfig)
	}
	if c.RedisAddr == "" {
		return fmt.Errorf("%w: REDIS_ADDR não pode ser vazio", ErrInvalidConfig)
	}
	if c.RabbitMQURL == "" {
		return fmt.Errorf("%w: RABBITMQ_URL não pode ser vazio", ErrInvalidConfig)
	}
	return nil
}

func text(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func duration(lookup LookupFunc, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %s inválido (%q): %v", ErrInvalidConfig, key, value, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%w: %s deve ser positivo, recebido %q", ErrInvalidConfig, key, value)
	}
	return parsed, nil
}

func boolean(lookup LookupFunc, key string, fallback bool) (bool, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%w: %s inválido (%q): %v", ErrInvalidConfig, key, value, err)
	}
	return parsed, nil
}
