package main

import (
	"strings"
	"testing"
	"time"
)

func TestWorkerConfigFromEnvironment(t *testing.T) {
	lookup := func(key string) (string, bool) {
		values := map[string]string{
			"POSTGRES_DSN":            "postgres://amparius_app:senha@127.0.0.1:5432/amparius?sslmode=disable",
			"OUTBOX_BATCH":            "25",
			"OUTBOX_MAX_TENANTS":      "40",
			"OUTBOX_LEASE":            "90s",
			"OUTBOX_INTERVAL":         "3s",
			"OUTBOX_DELIVERY_HANDLER": "delivery",
		}
		value, ok := values[key]
		return value, ok
	}
	cfg, err := workerConfigFrom(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Batch != 25 || cfg.MaxTenants != 40 || cfg.Lease != 90*time.Second || cfg.Interval != 3*time.Second {
		t.Fatalf("config inesperada: %+v", cfg)
	}
	if cfg.Handler != "delivery" {
		t.Fatalf("handler: %q", cfg.Handler)
	}
	// O DSN nunca aparece em representação de log nem em erro de validação.
	if strings.Contains(cfg.String(), "senha") || strings.Contains(cfg.String(), "amparius_app") {
		t.Fatalf("String() vazou segredo: %s", cfg.String())
	}
}

func TestWorkerConfigDefaultsAreSafe(t *testing.T) {
	cfg, err := workerConfigFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Batch <= 0 || cfg.Batch > 1000 || cfg.MaxTenants <= 0 || cfg.MaxTenants > 1000 {
		t.Fatalf("defaults fora do limite: %+v", cfg)
	}
	if cfg.Lease <= 0 || cfg.Interval <= 0 {
		t.Fatalf("defaults de tempo inválidos: %+v", cfg)
	}
	if cfg.Handler != "delivery" {
		t.Fatalf("handler padrão deveria ser entrega real, obtido %q", cfg.Handler)
	}
}

func TestWorkerConfigRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
	}{
		{"batch acima do teto", map[string]string{"OUTBOX_BATCH": "1001"}},
		{"batch zero explicito", map[string]string{"OUTBOX_BATCH": "0"}},
		{"max_tenants negativo", map[string]string{"OUTBOX_MAX_TENANTS": "-1"}},
		{"lease invalido", map[string]string{"OUTBOX_LEASE": "abc"}},
		{"lease menor que intervalo", map[string]string{"OUTBOX_LEASE": "1s", "OUTBOX_INTERVAL": "30s"}},
		{"intervalo negativo", map[string]string{"OUTBOX_INTERVAL": "-5s"}},
		{"handler desconhecido", map[string]string{"OUTBOX_DELIVERY_HANDLER": "magic"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := workerConfigFrom(func(key string) (string, bool) {
				value, ok := tc.values[key]
				return value, ok
			})
			if err == nil {
				t.Fatalf("config %q deveria falhar", tc.name)
			}
			if strings.Contains(err.Error(), "senha") {
				t.Fatalf("erro vazou segredo: %v", err)
			}
		})
	}
}

func TestWorkerConfigStringNeverIncludesDSN(t *testing.T) {
	cfg, err := workerConfigFrom(func(key string) (string, bool) {
		if key == "POSTGRES_DSN" {
			return "postgres://user:supersegredo@host/db", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg.String(), "supersegredo") {
		t.Fatalf("String() vazou DSN: %s", cfg.String())
	}
}
