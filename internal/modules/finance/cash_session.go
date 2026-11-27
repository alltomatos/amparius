package finance

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrCashSessionAlreadyOpen   = errors.New("já existe caixa aberto para o usuário")
	ErrCashSessionAlreadyClosed = errors.New("sessão de caixa já fechada")
)

type CashSessionPosition struct {
	OpenedAt time.Time
	ID       uuid.UUID
}

type CashSessionFilter struct {
	Status string
	After  *CashSessionPosition
	Limit  int
}

type CashClosure struct {
	CountedCents int64
	UserID       uuid.UUID
	Note         string
}

type CashReconciliation struct {
	Session          CashSession
	ExpectedCents    int64
	MovementInCents  int64
	MovementOutCents int64
	PaymentByMethod  map[string]int64
}

const cashSessionColumns = `id, tenant_id, COALESCE(user_id, '00000000-0000-0000-0000-000000000000'::uuid),
	opened_at, closed_at, (opening_amount * 100)::bigint, status,
	(expected_amount * 100)::bigint, (counted_amount * 100)::bigint,
	(difference_amount * 100)::bigint, COALESCE(closed_by, '00000000-0000-0000-0000-000000000000'::uuid),
	COALESCE(close_note, '')`

func scanCashSession(row pgx.Row) (CashSession, error) {
	var item CashSession
	err := row.Scan(&item.ID, &item.TenantID, &item.UserID, &item.OpenedAt, &item.ClosedAt,
		&item.OpeningAmountCents, &item.Status, &item.ExpectedAmountCents, &item.CountedAmountCents,
		&item.DifferenceAmountCents, &item.ClosedBy, &item.CloseNote)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return item, err
}

// Serialize submissions of the same key before any validation that depends on
// mutable state. Unique indexes also enforce the identity at rest.
func lockFinanceKey(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, operation, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, operation+":"+tenantID.String()+":"+key)
	return err
}

func (r *Repository) OpenCashSession(ctx context.Context, key string, item CashSession) (CashSession, error) {
	tenantID, err := tenantFor(ctx, item.TenantID)
	if err != nil {
		return CashSession{}, err
	}
	if item.UserID == uuid.Nil {
		return CashSession{}, ErrInvalidHTTPFinanceRequest
	}
	if err := validateAmount(item.OpeningAmountCents, false); err != nil {
		return CashSession{}, err
	}
	key, err = idempotencyKey(key)
	if err != nil {
		return CashSession{}, err
	}
	amount, err := centsToNumeric(item.OpeningAmountCents)
	if err != nil {
		return CashSession{}, err
	}
	var result CashSession
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockFinanceKey(ctx, tx, tenantID, "cash-open", key); err != nil {
			return err
		}
		result, err = scanCashSession(tx.QueryRow(ctx, `SELECT `+cashSessionColumns+
			` FROM finance.cash_session WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, key))
		if err == nil {
			if result.UserID != item.UserID || result.OpeningAmountCents != item.OpeningAmountCents {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		result, err = scanCashSession(tx.QueryRow(ctx, `
			INSERT INTO finance.cash_session (tenant_id,user_id,opening_amount,status,idempotency_key)
			VALUES ($1,$2,$3::numeric,'open',$4) RETURNING `+cashSessionColumns, tenantID, item.UserID, amount, key))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "cash_session_tenant_open_user_uq" {
			return ErrCashSessionAlreadyOpen
		}
		return err
	})
	return result, err
}

func (r *Repository) ListCashSessionPage(ctx context.Context, filter CashSessionFilter) ([]CashSession, *CashSessionPosition, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	limit := listLimit(filter.Limit)
	var afterTime *time.Time
	var afterID *uuid.UUID
	if filter.After != nil {
		afterTime, afterID = &filter.After.OpenedAt, &filter.After.ID
	}
	result := []CashSession{}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+cashSessionColumns+`
			FROM finance.cash_session WHERE tenant_id=$1 AND ($2='' OR status=$2)
			AND ($3::timestamptz IS NULL OR (opened_at,id) > ($3,$4::uuid))
			ORDER BY opened_at,id LIMIT $5`, tenantID, filter.Status, afterTime, afterID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanCashSession(rows)
			if err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil || len(result) <= limit {
		return result, nil, err
	}
	last := result[limit-1]
	return result[:limit], &CashSessionPosition{OpenedAt: *last.OpenedAt, ID: last.ID}, nil
}

// A shared row lock permits concurrent appends while excluding the close
// update. The close takes its row lock before reading any totals.
func requireOpenCashSession(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM finance.cash_session WHERE tenant_id=$1 AND id=$2 FOR SHARE`,
		tenantID, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "open" {
		return ErrCashSessionAlreadyClosed
	}
	return nil
}

const cashDifferenceCounter = "amparius_finance_cash_session_close_difference_total"

func (r *Repository) CloseCashSession(ctx context.Context, id uuid.UUID, key string, closure CashClosure) (CashSession, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return CashSession{}, err
	}
	if closure.UserID == uuid.Nil {
		return CashSession{}, ErrInvalidHTTPFinanceRequest
	}
	if err := validateAmount(closure.CountedCents, false); err != nil {
		return CashSession{}, err
	}
	key, err = idempotencyKey(key)
	if err != nil {
		return CashSession{}, err
	}
	counted, err := centsToNumeric(closure.CountedCents)
	if err != nil {
		return CashSession{}, err
	}
	var result CashSession
	var closedNow bool
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockFinanceKey(ctx, tx, tenantID, "cash-close", key); err != nil {
			return err
		}
		result, err = scanCashSession(tx.QueryRow(ctx, `SELECT `+cashSessionColumns+
			` FROM finance.cash_session WHERE tenant_id=$1 AND close_idempotency_key=$2`, tenantID, key))
		if err == nil {
			if result.ID != id || result.ClosedBy != closure.UserID ||
				result.CountedAmountCents == nil || *result.CountedAmountCents != closure.CountedCents || result.CloseNote != closure.Note {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM finance.cash_session
			WHERE tenant_id=$1 AND id=$2 FOR NO KEY UPDATE`, tenantID, id).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return ErrCashSessionAlreadyClosed
		}
		result, err = scanCashSession(tx.QueryRow(ctx, cashTotalsSQL+`
			UPDATE finance.cash_session SET
				status='closed', closed_at=statement_timestamp(), closed_by=$4,
				expected_amount=opening_amount + movements.total_in - movements.total_out + payments.cash,
				counted_amount=$3::numeric,
				difference_amount=$3::numeric - (opening_amount + movements.total_in - movements.total_out + payments.cash),
				close_idempotency_key=$5, close_note=$6, updated_at=statement_timestamp(), version=version+1
			FROM movements, payments WHERE tenant_id=$1 AND id=$2 RETURNING `+cashSessionColumns,
			tenantID, id, counted, closure.UserID, key, optionalString(closure.Note)))
		closedNow = err == nil
		return err
	})
	if err == nil && closedNow && result.DifferenceAmountCents != nil && *result.DifferenceAmountCents != 0 {
		r.metrics.AddCounter(cashDifferenceCounter, nil, 1)
		if r.logger != nil {
			r.logger.LogAttrs(ctx, slog.LevelWarn, "finance_cash_session_close_difference",
				slog.String("tenant_id", tenantID.String()),
				slog.String("session_id", id.String()),
				slog.String("user_id", closure.UserID.String()),
				slog.Int64("difference_amount_cents", *result.DifferenceAmountCents),
				slog.String("request_id", observability.CorrelationIDFromContext(ctx)),
				slog.String("trace_id", observability.TraceIDFromContext(ctx)),
			)
		}
	}
	return result, err
}

const cashTotalsSQL = `WITH movements AS (
	SELECT COALESCE(sum(amount) FILTER (WHERE kind='in'), 0) AS total_in,
		COALESCE(sum(amount) FILTER (WHERE kind='out'), 0) AS total_out
	FROM finance.cash_movement WHERE tenant_id=$1 AND session_id=$2
), methods AS (
	SELECT method, sum(amount) AS amount FROM finance.payment
	WHERE tenant_id=$1 AND session_id=$2 GROUP BY method
), payments AS (
	SELECT COALESCE(max(amount) FILTER (WHERE method='cash'), 0) AS cash,
		COALESCE(jsonb_object_agg(method, (amount * 100)::bigint), '{}'::jsonb) AS by_method
	FROM methods
) `

func (r *Repository) GetCashReconciliation(ctx context.Context, id uuid.UUID) (CashReconciliation, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return CashReconciliation{}, err
	}
	var result CashReconciliation
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, cashTotalsSQL+`SELECT `+cashSessionColumns+`,
			(COALESCE(expected_amount, opening_amount + movements.total_in - movements.total_out + payments.cash) * 100)::bigint,
			(movements.total_in * 100)::bigint, (movements.total_out * 100)::bigint, payments.by_method
			FROM finance.cash_session CROSS JOIN movements CROSS JOIN payments WHERE tenant_id=$1 AND id=$2`, tenantID, id)
		item := &result.Session
		return row.Scan(&item.ID, &item.TenantID, &item.UserID, &item.OpenedAt, &item.ClosedAt,
			&item.OpeningAmountCents, &item.Status, &item.ExpectedAmountCents, &item.CountedAmountCents,
			&item.DifferenceAmountCents, &item.ClosedBy, &item.CloseNote, &result.ExpectedCents,
			&result.MovementInCents, &result.MovementOutCents, &result.PaymentByMethod)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return result, err
}
