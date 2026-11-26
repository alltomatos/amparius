-- Amparius: corrige a interação entre o gatilho de saldo e o replay idempotente.
-- Forward-only: correções futuras devem ser novas migrations.

-- O gatilho finance.validate_payment_balance disparava ANTES do INSERT também na
-- re-submissão idempotente (mesma Idempotency-Key). Nesse replay, a soma dos
-- pagamentos já incluía o lançamento original e, quando o valor original era
-- igual ou superior à metade da parcela, o replay era rejeitado como
-- sobrepagamento ("pagamentos excedem o valor da parcela"), antes de o
-- ON CONFLICT (tenant_id, idempotency_key) DO NOTHING deduplicar o registro.
-- O replay deve ser um no-op que devolve o mesmo id, nunca uma rejeição.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION finance.validate_payment_balance() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE installment_amount numeric(18,2); paid_amount numeric(18,2);
BEGIN
    IF NEW.idempotency_key IS NOT NULL AND EXISTS (
        SELECT 1 FROM finance.payment
        WHERE tenant_id = NEW.tenant_id
          AND idempotency_key = NEW.idempotency_key
          AND id IS DISTINCT FROM NEW.id
    ) THEN
        RETURN NEW;
    END IF;
    SELECT amount INTO installment_amount FROM finance.installment WHERE id = NEW.installment_id FOR UPDATE;
    SELECT COALESCE(sum(amount), 0) INTO paid_amount FROM finance.payment WHERE installment_id = NEW.installment_id AND id <> COALESCE(NEW.id, '00000000-0000-0000-0000-000000000000'::uuid);
    IF paid_amount + NEW.amount > installment_amount THEN RAISE EXCEPTION 'pagamentos excedem o valor da parcela'; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION finance.validate_payment_balance() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE installment_amount numeric(18,2); paid_amount numeric(18,2);
BEGIN
    SELECT amount INTO installment_amount FROM finance.installment WHERE id = NEW.installment_id FOR UPDATE;
    SELECT COALESCE(sum(amount), 0) INTO paid_amount FROM finance.payment WHERE installment_id = NEW.installment_id AND id <> COALESCE(NEW.id, '00000000-0000-0000-0000-000000000000'::uuid);
    IF paid_amount + NEW.amount > installment_amount THEN RAISE EXCEPTION 'pagamentos excedem o valor da parcela'; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
