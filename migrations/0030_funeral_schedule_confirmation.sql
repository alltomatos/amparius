-- Amparius: confirmação explícita e cancelamento auditável da agenda.
-- Forward-only: correções futuras devem ser novas migrations.

-- +goose Up
INSERT INTO identity.domain_value (domain_code, code, label)
VALUES ('funeral_schedule_status', 'confirmed', 'Confirmado')
ON CONFLICT (domain_code, code) DO NOTHING;

ALTER TABLE funeral.schedule_mutation
    DROP CONSTRAINT schedule_mutation_operation_check;
ALTER TABLE funeral.schedule_mutation
    ADD CONSTRAINT schedule_mutation_operation_check
    CHECK (operation IN ('update', 'confirm', 'cancel', 'complete'));

ALTER TABLE funeral.schedule
    DROP CONSTRAINT funeral_schedule_location_no_overlap;
ALTER TABLE funeral.schedule
    DROP CONSTRAINT funeral_schedule_resource_no_overlap;
ALTER TABLE funeral.schedule
    ADD CONSTRAINT funeral_schedule_location_no_overlap
    EXCLUDE USING gist (tenant_id WITH =, company_id WITH =, location_id WITH =, slot WITH &&)
    WHERE (status IN ('scheduled', 'confirmed'));
ALTER TABLE funeral.schedule
    ADD CONSTRAINT funeral_schedule_resource_no_overlap
    EXCLUDE USING gist (tenant_id WITH =, company_id WITH =, resource_id WITH =, slot WITH &&)
    WHERE (status IN ('scheduled', 'confirmed') AND resource_id IS NOT NULL);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.validate_schedule() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    order_contract uuid;
    location_company uuid;
    resource_company uuid;
    resource_location uuid;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM identity.domain_value WHERE domain_code='funeral_schedule_status' AND code=NEW.status AND active) THEN
        RAISE EXCEPTION 'status de agenda invalido: %', NEW.status;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM identity.domain_value WHERE domain_code='funeral_schedule_kind' AND code=NEW.kind AND active) THEN
        RAISE EXCEPTION 'tipo de agenda invalido: %', NEW.kind;
    END IF;
    SELECT company_id INTO location_company FROM funeral.location WHERE tenant_id=NEW.tenant_id AND id=NEW.location_id;
    IF location_company IS NULL OR location_company <> NEW.company_id THEN
        RAISE EXCEPTION 'local fora do escopo da agenda';
    END IF;
    IF NEW.resource_id IS NOT NULL THEN
        SELECT company_id, location_id INTO resource_company, resource_location FROM funeral.resource WHERE tenant_id=NEW.tenant_id AND id=NEW.resource_id AND active;
        IF resource_company IS NULL OR resource_company <> NEW.company_id OR resource_location <> NEW.location_id THEN
            RAISE EXCEPTION 'recurso fora do escopo da agenda';
        END IF;
    END IF;
    IF NEW.service_order_id IS NOT NULL THEN
        SELECT contract_id INTO order_contract FROM funeral.service_order WHERE tenant_id=NEW.tenant_id AND company_id=NEW.company_id AND id=NEW.service_order_id;
        IF order_contract IS NULL OR order_contract <> NEW.contract_id THEN
            RAISE EXCEPTION 'ordem de servico nao pertence ao contrato da agenda';
        END IF;
    END IF;
    IF TG_OP = 'UPDATE' AND (
        NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.company_id <> OLD.company_id OR
        NEW.contract_id <> OLD.contract_id OR NEW.idempotency_key <> OLD.idempotency_key OR
        NEW.request_hash <> OLD.request_hash OR NEW.created_at <> OLD.created_at
    ) THEN
        RAISE EXCEPTION 'identidade da agenda e imutavel';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status <> NEW.status AND NOT (
        (OLD.status = 'scheduled' AND NEW.status IN ('confirmed','cancelled','completed')) OR
        (OLD.status = 'confirmed' AND NEW.status IN ('cancelled','completed'))
    ) THEN
        RAISE EXCEPTION 'transicao de agenda invalida: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE funeral.schedule
    DROP CONSTRAINT funeral_schedule_location_no_overlap;
ALTER TABLE funeral.schedule
    DROP CONSTRAINT funeral_schedule_resource_no_overlap;
ALTER TABLE funeral.schedule
    ADD CONSTRAINT funeral_schedule_location_no_overlap
    EXCLUDE USING gist (tenant_id WITH =, company_id WITH =, location_id WITH =, slot WITH &&)
    WHERE (status = 'scheduled');
ALTER TABLE funeral.schedule
    ADD CONSTRAINT funeral_schedule_resource_no_overlap
    EXCLUDE USING gist (tenant_id WITH =, company_id WITH =, resource_id WITH =, slot WITH &&)
    WHERE (status = 'scheduled' AND resource_id IS NOT NULL);

ALTER TABLE funeral.schedule_mutation
    DROP CONSTRAINT schedule_mutation_operation_check;
ALTER TABLE funeral.schedule_mutation
    ADD CONSTRAINT schedule_mutation_operation_check
    CHECK (operation IN ('update', 'cancel', 'complete'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.validate_schedule() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    order_contract uuid;
    location_company uuid;
    resource_company uuid;
    resource_location uuid;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM identity.domain_value WHERE domain_code='funeral_schedule_status' AND code=NEW.status AND active) THEN
        RAISE EXCEPTION 'status de agenda invalido: %', NEW.status;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM identity.domain_value WHERE domain_code='funeral_schedule_kind' AND code=NEW.kind AND active) THEN
        RAISE EXCEPTION 'tipo de agenda invalido: %', NEW.kind;
    END IF;
    SELECT company_id INTO location_company FROM funeral.location WHERE tenant_id=NEW.tenant_id AND id=NEW.location_id;
    IF location_company IS NULL OR location_company <> NEW.company_id THEN
        RAISE EXCEPTION 'local fora do escopo da agenda';
    END IF;
    IF NEW.resource_id IS NOT NULL THEN
        SELECT company_id, location_id INTO resource_company, resource_location FROM funeral.resource WHERE tenant_id=NEW.tenant_id AND id=NEW.resource_id AND active;
        IF resource_company IS NULL OR resource_company <> NEW.company_id OR resource_location <> NEW.location_id THEN
            RAISE EXCEPTION 'recurso fora do escopo da agenda';
        END IF;
    END IF;
    IF NEW.service_order_id IS NOT NULL THEN
        SELECT contract_id INTO order_contract FROM funeral.service_order WHERE tenant_id=NEW.tenant_id AND company_id=NEW.company_id AND id=NEW.service_order_id;
        IF order_contract IS NULL OR order_contract <> NEW.contract_id THEN
            RAISE EXCEPTION 'ordem de servico nao pertence ao contrato da agenda';
        END IF;
    END IF;
    IF TG_OP = 'UPDATE' AND (
        NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.company_id <> OLD.company_id OR
        NEW.contract_id <> OLD.contract_id OR NEW.idempotency_key <> OLD.idempotency_key OR
        NEW.request_hash <> OLD.request_hash OR NEW.created_at <> OLD.created_at
    ) THEN
        RAISE EXCEPTION 'identidade da agenda e imutavel';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status <> NEW.status AND NOT (OLD.status = 'scheduled' AND NEW.status IN ('cancelled','completed')) THEN
        RAISE EXCEPTION 'transicao de agenda invalida: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DELETE FROM identity.domain_value
WHERE domain_code = 'funeral_schedule_status' AND code = 'confirmed';
