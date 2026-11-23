package party_test

import (
	"context"
	"os"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/party"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRepositoryTenantIsolationAndCreateGet(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}

	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository, err := party.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	createTenant := func(tenantID uuid.UUID) {
		err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id, name, document, regime, status) VALUES ($1,$2,$3,$4,$5)`, tenantID, "Teste "+tenantID.String(), "doc-"+tenantID.String(), "economic_group", "active")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	createTenant(tenantA)
	createTenant(tenantB)

	created, err := repository.Create(db.WithTenant(ctx, tenantA), party.Party{Kind: party.KindPerson, Name: "Pessoa A", Document: "111", Status: party.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByID(db.WithTenant(ctx, tenantA), created.ID); err != nil {
		t.Fatalf("Create/GetByID: %v", err)
	}
	if _, err := repository.GetByID(db.WithTenant(ctx, tenantB), created.ID); err != party.ErrNotFound {
		t.Fatalf("isolamento esperado ErrNotFound, recebido %v", err)
	}
}
