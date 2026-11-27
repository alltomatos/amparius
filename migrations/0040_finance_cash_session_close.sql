-- +goose Up
ALTER TABLE finance.payment
    ADD COLUMN session_id uuid,
    ADD CONSTRAINT payment_tenant_session_fk FOREIGN KEY (tenant_id, session_id)
        REFERENCES finance.cash_session (tenant_id, id);
CREATE INDEX payment_tenant_session_paid_idx
    ON finance.payment (tenant_id, session_id, paid_at) WHERE session_id IS NOT NULL;

ALTER TABLE finance.cash_session
    ADD COLUMN idempotency_key text,
    ADD COLUMN expected_amount numeric(18,2),
    ADD COLUMN counted_amount numeric(18,2),
    ADD COLUMN difference_amount numeric(18,2),
    ADD COLUMN closed_by uuid,
    ADD COLUMN close_idempotency_key text,
    ADD COLUMN close_note text;

-- Legacy closures have no recorded count. Use the opening balance as the
-- historical placeholder and leave closed_by and the close request unset.
UPDATE finance.cash_session
SET closed_at = COALESCE(closed_at, opened_at),
    expected_amount = opening_amount,
    counted_amount = opening_amount,
    difference_amount = 0
WHERE status = 'closed';

ALTER TABLE finance.cash_session ADD CONSTRAINT cash_session_closure_ck CHECK (
    status <> 'closed' OR (
        closed_at IS NOT NULL AND counted_amount IS NOT NULL AND expected_amount IS NOT NULL
        AND difference_amount IS NOT NULL AND counted_amount >= 0
        AND difference_amount = counted_amount - expected_amount
    )
);
CREATE UNIQUE INDEX cash_session_tenant_idempotency_uq
    ON finance.cash_session (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX cash_session_tenant_close_key_uq
    ON finance.cash_session (tenant_id, close_idempotency_key) WHERE close_idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX cash_session_tenant_open_user_uq
    ON finance.cash_session (tenant_id, user_id) WHERE status = 'open' AND user_id IS NOT NULL;

GRANT UPDATE (status, closed_at, expected_amount, counted_amount, difference_amount,
              closed_by, close_idempotency_key, close_note, updated_at, version)
    ON finance.cash_session TO amparius_app;

-- +goose Down
REVOKE UPDATE (status, closed_at, expected_amount, counted_amount, difference_amount,
               closed_by, close_idempotency_key, close_note, updated_at, version)
    ON finance.cash_session FROM amparius_app;
DROP INDEX finance.cash_session_tenant_open_user_uq;
DROP INDEX finance.cash_session_tenant_close_key_uq;
DROP INDEX finance.cash_session_tenant_idempotency_uq;
DROP INDEX finance.payment_tenant_session_paid_idx;
ALTER TABLE finance.payment DROP COLUMN session_id;
ALTER TABLE finance.cash_session
    DROP CONSTRAINT cash_session_closure_ck,
    DROP COLUMN idempotency_key,
    DROP COLUMN expected_amount,
    DROP COLUMN counted_amount,
    DROP COLUMN difference_amount,
    DROP COLUMN closed_by,
    DROP COLUMN close_idempotency_key,
    DROP COLUMN close_note;
