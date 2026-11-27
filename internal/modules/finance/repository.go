// Package finance implementa o acesso explícito às tabelas do módulo financeiro.
package finance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound              = errors.New("registro financeiro não encontrado")
	ErrInvalidAmount         = errors.New("valor monetário inválido")
	ErrTenantMismatch        = errors.New("tenant da entidade diverge do contexto")
	ErrInvalidIdempotencyKey = errors.New("chave de idempotencia invalida")
	ErrIdempotencyConflict   = errors.New("chave de idempotencia reutilizada com conteudo diferente")
)

type Account struct {
	ID, TenantID, ParentID   uuid.UUID
	Code, Name, Type, Status string
}
type Title struct {
	ID, TenantID, PartyID                       uuid.UUID
	Direction                                   string
	AmountCents                                 int64
	DueDate                                     time.Time
	Status, Origin, Description, IdempotencyKey string
}
type Installment struct {
	ID, TenantID, TitleID uuid.UUID
	Number                int
	AmountCents           int64
	DueDate               time.Time
	Status                string
}
type InstallmentBalance struct {
	Installment
	PaidCents    int64
	BalanceCents int64
}
type TitleBalance struct {
	AmountCents  int64
	PaidCents    int64
	BalanceCents int64
}
type Payment struct {
	ID, TenantID, InstallmentID, BankAccountID uuid.UUID
	AmountCents                                int64
	PaidAt                                     time.Time
	Method, Reference, IdempotencyKey          string
}
type CashSession struct {
	ID, TenantID, UserID uuid.UUID
	OpenedAt, ClosedAt   *time.Time
	OpeningAmountCents   int64
	Status               string
}
type CashMovement struct {
	ID, TenantID, SessionID uuid.UUID
	Kind                    string
	AmountCents             int64
	Reason, IdempotencyKey  string
	OccurredAt              time.Time
}
type LedgerEntry struct {
	ID, TenantID, AccountID, SourceID uuid.UUID
	Competence                        time.Time
	DebitCents, CreditCents           int64
	Source                            string
}

type Repository struct{ database *db.DB }

func NewRepository(database *db.DB) (*Repository, error) {
	if database == nil {
		return nil, errors.New("banco nulo")
	}
	return &Repository{database: database}, nil
}

func tenantFor(ctx context.Context, entityTenant uuid.UUID) (uuid.UUID, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if entityTenant != uuid.Nil && entityTenant != tenantID {
		return uuid.Nil, ErrTenantMismatch
	}
	return tenantID, nil
}

func validateAmount(cents int64, positive bool) error {
	if positive && cents <= 0 {
		return ErrInvalidAmount
	}
	if !positive && cents < 0 {
		return ErrInvalidAmount
	}
	return nil
}

func centsToNumeric(cents int64) (string, error) {
	if cents == math.MinInt64 {
		return "", ErrInvalidAmount
	}
	negative := cents < 0
	if negative {
		cents = -cents
	}
	value := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if negative {
		value = "-" + value
	}
	return value, nil
}

func numericToCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, ErrInvalidAmount
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	if value == "" || strings.HasPrefix(value, "+") {
		return 0, ErrInvalidAmount
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, ErrInvalidAmount
	}
	frac := "00"
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) > 2 {
			return 0, ErrInvalidAmount
		}
	}
	frac += strings.Repeat("0", 2-len(frac))
	whole, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, ErrInvalidAmount
	}
	fraction, err := strconv.ParseUint(frac, 10, 64)
	if err != nil {
		return 0, ErrInvalidAmount
	}
	if whole > uint64(math.MaxInt64)/100 || whole*100 > uint64(math.MaxInt64)-fraction {
		return 0, ErrInvalidAmount
	}
	result := int64(whole*100 + fraction)
	if negative {
		result = -result
	}
	return result, nil
}

func optionalUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
func optionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func idempotencyKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 200 {
		return "", ErrInvalidIdempotencyKey
	}
	return value, nil
}
func listLimit(limit int) int {
	if limit <= 0 || limit > 1000 {
		return 100
	}
	return limit
}

func (r *Repository) CreateAccount(ctx context.Context, account Account) (Account, error) {
	tenantID, err := tenantFor(ctx, account.TenantID)
	if err != nil {
		return Account{}, err
	}
	account.TenantID = tenantID
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO finance.account (tenant_id,parent_id,code,name,type,status) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, tenantID, optionalUUID(account.ParentID), account.Code, account.Name, account.Type, account.Status).Scan(&account.ID)
	})
	if err != nil {
		return Account{}, fmt.Errorf("criar conta: %w", err)
	}
	return account, nil
}

func (r *Repository) GetAccountByID(ctx context.Context, id uuid.UUID) (Account, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Account{}, err
	}
	var result Account
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,tenant_id,COALESCE(parent_id,'00000000-0000-0000-0000-000000000000'::uuid),code,name,type,status FROM finance.account WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan(&result.ID, &result.TenantID, &result.ParentID, &result.Code, &result.Name, &result.Type, &result.Status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("buscar conta: %w", err)
	}
	return result, nil
}

func (r *Repository) ListAccounts(ctx context.Context, limit int) ([]Account, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result := []Account{}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,tenant_id,COALESCE(parent_id,'00000000-0000-0000-0000-000000000000'::uuid),code,name,type,status FROM finance.account WHERE tenant_id=$1 ORDER BY code,id LIMIT $2`, tenantID, listLimit(limit))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Account
			if err := rows.Scan(&item.ID, &item.TenantID, &item.ParentID, &item.Code, &item.Name, &item.Type, &item.Status); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar contas: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateTitle(ctx context.Context, title Title) (Title, error) {
	if strings.TrimSpace(title.IdempotencyKey) != "" {
		return r.CreateTitleIdempotent(ctx, title.IdempotencyKey, title)
	}
	tenantID, err := tenantFor(ctx, title.TenantID)
	if err != nil {
		return Title{}, err
	}
	amount, err := centsToNumeric(title.AmountCents)
	if err != nil {
		return Title{}, err
	}
	title.TenantID = tenantID
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO finance.title (tenant_id,direction,party_id,amount,due_date,status,origin,description,idempotency_key) VALUES ($1,$2,$3,$4::numeric,$5,$6,$7,$8,$9) RETURNING id`, tenantID, title.Direction, optionalUUID(title.PartyID), amount, title.DueDate, title.Status, title.Origin, optionalString(title.Description), optionalString(title.IdempotencyKey)).Scan(&title.ID)
	})
	if err != nil {
		return Title{}, fmt.Errorf("criar título: %w", err)
	}
	return title, nil
}

// CreateTitleIdempotent cria ou devolve o mesmo título para uma chave estável.
// A comparação impede que uma chave de evento seja reutilizada para outra
// cobrança.
func (r *Repository) CreateTitleIdempotent(ctx context.Context, key string, title Title) (Title, error) {
	tenantID, err := tenantFor(ctx, title.TenantID)
	if err != nil {
		return Title{}, err
	}
	key, err = idempotencyKey(key)
	if err != nil {
		return Title{}, err
	}
	amount, err := centsToNumeric(title.AmountCents)
	if err != nil {
		return Title{}, err
	}
	title.TenantID, title.IdempotencyKey = tenantID, key
	requested := title
	var storedAmount, storedDescription string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		insert := `INSERT INTO finance.title
			(tenant_id,direction,party_id,amount,due_date,status,origin,description,idempotency_key)
			VALUES ($1,$2,$3,$4::numeric,$5,$6,$7,$8,$9)
			ON CONFLICT (tenant_id,idempotency_key) DO NOTHING
			RETURNING id,tenant_id,COALESCE(party_id,'00000000-0000-0000-0000-000000000000'::uuid),direction,amount::text,due_date,status,origin,COALESCE(description,''),idempotency_key`
		err := tx.QueryRow(ctx, insert, tenantID, title.Direction, optionalUUID(title.PartyID), amount, title.DueDate, title.Status, title.Origin, optionalString(title.Description), key).
			Scan(&title.ID, &title.TenantID, &title.PartyID, &title.Direction, &storedAmount, &title.DueDate, &title.Status, &title.Origin, &storedDescription, &title.IdempotencyKey)
		if err == nil {
			title.Description = storedDescription
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id,tenant_id,COALESCE(party_id,'00000000-0000-0000-0000-000000000000'::uuid),direction,amount::text,due_date,status,origin,COALESCE(description,''),idempotency_key FROM finance.title WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, key).
			Scan(&title.ID, &title.TenantID, &title.PartyID, &title.Direction, &storedAmount, &title.DueDate, &title.Status, &title.Origin, &storedDescription, &title.IdempotencyKey)
	})
	if err != nil {
		return Title{}, fmt.Errorf("criar título idempotente: %w", err)
	}
	storedCents, err := numericToCents(storedAmount)
	if err != nil {
		return Title{}, fmt.Errorf("converter título: %w", err)
	}
	if title.Direction != requested.Direction || title.PartyID != requested.PartyID ||
		title.DueDate.UTC().Format("2006-01-02") != requested.DueDate.UTC().Format("2006-01-02") || title.Status != requested.Status ||
		title.Origin != requested.Origin || storedCents != requested.AmountCents ||
		storedDescription != requested.Description {
		return Title{}, ErrIdempotencyConflict
	}
	title.AmountCents = storedCents
	title.Description = storedDescription
	return title, nil
}

func (r *Repository) GetTitleByID(ctx context.Context, id uuid.UUID) (Title, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Title{}, err
	}
	var result Title
	var amount string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,tenant_id,COALESCE(party_id,'00000000-0000-0000-0000-000000000000'::uuid),direction,amount::text,due_date,status,origin,COALESCE(description,''),COALESCE(idempotency_key,'') FROM finance.title WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan(&result.ID, &result.TenantID, &result.PartyID, &result.Direction, &amount, &result.DueDate, &result.Status, &result.Origin, &result.Description, &result.IdempotencyKey)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Title{}, ErrNotFound
	}
	if err != nil {
		return Title{}, fmt.Errorf("buscar título: %w", err)
	}
	result.AmountCents, err = numericToCents(amount)
	if err != nil {
		return Title{}, fmt.Errorf("converter título: %w", err)
	}
	return result, nil
}

func (r *Repository) ListTitles(ctx context.Context, limit int) ([]Title, error) {
	return r.listTitles(ctx, TitleListFilter{}, listLimit(limit))
}

func (r *Repository) ListTitlePage(ctx context.Context, filter TitleListFilter) ([]Title, *TitleListPosition, error) {
	limit := listLimit(filter.Limit)
	items, err := r.listTitles(ctx, filter, limit+1)
	if err != nil || len(items) <= limit {
		return items, nil, err
	}
	last := items[limit-1]
	return items[:limit], &TitleListPosition{DueDate: last.DueDate, ID: last.ID}, nil
}

func (r *Repository) listTitles(ctx context.Context, filter TitleListFilter, limit int) ([]Title, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result := []Title{}
	var afterDate *time.Time
	var afterID *uuid.UUID
	if filter.After != nil {
		afterDate, afterID = &filter.After.DueDate, &filter.After.ID
	}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id,tenant_id,COALESCE(party_id,'00000000-0000-0000-0000-000000000000'::uuid),
			       direction,amount::text,due_date,status,origin,COALESCE(description,''),COALESCE(idempotency_key,'')
			FROM finance.title
			WHERE tenant_id=$1
			  AND ($2='' OR status=$2)
			  AND ($3='' OR direction=$3)
			  AND ($4::date IS NULL OR due_date >= $4)
			  AND ($5::date IS NULL OR due_date <= $5)
			  AND ($6::date IS NULL OR (due_date,id) > ($6,$7::uuid))
			ORDER BY due_date,id
			LIMIT $8`, tenantID, filter.Status, filter.Direction, filter.DueFrom, filter.DueTo, afterDate, afterID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Title
			var amount string
			if err := rows.Scan(&item.ID, &item.TenantID, &item.PartyID, &item.Direction, &amount, &item.DueDate, &item.Status, &item.Origin, &item.Description, &item.IdempotencyKey); err != nil {
				return err
			}
			item.AmountCents, err = numericToCents(amount)
			if err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar títulos: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateInstallment(ctx context.Context, item Installment) (Installment, error) {
	tenantID, err := tenantFor(ctx, item.TenantID)
	if err != nil {
		return Installment{}, err
	}
	amount, err := centsToNumeric(item.AmountCents)
	if err != nil {
		return Installment{}, err
	}
	item.TenantID = tenantID
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO finance.installment (tenant_id,title_id,number,amount,due_date,status) VALUES ($1,$2,$3,$4::numeric,$5,$6) RETURNING id`, tenantID, item.TitleID, item.Number, amount, item.DueDate, item.Status).Scan(&item.ID)
	})
	if err != nil {
		return Installment{}, fmt.Errorf("criar parcela: %w", err)
	}
	return item, nil
}

func (r *Repository) GetInstallmentByID(ctx context.Context, id uuid.UUID) (Installment, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Installment{}, err
	}
	var item Installment
	var amount string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,tenant_id,title_id,number,amount::text,due_date,status FROM finance.installment WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan(&item.ID, &item.TenantID, &item.TitleID, &item.Number, &amount, &item.DueDate, &item.Status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Installment{}, ErrNotFound
	}
	if err != nil {
		return Installment{}, fmt.Errorf("buscar parcela: %w", err)
	}
	item.AmountCents, err = numericToCents(amount)
	if err != nil {
		return Installment{}, fmt.Errorf("converter parcela: %w", err)
	}
	return item, nil
}

func (r *Repository) ListInstallments(ctx context.Context, titleID uuid.UUID, limit int) ([]Installment, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result := []Installment{}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,tenant_id,title_id,number,amount::text,due_date,status FROM finance.installment WHERE tenant_id=$1 AND ($2='00000000-0000-0000-0000-000000000000'::uuid OR title_id=$2) ORDER BY number,id LIMIT $3`, tenantID, titleID, listLimit(limit))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Installment
			var amount string
			if err := rows.Scan(&item.ID, &item.TenantID, &item.TitleID, &item.Number, &amount, &item.DueDate, &item.Status); err != nil {
				return err
			}
			item.AmountCents, err = numericToCents(amount)
			if err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar parcelas: %w", err)
	}
	return result, nil
}

func settlementBalance(amount, paid int64) int64 {
	if paid < 0 {
		paid = 0
	}
	if paid >= amount {
		return 0
	}
	return amount - paid
}

func (r *Repository) ListInstallmentBalances(ctx context.Context, titleID uuid.UUID, limit int) ([]InstallmentBalance, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result := []InstallmentBalance{}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT i.id,i.tenant_id,i.title_id,i.number,i.amount::text,i.due_date,i.status,
			       COALESCE(p.paid,'0')::text
			FROM finance.installment i
			LEFT JOIN (
				SELECT pay.installment_id, SUM(pay.amount) AS paid
				FROM finance.payment pay
				JOIN finance.installment pi ON pi.id=pay.installment_id AND pi.tenant_id=pay.tenant_id
				WHERE pay.tenant_id=$1 AND ($2='00000000-0000-0000-0000-000000000000'::uuid OR pi.title_id=$2)
				GROUP BY pay.installment_id
			) p ON p.installment_id=i.id
			WHERE i.tenant_id=$1 AND ($2='00000000-0000-0000-0000-000000000000'::uuid OR i.title_id=$2)
			ORDER BY i.number,i.id
			LIMIT $3`, tenantID, titleID, listLimit(limit))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item InstallmentBalance
			var amount, paid string
			if err := rows.Scan(&item.ID, &item.TenantID, &item.TitleID, &item.Number, &amount, &item.DueDate, &item.Status, &paid); err != nil {
				return err
			}
			item.AmountCents, err = numericToCents(amount)
			if err != nil {
				return err
			}
			item.PaidCents, err = numericToCents(paid)
			if err != nil {
				return err
			}
			item.BalanceCents = settlementBalance(item.AmountCents, item.PaidCents)
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar saldo das parcelas: %w", err)
	}
	return result, nil
}

func (r *Repository) GetTitleBalance(ctx context.Context, titleID uuid.UUID) (TitleBalance, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return TitleBalance{}, err
	}
	var amount, paid string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT t.amount::text, COALESCE(SUM(p.amount),0)::text
			FROM finance.title t
			LEFT JOIN finance.installment i ON i.tenant_id=t.tenant_id AND i.title_id=t.id
			LEFT JOIN finance.payment p ON p.tenant_id=t.tenant_id AND p.installment_id=i.id
			WHERE t.id=$1 AND t.tenant_id=$2
			GROUP BY t.id,t.amount`, titleID, tenantID).Scan(&amount, &paid)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TitleBalance{}, ErrNotFound
	}
	if err != nil {
		return TitleBalance{}, fmt.Errorf("buscar saldo do título: %w", err)
	}
	result := TitleBalance{}
	result.AmountCents, err = numericToCents(amount)
	if err != nil {
		return TitleBalance{}, fmt.Errorf("converter título: %w", err)
	}
	result.PaidCents, err = numericToCents(paid)
	if err != nil {
		return TitleBalance{}, fmt.Errorf("converter pagamentos do título: %w", err)
	}
	result.BalanceCents = settlementBalance(result.AmountCents, result.PaidCents)
	return result, nil
}

func (r *Repository) CreatePayment(ctx context.Context, payment Payment) (Payment, error) {
	if payment.IdempotencyKey == "" {
		payment.IdempotencyKey = "legacy:" + uuid.NewString()
	}
	return r.CreatePaymentIdempotent(ctx, payment.IdempotencyKey, payment)
}

func (r *Repository) CreatePaymentIdempotent(ctx context.Context, key string, payment Payment) (Payment, error) {
	tenantID, err := tenantFor(ctx, payment.TenantID)
	if err != nil {
		return Payment{}, err
	}
	if err := validateAmount(payment.AmountCents, true); err != nil {
		return Payment{}, err
	}
	key, err = idempotencyKey(key)
	if err != nil {
		return Payment{}, err
	}
	amount, err := centsToNumeric(payment.AmountCents)
	if err != nil {
		return Payment{}, err
	}
	payment.TenantID = tenantID
	payment.IdempotencyKey = key
	var returnedAmount string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO finance.payment (tenant_id,installment_id,bank_account_id,amount,paid_at,method,reference,idempotency_key) VALUES ($1,$2,$3,$4::numeric,COALESCE(NULLIF($5::timestamptz,'0001-01-01 00:00:00+00'::timestamptz),now()),$6,$7,$8) ON CONFLICT (tenant_id,idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING RETURNING id,tenant_id,installment_id,COALESCE(bank_account_id,'00000000-0000-0000-0000-000000000000'::uuid),amount::text,paid_at,method,COALESCE(reference,''),idempotency_key`, tenantID, payment.InstallmentID, optionalUUID(payment.BankAccountID), amount, payment.PaidAt, payment.Method, optionalString(payment.Reference), key).Scan(&payment.ID, &payment.TenantID, &payment.InstallmentID, &payment.BankAccountID, &returnedAmount, &payment.PaidAt, &payment.Method, &payment.Reference, &payment.IdempotencyKey)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id,tenant_id,installment_id,COALESCE(bank_account_id,'00000000-0000-0000-0000-000000000000'::uuid),amount::text,paid_at,method,COALESCE(reference,''),idempotency_key FROM finance.payment WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, key).Scan(&payment.ID, &payment.TenantID, &payment.InstallmentID, &payment.BankAccountID, &returnedAmount, &payment.PaidAt, &payment.Method, &payment.Reference, &payment.IdempotencyKey)
	})
	if err != nil {
		return Payment{}, fmt.Errorf("criar pagamento: %w", err)
	}
	payment.AmountCents, err = numericToCents(returnedAmount)
	if err != nil {
		return Payment{}, fmt.Errorf("converter pagamento: %w", err)
	}
	return payment, nil
}

func (r *Repository) GetPaymentByIdempotencyKey(ctx context.Context, key string) (Payment, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Payment{}, err
	}
	key, err = idempotencyKey(key)
	if err != nil {
		return Payment{}, err
	}
	var payment Payment
	var amount string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,tenant_id,installment_id,COALESCE(bank_account_id,'00000000-0000-0000-0000-000000000000'::uuid),amount::text,paid_at,method,COALESCE(reference,''),idempotency_key FROM finance.payment WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, key).
			Scan(&payment.ID, &payment.TenantID, &payment.InstallmentID, &payment.BankAccountID, &amount, &payment.PaidAt, &payment.Method, &payment.Reference, &payment.IdempotencyKey)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("buscar pagamento: %w", err)
	}
	payment.AmountCents, err = numericToCents(amount)
	return payment, err
}

func (r *Repository) CreateCashSession(ctx context.Context, item CashSession) (CashSession, error) {
	tenantID, err := tenantFor(ctx, item.TenantID)
	if err != nil {
		return CashSession{}, err
	}
	amount, err := centsToNumeric(item.OpeningAmountCents)
	if err != nil {
		return CashSession{}, err
	}
	item.TenantID = tenantID
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO finance.cash_session (tenant_id,user_id,opening_amount,status) VALUES ($1,$2,$3::numeric,$4) RETURNING id,opened_at`, tenantID, optionalUUID(item.UserID), amount, item.Status).Scan(&item.ID, &item.OpenedAt)
	})
	if err != nil {
		return CashSession{}, fmt.Errorf("criar sessão de caixa: %w", err)
	}
	return item, nil
}
func (r *Repository) GetCashSessionByID(ctx context.Context, id uuid.UUID) (CashSession, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return CashSession{}, err
	}
	var item CashSession
	var amount string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,tenant_id,COALESCE(user_id,'00000000-0000-0000-0000-000000000000'::uuid),opened_at,closed_at,opening_amount::text,status FROM finance.cash_session WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan(&item.ID, &item.TenantID, &item.UserID, &item.OpenedAt, &item.ClosedAt, &amount, &item.Status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CashSession{}, ErrNotFound
	}
	if err != nil {
		return CashSession{}, fmt.Errorf("buscar sessão de caixa: %w", err)
	}
	item.OpeningAmountCents, err = numericToCents(amount)
	return item, err
}
func (r *Repository) CreateCashMovement(ctx context.Context, item CashMovement) (CashMovement, error) {
	if item.IdempotencyKey == "" {
		item.IdempotencyKey = "legacy:" + uuid.NewString()
	}
	return r.CreateCashMovementIdempotent(ctx, item.IdempotencyKey, item)
}

func (r *Repository) CreateCashMovementIdempotent(ctx context.Context, key string, item CashMovement) (CashMovement, error) {
	tenantID, err := tenantFor(ctx, item.TenantID)
	if err != nil {
		return CashMovement{}, err
	}
	if err := validateAmount(item.AmountCents, true); err != nil {
		return CashMovement{}, err
	}
	key, err = idempotencyKey(key)
	if err != nil {
		return CashMovement{}, err
	}
	amount, err := centsToNumeric(item.AmountCents)
	if err != nil {
		return CashMovement{}, err
	}
	item.TenantID = tenantID
	item.IdempotencyKey = key
	var returnedAmount string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO finance.cash_movement (tenant_id,session_id,kind,amount,reason,occurred_at,idempotency_key) VALUES ($1,$2,$3,$4::numeric,$5,COALESCE(NULLIF($6::timestamptz,'0001-01-01 00:00:00+00'::timestamptz),now()),$7) ON CONFLICT (tenant_id,idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING RETURNING id,tenant_id,session_id,kind,amount::text,reason,occurred_at,idempotency_key`, tenantID, item.SessionID, item.Kind, amount, item.Reason, item.OccurredAt, key).Scan(&item.ID, &item.TenantID, &item.SessionID, &item.Kind, &returnedAmount, &item.Reason, &item.OccurredAt, &item.IdempotencyKey)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id,tenant_id,session_id,kind,amount::text,reason,occurred_at,idempotency_key FROM finance.cash_movement WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, key).Scan(&item.ID, &item.TenantID, &item.SessionID, &item.Kind, &returnedAmount, &item.Reason, &item.OccurredAt, &item.IdempotencyKey)
	})
	if err != nil {
		return CashMovement{}, fmt.Errorf("criar movimento de caixa: %w", err)
	}
	item.AmountCents, err = numericToCents(returnedAmount)
	if err != nil {
		return CashMovement{}, fmt.Errorf("converter movimento de caixa: %w", err)
	}
	return item, nil
}

func (r *Repository) GetCashMovementByIdempotencyKey(ctx context.Context, key string) (CashMovement, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return CashMovement{}, err
	}
	key, err = idempotencyKey(key)
	if err != nil {
		return CashMovement{}, err
	}
	var item CashMovement
	var amount string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,tenant_id,session_id,kind,amount::text,reason,occurred_at,idempotency_key FROM finance.cash_movement WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, key).
			Scan(&item.ID, &item.TenantID, &item.SessionID, &item.Kind, &amount, &item.Reason, &item.OccurredAt, &item.IdempotencyKey)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CashMovement{}, ErrNotFound
	}
	if err != nil {
		return CashMovement{}, fmt.Errorf("buscar movimento de caixa: %w", err)
	}
	item.AmountCents, err = numericToCents(amount)
	return item, err
}
func (r *Repository) ListCashMovements(ctx context.Context, sessionID uuid.UUID, limit int) ([]CashMovement, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result := []CashMovement{}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,tenant_id,session_id,kind,amount::text,reason,occurred_at FROM finance.cash_movement WHERE tenant_id=$1 AND session_id=$2 ORDER BY occurred_at,id LIMIT $3`, tenantID, sessionID, listLimit(limit))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item CashMovement
			var amount string
			if err := rows.Scan(&item.ID, &item.TenantID, &item.SessionID, &item.Kind, &amount, &item.Reason, &item.OccurredAt); err != nil {
				return err
			}
			item.AmountCents, err = numericToCents(amount)
			if err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar movimentos de caixa: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateLedgerEntry(ctx context.Context, item LedgerEntry) (LedgerEntry, error) {
	tenantID, err := tenantFor(ctx, item.TenantID)
	if err != nil {
		return LedgerEntry{}, err
	}
	debit, err := centsToNumeric(item.DebitCents)
	if err != nil {
		return LedgerEntry{}, err
	}
	credit, err := centsToNumeric(item.CreditCents)
	if err != nil {
		return LedgerEntry{}, err
	}
	if (item.DebitCents == 0) == (item.CreditCents == 0) {
		return LedgerEntry{}, ErrInvalidAmount
	}
	item.TenantID = tenantID
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO finance.ledger_entry (tenant_id,account_id,competence,debit,credit,source,source_id) VALUES ($1,$2,$3,$4::numeric,$5::numeric,$6,$7) RETURNING id`, tenantID, item.AccountID, item.Competence, debit, credit, item.Source, optionalUUID(item.SourceID)).Scan(&item.ID)
	})
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("criar lançamento: %w", err)
	}
	return item, nil
}
func (r *Repository) ListLedgerEntries(ctx context.Context, accountID uuid.UUID, limit int) ([]LedgerEntry, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result := []LedgerEntry{}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,tenant_id,account_id,competence,debit::text,credit::text,source,COALESCE(source_id,'00000000-0000-0000-0000-000000000000'::uuid) FROM finance.ledger_entry WHERE tenant_id=$1 AND ($2='00000000-0000-0000-0000-000000000000'::uuid OR account_id=$2) ORDER BY competence,id LIMIT $3`, tenantID, accountID, listLimit(limit))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LedgerEntry
			var debit, credit string
			if err := rows.Scan(&item.ID, &item.TenantID, &item.AccountID, &item.Competence, &debit, &credit, &item.Source, &item.SourceID); err != nil {
				return err
			}
			item.DebitCents, err = numericToCents(debit)
			if err != nil {
				return err
			}
			item.CreditCents, err = numericToCents(credit)
			if err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar lançamentos: %w", err)
	}
	return result, nil
}
