-- Amparius: agenda operacional de velorio e sepultamento.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up
INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('funeral_schedule_status', 'scheduled', 'Agendado'),
    ('funeral_schedule_status', 'cancelled', 'Cancelado'),
    ('funeral_schedule_status', 'completed', 'Concluido'),
    ('funeral_schedule_kind', 'wake', 'Velorio'),
    ('funeral_schedule_kind', 'burial', 'Sepultamento')
ON CONFLICT (domain_code, code) DO NOTHING;

CREATE TABLE funeral.location (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    company_id uuid NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    kind text NOT NULL CHECK (length(btrim(kind)) BETWEEN 1 AND 80),
    time_zone text NOT NULL CHECK (length(btrim(time_zone)) BETWEEN 1 AND 100),
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 200),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT funeral_location_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT funeral_location_tenant_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT funeral_location_idempotency_uq UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE funeral.resource (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    company_id uuid NOT NULL,
    location_id uuid NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    kind text NOT NULL CHECK (length(btrim(kind)) BETWEEN 1 AND 80),
    active boolean NOT NULL DEFAULT true,
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 200),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT funeral_resource_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT funeral_resource_tenant_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT funeral_resource_location_fk FOREIGN KEY (tenant_id, location_id)
        REFERENCES funeral.location (tenant_id, id),
    CONSTRAINT funeral_resource_idempotency_uq UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE funeral.schedule (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    company_id uuid NOT NULL,
    contract_id uuid NOT NULL,
    service_order_id uuid,
    kind text NOT NULL,
    location_id uuid NOT NULL,
    resource_id uuid,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    time_zone text NOT NULL CHECK (length(btrim(time_zone)) BETWEEN 1 AND 100),
    slot tstzrange GENERATED ALWAYS AS (tstzrange(starts_at, ends_at, '[)')) STORED,
    status text NOT NULL DEFAULT 'scheduled',
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 200),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT funeral_schedule_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT funeral_schedule_tenant_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT funeral_schedule_contract_fk FOREIGN KEY (tenant_id, contract_id)
        REFERENCES funeral.contract (tenant_id, id),
    CONSTRAINT funeral_schedule_service_order_fk FOREIGN KEY (tenant_id, service_order_id)
        REFERENCES funeral.service_order (tenant_id, id),
    CONSTRAINT funeral_schedule_location_fk FOREIGN KEY (tenant_id, location_id)
        REFERENCES funeral.location (tenant_id, id),
    CONSTRAINT funeral_schedule_resource_fk FOREIGN KEY (tenant_id, resource_id)
        REFERENCES funeral.resource (tenant_id, id),
    CONSTRAINT funeral_schedule_interval_ck CHECK (starts_at < ends_at),
    CONSTRAINT funeral_schedule_idempotency_uq UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE funeral.schedule_status_history (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    schedule_id uuid NOT NULL,
    from_status text,
    to_status text NOT NULL,
    reason text,
    changed_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT funeral_schedule_history_fk FOREIGN KEY (tenant_id, schedule_id)
        REFERENCES funeral.schedule (tenant_id, id),
    CONSTRAINT funeral_schedule_history_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE TABLE funeral.schedule_mutation (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    schedule_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('update', 'cancel', 'complete')),
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 200),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT funeral_schedule_mutation_fk FOREIGN KEY (tenant_id, schedule_id)
        REFERENCES funeral.schedule (tenant_id, id),
    CONSTRAINT funeral_schedule_mutation_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT funeral_schedule_mutation_key_uq UNIQUE (tenant_id, operation, idempotency_key)
);

CREATE INDEX funeral_location_company_idx ON funeral.location (tenant_id, company_id, name, id);
CREATE INDEX funeral_resource_company_idx ON funeral.resource (tenant_id, company_id, location_id, name, id);
CREATE INDEX funeral_schedule_contract_idx ON funeral.schedule (tenant_id, company_id, contract_id, starts_at, id);
CREATE INDEX funeral_schedule_window_idx ON funeral.schedule (tenant_id, company_id, starts_at, ends_at, id);
CREATE INDEX funeral_schedule_history_idx ON funeral.schedule_status_history (tenant_id, schedule_id, changed_at, id);
CREATE INDEX funeral_schedule_mutation_idx ON funeral.schedule_mutation (tenant_id, schedule_id, created_at, id);

-- O local e o recurso sao recursos exclusivos na janela semiaberta [starts_at, ends_at).
-- A constraint de exclusao e a decisao deterministica tambem sob INSERT concorrente.
ALTER TABLE funeral.schedule
    ADD CONSTRAINT funeral_schedule_location_no_overlap
    EXCLUDE USING gist (tenant_id WITH =, company_id WITH =, location_id WITH =, slot WITH &&)
    WHERE (status = 'scheduled');
ALTER TABLE funeral.schedule
    ADD CONSTRAINT funeral_schedule_resource_no_overlap
    EXCLUDE USING gist (tenant_id WITH =, company_id WITH =, resource_id WITH =, slot WITH &&)
    WHERE (status = 'scheduled' AND resource_id IS NOT NULL);

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
        (OLD.status = 'scheduled' AND NEW.status IN ('cancelled','completed'))
    ) THEN
        RAISE EXCEPTION 'transicao de agenda invalida: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Historico e comandos de idempotencia sao append-only; a agenda nao pode ser apagada.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.reject_schedule_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tabela funeral.% e append-only: % proibido', TG_TABLE_NAME, TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER funeral_schedule_validate BEFORE INSERT OR UPDATE ON funeral.schedule
    FOR EACH ROW EXECUTE FUNCTION funeral.validate_schedule();
CREATE TRIGGER funeral_schedule_history_append_only BEFORE UPDATE OR DELETE ON funeral.schedule_status_history
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_schedule_mutation();
CREATE TRIGGER funeral_schedule_mutation_append_only BEFORE UPDATE OR DELETE ON funeral.schedule_mutation
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_schedule_mutation();
CREATE TRIGGER funeral_schedule_preserve BEFORE DELETE ON funeral.schedule
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_schedule_mutation();
CREATE TRIGGER funeral_location_preserve BEFORE DELETE ON funeral.location
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_schedule_mutation();
CREATE TRIGGER funeral_resource_preserve BEFORE DELETE ON funeral.resource
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_schedule_mutation();

ALTER TABLE funeral.location ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.resource ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.schedule ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.schedule_status_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.schedule_mutation ENABLE ROW LEVEL SECURITY;

CREATE POLICY funeral_location_tenant_isolation ON funeral.location USING (tenant_id = current_setting('app.tenant_id')::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY funeral_resource_tenant_isolation ON funeral.resource USING (tenant_id = current_setting('app.tenant_id')::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY funeral_schedule_tenant_isolation ON funeral.schedule USING (tenant_id = current_setting('app.tenant_id')::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY funeral_schedule_history_tenant_isolation ON funeral.schedule_status_history USING (tenant_id = current_setting('app.tenant_id')::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY funeral_schedule_mutation_tenant_isolation ON funeral.schedule_mutation USING (tenant_id = current_setting('app.tenant_id')::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON funeral.location TO amparius_app;
GRANT SELECT, INSERT, UPDATE ON funeral.resource TO amparius_app;
GRANT SELECT, INSERT, UPDATE ON funeral.schedule TO amparius_app;
GRANT SELECT, INSERT ON funeral.schedule_status_history TO amparius_app;
GRANT SELECT, INSERT ON funeral.schedule_mutation TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT ON funeral.schedule_mutation FROM amparius_app;
REVOKE SELECT, INSERT ON funeral.schedule_status_history FROM amparius_app;
REVOKE SELECT, INSERT, UPDATE ON funeral.schedule FROM amparius_app;
REVOKE SELECT, INSERT, UPDATE ON funeral.resource FROM amparius_app;
REVOKE SELECT, INSERT, UPDATE ON funeral.location FROM amparius_app;
DROP TABLE funeral.schedule_mutation;
DROP TABLE funeral.schedule_status_history;
DROP TABLE funeral.schedule;
DROP TABLE funeral.resource;
DROP TABLE funeral.location;
DELETE FROM identity.domain_value WHERE domain_code IN ('funeral_schedule_status', 'funeral_schedule_kind');
