-- Idempotencia persistida para operacoes que movem dinheiro.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up
ALTER TABLE finance.payment ADD COLUMN idempotency_key text;
ALTER TABLE finance.cash_movement ADD COLUMN idempotency_key text;

CREATE UNIQUE INDEX payment_tenant_idempotency_uq
    ON finance.payment (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX cash_movement_tenant_idempotency_uq
    ON finance.cash_movement (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX cash_movement_tenant_idempotency_uq;
DROP INDEX payment_tenant_idempotency_uq;
ALTER TABLE finance.cash_movement DROP COLUMN idempotency_key;
ALTER TABLE finance.payment DROP COLUMN idempotency_key;
