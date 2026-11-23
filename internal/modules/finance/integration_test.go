package finance_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/finance"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRepositoryFinanceTenantIsolationAndPaymentBalance(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}
	adminDSN := os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_ADMIN_DSN não definido; teste Finance não criará dados sem cleanup administrativo")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var bypass bool
	var role string
	if err := database.WithTenantTx(db.WithTenant(ctx, uuid.New()), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user, rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&role, &bypass)
	}); err != nil {
		t.Fatal(err)
	}
	if role != "amparius_app" || bypass {
		t.Fatalf("teste precisa usar amparius_app sem BYPASSRLS; role=%s bypass=%v", role, bypass)
	}
	repository, err := finance.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer adminPool.Close()
		cleanupCtx := context.Background()
		tx, cleanupErr := adminPool.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			tenantIDs := []uuid.UUID{tenantA, tenantB}
			for _, statement := range []string{
				"ALTER TABLE finance.payment DISABLE TRIGGER payment_append_only",
				"ALTER TABLE finance.installment DISABLE TRIGGER installment_history_preserve",
				"ALTER TABLE finance.title DISABLE TRIGGER title_history_preserve",
				"ALTER TABLE finance.cash_session DISABLE TRIGGER cash_session_history_preserve",
				"ALTER TABLE finance.cash_movement DISABLE TRIGGER cash_movement_append_only",
				"ALTER TABLE finance.ledger_entry DISABLE TRIGGER ledger_entry_append_only",
			} {
				if _, cleanupErr = tx.Exec(cleanupCtx, statement); cleanupErr != nil {
					break
				}
			}
			for _, statement := range []string{
				"DELETE FROM finance.payment WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.cash_movement WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.ledger_entry WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.installment WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.title WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.cash_session WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.account WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM identity.tenant WHERE id = ANY($1::uuid[])",
			} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, statement, tenantIDs)
			}
			for _, statement := range []string{
				"ALTER TABLE finance.payment ENABLE TRIGGER payment_append_only",
				"ALTER TABLE finance.installment ENABLE TRIGGER installment_history_preserve",
				"ALTER TABLE finance.title ENABLE TRIGGER title_history_preserve",
				"ALTER TABLE finance.cash_session ENABLE TRIGGER cash_session_history_preserve",
				"ALTER TABLE finance.cash_movement ENABLE TRIGGER cash_movement_append_only",
				"ALTER TABLE finance.ledger_entry ENABLE TRIGGER ledger_entry_append_only",
			} {
				if cleanupErr != nil {
					break
				}
				if _, cleanupErr = tx.Exec(cleanupCtx, statement); cleanupErr != nil {
					break
				}
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(cleanupCtx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup administrativo dos tenants Finance: %v", cleanupErr)
			return
		}
		var remaining int
		if err := adminPool.QueryRow(cleanupCtx, `SELECT count(*) FROM identity.tenant WHERE id = ANY($1::uuid[])`, []uuid.UUID{tenantA, tenantB}).Scan(&remaining); err != nil {
			t.Errorf("confirmar cleanup Finance: %v", err)
		} else if remaining != 0 {
			t.Errorf("cleanup Finance deixou %d tenant(s) residuais", remaining)
		}
	})
	createTenant := func(tenantID uuid.UUID) {
		err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,$4,$5)`, tenantID, "Finance "+tenantID.String(), "doc-"+tenantID.String(), "economic_group", "active")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	createTenant(tenantA)
	createTenant(tenantB)
	ctxA := db.WithTenant(ctx, tenantA)
	ctxB := db.WithTenant(ctx, tenantB)
	account, err := repository.CreateAccount(ctxA, finance.Account{Code: "1.1", Name: "Caixa", Type: "asset", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetAccountByID(ctxA, account.ID); err != nil {
		t.Fatalf("conta Create/GetByID: %v", err)
	}
	if _, err := repository.GetAccountByID(ctxB, account.ID); !errors.Is(err, finance.ErrNotFound) {
		t.Fatalf("isolamento esperado ErrNotFound, recebido %v", err)
	}
	title, err := repository.CreateTitle(ctxA, finance.Title{Direction: "receivable", AmountCents: 10000, DueDate: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), Status: "open", Origin: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetTitleByID(ctxA, title.ID); err != nil {
		t.Fatalf("título Create/GetByID: %v", err)
	}
	installment, err := repository.CreateInstallment(ctxA, finance.Installment{TitleID: title.ID, Number: 1, AmountCents: 10000, DueDate: title.DueDate, Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreatePayment(ctxA, finance.Payment{InstallmentID: installment.ID, AmountCents: 6000, Method: "pix"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreatePayment(ctxA, finance.Payment{InstallmentID: installment.ID, AmountCents: 5000, Method: "pix"}); err == nil {
		t.Fatal("pagamento acima do saldo deveria falhar")
	}
	if rows, err := repository.ListTitles(ctxB, 10); err != nil || len(rows) != 0 {
		t.Fatalf("listagem isolada: rows=%d err=%v", len(rows), err)
	}
}
