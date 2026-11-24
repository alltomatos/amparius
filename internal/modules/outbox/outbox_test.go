package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

type noopHandler struct{}

func (noopHandler) Handle(context.Context, outbox.Event) error { return nil }

func TestBackoffGrowsExponentiallyAndCaps(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 2 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{10, 1024 * time.Second},
		{20, time.Hour},
		{21, time.Hour},
		{1000, time.Hour},
	}
	for _, tc := range cases {
		if got := outbox.Backoff(tc.attempts); got != tc.want {
			t.Errorf("Backoff(%d) = %s, quer %s", tc.attempts, got, tc.want)
		}
	}
}

func TestNewDispatcherValidatesDependenciesAndLimits(t *testing.T) {
	repository, err := outbox.NewRepository(nil)
	if err == nil || repository != nil {
		t.Fatal("repositório sem banco deveria falhar")
	}
	if _, err := outbox.NewDispatcher(nil, noopHandler{}, outbox.DispatcherConfig{}); err == nil {
		t.Fatal("dispatcher sem repositório deveria falhar")
	}
	repository, err = outbox.NewRepository(&db.DB{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.NewDispatcher(repository, nil, outbox.DispatcherConfig{}); err == nil {
		t.Fatal("dispatcher sem handler deveria falhar")
	}
	invalid := []outbox.DispatcherConfig{
		{Batch: -1},
		{MaxTenants: -1},
		{Lease: -time.Second},
		{Interval: -time.Second},
	}
	for _, config := range invalid {
		if _, err := outbox.NewDispatcher(repository, noopHandler{}, config); err == nil {
			t.Errorf("configuração %+v deveria falhar", config)
		}
	}
	if _, err := outbox.NewDispatcher(repository, noopHandler{}, outbox.DispatcherConfig{}); err != nil {
		t.Fatalf("configuração padrão deveria ser válida: %v", err)
	}
}

func TestAppendRejectsMissingOrForeignTenant(t *testing.T) {
	repository, err := outbox.NewRepository(&db.DB{})
	if err != nil {
		t.Fatal(err)
	}
	valid := outbox.Event{
		AggregateType:  "finance.title",
		AggregateID:    uuid.UUID{1},
		EventType:      "finance.title.created",
		Version:        1,
		Payload:        []byte(`{"amount":1}`),
		IdempotencyKey: "k1",
	}
	if _, err := repository.Append(context.Background(), valid); !errors.Is(err, db.ErrTenantRequired) {
		t.Fatalf("tenant ausente deveria falhar antes do banco: %v", err)
	}
	ctx := db.WithTenant(context.Background(), uuid.New())
	foreign := valid
	foreign.TenantID = uuid.New()
	if _, err := repository.Append(ctx, foreign); !errors.Is(err, outbox.ErrTenantMismatch) {
		t.Fatalf("tenant estrangeiro deveria falhar antes do banco: %v", err)
	}
}
