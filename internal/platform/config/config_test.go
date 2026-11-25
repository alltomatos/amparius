package config

import (
	"errors"
	"testing"
	"time"
)

func lookupFrom(values map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestLoadFromUsaPadroesDeDesenvolvimento(t *testing.T) {
	cfg, err := LoadFrom(lookupFrom(nil))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if cfg.HTTPPort != "8080" {
		t.Fatalf("HTTPPort = %q, quer %q", cfg.HTTPPort, "8080")
	}
	if cfg.HTTPAddr() != ":8080" {
		t.Fatalf("HTTPAddr() = %q, quer %q", cfg.HTTPAddr(), ":8080")
	}
	if cfg.Env != "development" {
		t.Fatalf("Env = %q, quer %q", cfg.Env, "development")
	}
	if cfg.EnableDevOnboarding {
		t.Fatal("EnableDevOnboarding deveria ser false por padrão")
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Fatalf("ShutdownTimeout = %v, quer %v", cfg.ShutdownTimeout, 15*time.Second)
	}
	if cfg.ReadinessTimeout != 2*time.Second {
		t.Fatalf("ReadinessTimeout = %v, quer %v", cfg.ReadinessTimeout, 2*time.Second)
	}
	if cfg.PostgresDSN == "" || cfg.RedisAddr == "" || cfg.RabbitMQURL == "" {
		t.Fatal("DSNs padrão não deveriam ser vazios")
	}
}

func TestLoadFromSobrescreveComAmbiente(t *testing.T) {
	cfg, err := LoadFrom(lookupFrom(map[string]string{
		"HTTP_PORT":                     "9090",
		"APP_ENV":                       "test",
		"LOG_LEVEL":                     "debug",
		"HTTP_SHUTDOWN_TIMEOUT":         "30s",
		"READINESS_TIMEOUT":             "750ms",
		"OBJECT_STORAGE_USE_PATH_STYLE": "true",
		"OBJECT_STORAGE_ENDPOINT":       "https://objects.example.test",
		"OBJECT_STORAGE_REGION":         "auto",
		"OBJECT_STORAGE_BUCKET":         "amparius-test",
		"OBJECT_STORAGE_ACCESS_KEY":     "access",
		"OBJECT_STORAGE_SECRET_KEY":     "secret",
		"ENABLE_DEV_ONBOARDING":         "true",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if cfg.HTTPAddr() != ":9090" {
		t.Fatalf("HTTPAddr() = %q, quer %q", cfg.HTTPAddr(), ":9090")
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, quer %q", cfg.LogLevel, "debug")
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("ShutdownTimeout = %v, quer %v", cfg.ShutdownTimeout, 30*time.Second)
	}
	if cfg.ReadinessTimeout != 750*time.Millisecond {
		t.Fatalf("ReadinessTimeout = %v, quer %v", cfg.ReadinessTimeout, 750*time.Millisecond)
	}
	if !cfg.ObjectStorageUsePathStyle {
		t.Fatal("ObjectStorageUsePathStyle deveria ser true")
	}
	if cfg.ObjectStorageEndpoint != "https://objects.example.test" || cfg.ObjectStorageBucket != "amparius-test" {
		t.Fatalf("configuração object storage inesperada: endpoint=%q bucket=%q", cfg.ObjectStorageEndpoint, cfg.ObjectStorageBucket)
	}
	if !cfg.EnableDevOnboarding {
		t.Fatal("EnableDevOnboarding deveria ser true")
	}
}

func TestLoadFromRejeitaOptInDevInvalido(t *testing.T) {
	_, err := LoadFrom(lookupFrom(map[string]string{"ENABLE_DEV_ONBOARDING": "talvez"}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, quer ErrInvalidConfig", err)
	}
}

func TestLoadFromRejeitaPortaInvalida(t *testing.T) {
	for _, port := range []string{"abc", "0", "65536", "-1"} {
		_, err := LoadFrom(lookupFrom(map[string]string{"HTTP_PORT": port}))
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("porta %q: err = %v, quer ErrInvalidConfig", port, err)
		}
	}
}

func TestLoadFromRejeitaDuracaoInvalida(t *testing.T) {
	_, err := LoadFrom(lookupFrom(map[string]string{"HTTP_SHUTDOWN_TIMEOUT": "logo"}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, quer ErrInvalidConfig", err)
	}
}
