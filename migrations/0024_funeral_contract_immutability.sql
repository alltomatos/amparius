-- Amparius: protege a identidade do contrato e reduz o grant de UPDATE.
-- Forward-only: correcoes futuras devem ser novas migracoes.

-- +goose Up
-- O agregado pode mudar apenas status e updated_at. Cliente, falecido,
-- empresa, valor e chave de idempotencia sao snapshots imutaveis.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.validate_contract() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM identity.domain_value
        WHERE domain_code = 'funeral_contract_status' AND code = NEW.status AND active
    ) THEN
        RAISE EXCEPTION 'status de contrato funerario invalido: %', NEW.status;
    END IF;
    IF TG_OP = 'UPDATE' AND (
        NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.company_id <> OLD.company_id OR
        NEW.customer_party_id <> OLD.customer_party_id OR NEW.deceased_id <> OLD.deceased_id OR
        NEW.total_amount <> OLD.total_amount OR NEW.idempotency_key <> OLD.idempotency_key OR
        NEW.request_hash <> OLD.request_hash OR NEW.created_at <> OLD.created_at
    ) THEN
        RAISE EXCEPTION 'identidade do contrato funerario e imutavel';
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status AND NOT (
        (OLD.status = 'draft' AND NEW.status IN ('active', 'cancelled')) OR
        (OLD.status = 'active' AND NEW.status IN ('completed', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'transicao de status de contrato funerario invalida: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

REVOKE UPDATE ON funeral.contract FROM amparius_app;
GRANT UPDATE (status, updated_at) ON funeral.contract TO amparius_app;

-- +goose Down
REVOKE UPDATE (status, updated_at) ON funeral.contract FROM amparius_app;
GRANT UPDATE ON funeral.contract TO amparius_app;
