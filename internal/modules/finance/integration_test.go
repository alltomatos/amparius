package finance_test

import (
	"context"
	"errors"
	"os"
	"sync"
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
	financeTables := []string{
		"account", "cost_center", "bank_account", "title", "installment",
		"payment", "cash_session", "cash_movement", "ledger_entry",
	}
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
	var hasFinanceUsage bool
	if err := database.WithTenantTx(db.WithTenant(ctx, uuid.New()), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT has_schema_privilege(current_user, 'finance', 'USAGE')`).Scan(&hasFinanceUsage)
	}); err != nil {
		t.Fatal(err)
	}
	if !hasFinanceUsage {
		t.Fatal("amparius_app deve ter USAGE no schema finance")
	}
	for _, table := range financeTables {
		var canSelect, canInsert, canUpdate, canDelete bool
		if err := database.WithTenantTx(db.WithTenant(ctx, uuid.New()), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT has_table_privilege(current_user, $1, 'SELECT'),
				       has_table_privilege(current_user, $1, 'INSERT'),
				       has_table_privilege(current_user, $1, 'UPDATE'),
				       has_table_privilege(current_user, $1, 'DELETE')`, "finance."+table).
				Scan(&canSelect, &canInsert, &canUpdate, &canDelete)
		}); err != nil {
			t.Fatalf("privilégios finance.%s: %v", table, err)
		}
		wantUpdate := table == "installment"
		if !canSelect || !canInsert || canUpdate != wantUpdate || canDelete {
			t.Fatalf("grants inesperados em finance.%s: select=%v insert=%v update=%v delete=%v", table, canSelect, canInsert, canUpdate, canDelete)
		}
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
	for _, column := range []struct {
		table string
		name  string
	}{
		{"title", "amount"},
		{"installment", "amount"},
		{"payment", "amount"},
		{"cash_session", "opening_amount"},
		{"cash_movement", "amount"},
		{"ledger_entry", "debit"},
		{"ledger_entry", "credit"},
	} {
		var dataType string
		var precision, scale int
		err := adminPool.QueryRow(ctx, `
			SELECT data_type, numeric_precision, numeric_scale
			FROM information_schema.columns
			WHERE table_schema = 'finance' AND table_name = $1 AND column_name = $2`, column.table, column.name).
			Scan(&dataType, &precision, &scale)
		if err != nil {
			t.Fatalf("metadado monetario finance.%s.%s: %v", column.table, column.name, err)
		}
		if dataType != "numeric" || precision != 18 || scale != 2 {
			t.Fatalf("finance.%s.%s = %s(%d,%d), esperado NUMERIC(18,2)", column.table, column.name, dataType, precision, scale)
		}
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
				"DELETE FROM finance.bank_account WHERE tenant_id = ANY($1::uuid[])",
				"DELETE FROM finance.cost_center WHERE tenant_id = ANY($1::uuid[])",
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
	var costCenterID, bankAccountID uuid.UUID
	if err := database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO finance.cost_center (tenant_id, code, name, status)
			VALUES ($1, $2, $3, $4) RETURNING id`, tenantA, "CC-001", "Operacao", "active").Scan(&costCenterID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO finance.bank_account (tenant_id, name, kind, status)
			VALUES ($1, $2, $3, $4) RETURNING id`, tenantA, "Conta principal", "checking", "active").Scan(&bankAccountID)
	}); err != nil {
		t.Fatalf("criar centro de custo e conta bancaria: %v", err)
	}
	if costCenterID == uuid.Nil || bankAccountID == uuid.Nil {
		t.Fatal("finance.cost_center e finance.bank_account devem gerar IDs")
	}
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
	if _, err := repository.CreatePayment(ctxA, finance.Payment{InstallmentID: installment.ID, BankAccountID: bankAccountID, AmountCents: 6000, Method: "pix"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreatePayment(ctxA, finance.Payment{InstallmentID: installment.ID, AmountCents: 5000, Method: "pix"}); err == nil {
		t.Fatal("pagamento acima do saldo deveria falhar")
	}
	if rows, err := repository.ListTitles(ctxB, 10); err != nil || len(rows) != 0 {
		t.Fatalf("listagem isolada: rows=%d err=%v", len(rows), err)
	}
	cashSession, err := repository.CreateCashSession(ctxA, finance.CashSession{OpeningAmountCents: 10000, Status: "open"})
	if err != nil {
		t.Fatalf("criar sessao de caixa: %v", err)
	}
	if _, err := repository.CreateCashMovement(ctxA, finance.CashMovement{SessionID: cashSession.ID, Kind: "in", AmountCents: 1000, Reason: "suprimento"}); err != nil {
		t.Fatalf("criar movimento de caixa: %v", err)
	}
	if _, err := repository.CreateLedgerEntry(ctxA, finance.LedgerEntry{AccountID: account.ID, Competence: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), DebitCents: 10000, Source: "test"}); err != nil {
		t.Fatalf("criar debito no ledger: %v", err)
	}
	if _, err := repository.CreateLedgerEntry(ctxA, finance.LedgerEntry{AccountID: account.ID, Competence: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), CreditCents: 10000, Source: "test"}); err != nil {
		t.Fatalf("criar credito no ledger: %v", err)
	}
	ledgerRows, err := repository.ListLedgerEntries(ctxA, account.ID, 10)
	if err != nil || len(ledgerRows) != 2 {
		t.Fatalf("ledger balanceado: rows=%d err=%v", len(ledgerRows), err)
	}
	var debitTotal, creditTotal int64
	for _, row := range ledgerRows {
		debitTotal += row.DebitCents
		creditTotal += row.CreditCents
	}
	if debitTotal != creditTotal {
		t.Fatalf("ledger desequilibrado: debito=%d credito=%d", debitTotal, creditTotal)
	}

	// O RLS deve esconder as linhas de todas as tabelas, nao apenas das
	// operacoes expostas pelo repositorio.
	for _, table := range financeTables {
		var count int
		if err := database.WithTenantTx(ctxB, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM finance."+table).Scan(&count)
		}); err != nil {
			t.Fatalf("consultar RLS em finance.%s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("RLS vazou %d linha(s) de finance.%s para tenant B", count, table)
		}
	}

	// Duas baixas concorrentes nao podem consumir o mesmo saldo.
	titleConcurrent, err := repository.CreateTitle(ctxA, finance.Title{Direction: "receivable", AmountCents: 10000, DueDate: time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC), Status: "open", Origin: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	installmentConcurrent, err := repository.CreateInstallment(ctxA, finance.Installment{TitleID: titleConcurrent.ID, Number: 1, AmountCents: 10000, DueDate: titleConcurrent.DueDate, Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := repository.CreatePayment(ctxA, finance.Payment{InstallmentID: installmentConcurrent.ID, AmountCents: 6000, Method: "pix"})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("sobrepagamento concorrente: sucessos=%d falhas=%d", successes, failures)
	}

	// O papel da aplicacao nao pode mutar registros protegidos. Os triggers
	// tambem sao exercitados com a conexao administrativa, que tem privilegio
	// para chegar ao trigger e nao apenas falhar por falta de grant.
	for _, mutation := range []struct {
		name  string
		query string
		args  []any
	}{
		{"payment update", "UPDATE finance.payment SET reference = 'blocked' WHERE id = $1", []any{uuid.Nil}},
		{"cash movement delete", "DELETE FROM finance.cash_movement WHERE id = $1", []any{uuid.Nil}},
		{"ledger delete", "DELETE FROM finance.ledger_entry WHERE id = $1 AND competence = $2", []any{uuid.Nil, time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)}},
		{"title delete", "DELETE FROM finance.title WHERE id = $1", []any{uuid.Nil}},
		{"installment delete", "DELETE FROM finance.installment WHERE id = $1", []any{uuid.Nil}},
		{"cash session delete", "DELETE FROM finance.cash_session WHERE id = $1", []any{uuid.Nil}},
	} {
		if err := database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, mutation.query, mutation.args...)
			return err
		}); err == nil {
			t.Fatalf("mutacao %s deveria ser recusada pelo papel da aplicacao", mutation.name)
		}
	}

	for _, mutation := range []struct {
		name  string
		query string
		args  []any
	}{
		{"payment trigger", "UPDATE finance.payment SET reference = 'blocked' WHERE tenant_id = $1 AND id = (SELECT id FROM finance.payment WHERE tenant_id = $1 LIMIT 1)", []any{tenantA}},
		{"cash movement trigger", "DELETE FROM finance.cash_movement WHERE tenant_id = $1 AND id = (SELECT id FROM finance.cash_movement WHERE tenant_id = $1 LIMIT 1)", []any{tenantA}},
		{"ledger trigger", "DELETE FROM finance.ledger_entry WHERE tenant_id = $1 AND id = (SELECT id FROM finance.ledger_entry WHERE tenant_id = $1 LIMIT 1)", []any{tenantA}},
		{"title trigger", "DELETE FROM finance.title WHERE tenant_id = $1 AND id = (SELECT id FROM finance.title WHERE tenant_id = $1 LIMIT 1)", []any{tenantA}},
		{"installment trigger", "DELETE FROM finance.installment WHERE tenant_id = $1 AND id = (SELECT id FROM finance.installment WHERE tenant_id = $1 LIMIT 1)", []any{tenantA}},
		{"cash session trigger", "DELETE FROM finance.cash_session WHERE tenant_id = $1 AND id = (SELECT id FROM finance.cash_session WHERE tenant_id = $1 LIMIT 1)", []any{tenantA}},
	} {
		tx, err := adminPool.Begin(ctx)
		if err != nil {
			t.Fatalf("iniciar teste de %s: %v", mutation.name, err)
		}
		_, execErr := tx.Exec(ctx, mutation.query, mutation.args...)
		_ = tx.Rollback(ctx)
		if execErr == nil {
			t.Fatalf("trigger %s deveria recusar a mutacao", mutation.name)
		}
	}
}
