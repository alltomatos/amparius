-- Amparius: ordem de servico ligada ao contrato funerario.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up
INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('funeral_service_order_status', 'open', 'Aberta'),
    ('funeral_service_order_status', 'in_progress', 'Em atendimento'),
    ('funeral_service_order_status', 'completed', 'Concluida'),
    ('funeral_service_order_status', 'cancelled', 'Cancelada')
ON CONFLICT (domain_code, code) DO NOTHING;

CREATE TABLE funeral.service_order (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    company_id uuid NOT NULL,
    contract_id uuid NOT NULL,
    kind text NOT NULL CHECK (length(btrim(kind)) > 0 AND length(btrim(kind)) <= 100),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    status text NOT NULL DEFAULT 'open',
    scheduled_for timestamptz,
    completed_at timestamptz,
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) > 0 AND length(btrim(idempotency_key)) <= 200),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT service_order_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT service_order_tenant_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT service_order_tenant_contract_fk FOREIGN KEY (tenant_id, contract_id)
        REFERENCES funeral.contract (tenant_id, id),
    CONSTRAINT service_order_tenant_idempotency_uq UNIQUE (tenant_id, idempotency_key),
    CONSTRAINT service_order_completed_at_ck CHECK (status <> 'completed' OR completed_at IS NOT NULL)
);

CREATE TABLE funeral.service_order_status_history (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    service_order_id uuid NOT NULL,
    from_status text,
    to_status text NOT NULL,
    reason text,
    changed_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT service_order_status_tenant_order_fk FOREIGN KEY (tenant_id, service_order_id)
        REFERENCES funeral.service_order (tenant_id, id),
    CONSTRAINT service_order_status_tenant_id_uq UNIQUE (tenant_id, id)
);

-- Atualizacoes e conclusoes aceitam replay seguro sem duplicar historico/evento.
CREATE TABLE funeral.service_order_mutation (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    service_order_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('update', 'complete')),
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) > 0 AND length(btrim(idempotency_key)) <= 200),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT service_order_mutation_tenant_order_fk FOREIGN KEY (tenant_id, service_order_id)
        REFERENCES funeral.service_order (tenant_id, id),
    CONSTRAINT service_order_mutation_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT service_order_mutation_key_uq UNIQUE (tenant_id, operation, idempotency_key)
);

CREATE INDEX service_order_tenant_contract_idx
    ON funeral.service_order (tenant_id, company_id, contract_id, created_at, id);
CREATE INDEX service_order_tenant_status_idx
    ON funeral.service_order (tenant_id, company_id, status, scheduled_for, id);
CREATE INDEX service_order_status_history_order_idx
    ON funeral.service_order_status_history (tenant_id, service_order_id, changed_at, id);
CREATE INDEX service_order_mutation_order_idx
    ON funeral.service_order_mutation (tenant_id, service_order_id, created_at, id);

-- O agregado muda apenas os campos operacionais e o estado permitido.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.validate_service_order() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM identity.domain_value
        WHERE domain_code = 'funeral_service_order_status' AND code = NEW.status AND active
    ) THEN
        RAISE EXCEPTION 'status de ordem de servico invalido: %', NEW.status;
    END IF;
    IF TG_OP = 'UPDATE' AND (
        NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.company_id <> OLD.company_id OR
        NEW.contract_id <> OLD.contract_id OR NEW.kind <> OLD.kind OR
        NEW.idempotency_key <> OLD.idempotency_key OR NEW.request_hash <> OLD.request_hash OR
        NEW.created_at <> OLD.created_at
    ) THEN
        RAISE EXCEPTION 'identidade da ordem de servico e imutavel';
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status AND NOT (
        (OLD.status = 'open' AND NEW.status IN ('in_progress', 'completed', 'cancelled')) OR
        (OLD.status = 'in_progress' AND NEW.status IN ('completed', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'transicao de status de ordem de servico invalida: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Historico e comandos de idempotencia sao append-only.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.reject_service_order_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tabela funeral.% e append-only: % proibido', TG_TABLE_NAME, TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER service_order_validate
    BEFORE INSERT OR UPDATE ON funeral.service_order
    FOR EACH ROW EXECUTE FUNCTION funeral.validate_service_order();
CREATE TRIGGER service_order_status_append_only
    BEFORE UPDATE OR DELETE ON funeral.service_order_status_history
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_service_order_mutation();
CREATE TRIGGER service_order_mutation_append_only
    BEFORE UPDATE OR DELETE ON funeral.service_order_mutation
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_service_order_mutation();
CREATE TRIGGER service_order_history_preserve
    BEFORE DELETE ON funeral.service_order
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_service_order_mutation();

ALTER TABLE funeral.service_order ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.service_order_status_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.service_order_mutation ENABLE ROW LEVEL SECURITY;

CREATE POLICY service_order_tenant_isolation ON funeral.service_order
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY service_order_status_tenant_isolation ON funeral.service_order_status_history
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY service_order_mutation_tenant_isolation ON funeral.service_order_mutation
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON funeral.service_order TO amparius_app;
GRANT SELECT, INSERT ON funeral.service_order_status_history TO amparius_app;
GRANT SELECT, INSERT ON funeral.service_order_mutation TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT ON funeral.service_order_mutation FROM amparius_app;
REVOKE SELECT, INSERT ON funeral.service_order_status_history FROM amparius_app;
REVOKE SELECT, INSERT, UPDATE ON funeral.service_order FROM amparius_app;
DROP TABLE funeral.service_order_mutation;
DROP TABLE funeral.service_order_status_history;
DROP TABLE funeral.service_order;
DELETE FROM identity.domain_value WHERE domain_code = 'funeral_service_order_status';
