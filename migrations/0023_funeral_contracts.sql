-- Amparius: contrato funerario, itens, familiares e historico de status.
-- Forward-only: correcoes futuras devem ser novas migracoes.

-- +goose Up
CREATE SCHEMA IF NOT EXISTS funeral;

INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('funeral_contract_status', 'draft', 'Rascunho'),
    ('funeral_contract_status', 'active', 'Ativo'),
    ('funeral_contract_status', 'completed', 'Concluido'),
    ('funeral_contract_status', 'cancelled', 'Cancelado')
ON CONFLICT (domain_code, code) DO NOTHING;

CREATE TABLE funeral.contract (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    company_id uuid NOT NULL,
    customer_party_id uuid NOT NULL,
    deceased_id uuid NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    total_amount numeric(18,2) NOT NULL CHECK (total_amount >= 0),
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) > 0 AND length(btrim(idempotency_key)) <= 200),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contract_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT contract_tenant_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT contract_tenant_customer_fk FOREIGN KEY (tenant_id, customer_party_id)
        REFERENCES party.party (tenant_id, id),
    CONSTRAINT contract_tenant_deceased_fk FOREIGN KEY (tenant_id, deceased_id)
        REFERENCES party.deceased (tenant_id, id),
    CONSTRAINT contract_tenant_idempotency_uq UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE funeral.contract_item (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    contract_id uuid NOT NULL,
    catalog_item_id uuid NOT NULL,
    item_kind text NOT NULL,
    description text NOT NULL CHECK (length(btrim(description)) > 0),
    unit text NOT NULL CHECK (length(btrim(unit)) > 0),
    quantity integer NOT NULL CHECK (quantity > 0),
    unit_amount numeric(18,2) NOT NULL CHECK (unit_amount >= 0),
    total_amount numeric(18,2) NOT NULL CHECK (total_amount >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contract_item_tenant_contract_fk FOREIGN KEY (tenant_id, contract_id)
        REFERENCES funeral.contract (tenant_id, id),
    CONSTRAINT contract_item_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT contract_item_tenant_catalog_fk FOREIGN KEY (tenant_id, catalog_item_id)
        REFERENCES catalog.catalog_item (tenant_id, id)
);

CREATE TABLE funeral.contract_family_member (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    contract_id uuid NOT NULL,
    family_link_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contract_family_tenant_contract_fk FOREIGN KEY (tenant_id, contract_id)
        REFERENCES funeral.contract (tenant_id, id),
    CONSTRAINT contract_family_tenant_link_fk FOREIGN KEY (tenant_id, family_link_id)
        REFERENCES party.family_link (tenant_id, id),
    CONSTRAINT contract_family_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT contract_family_contract_link_uq UNIQUE (tenant_id, contract_id, family_link_id)
);

CREATE TABLE funeral.contract_status_history (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    contract_id uuid NOT NULL,
    from_status text,
    to_status text NOT NULL,
    reason text,
    changed_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contract_status_tenant_contract_fk FOREIGN KEY (tenant_id, contract_id)
        REFERENCES funeral.contract (tenant_id, id),
    CONSTRAINT contract_status_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE INDEX contract_tenant_created_idx ON funeral.contract (tenant_id, created_at, id);
CREATE INDEX contract_item_tenant_contract_idx ON funeral.contract_item (tenant_id, contract_id, id);
CREATE INDEX contract_family_tenant_contract_idx ON funeral.contract_family_member (tenant_id, contract_id, id);
CREATE INDEX contract_status_tenant_contract_idx ON funeral.contract_status_history (tenant_id, contract_id, changed_at, id);

-- O banco valida o dominio mesmo quando a escrita nao passa pelo repositorio Go.
-- A transicao de status e deliberadamente restrita: estados terminais nao voltam.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.validate_contract() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM identity.domain_value
        WHERE domain_code = 'funeral_contract_status' AND code = NEW.status AND active
    ) THEN
        RAISE EXCEPTION 'status de contrato funerario invalido: %', NEW.status;
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

-- Historico e itens sao append-only. O contrato preserva o agregado e apenas
-- permite a transicao de status prevista acima.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tabela funeral.% e append-only: % proibido', TG_TABLE_NAME, TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER contract_validate
    BEFORE INSERT OR UPDATE OF status ON funeral.contract
    FOR EACH ROW EXECUTE FUNCTION funeral.validate_contract();
CREATE TRIGGER contract_item_append_only
    BEFORE UPDATE OR DELETE ON funeral.contract_item
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_mutation();
CREATE TRIGGER contract_family_append_only
    BEFORE UPDATE OR DELETE ON funeral.contract_family_member
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_mutation();
CREATE TRIGGER contract_status_append_only
    BEFORE UPDATE OR DELETE ON funeral.contract_status_history
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_mutation();
CREATE TRIGGER contract_history_preserve
    BEFORE DELETE ON funeral.contract
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_mutation();

ALTER TABLE funeral.contract ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.contract_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.contract_family_member ENABLE ROW LEVEL SECURITY;
ALTER TABLE funeral.contract_status_history ENABLE ROW LEVEL SECURITY;

CREATE POLICY contract_tenant_isolation ON funeral.contract
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY contract_item_tenant_isolation ON funeral.contract_item
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY contract_family_tenant_isolation ON funeral.contract_family_member
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY contract_status_tenant_isolation ON funeral.contract_status_history
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

GRANT USAGE ON SCHEMA funeral TO amparius_app;
GRANT SELECT, INSERT, UPDATE ON funeral.contract TO amparius_app;
GRANT SELECT, INSERT ON funeral.contract_item TO amparius_app;
GRANT SELECT, INSERT ON funeral.contract_family_member TO amparius_app;
GRANT SELECT, INSERT ON funeral.contract_status_history TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT, UPDATE ON funeral.contract FROM amparius_app;
REVOKE SELECT, INSERT ON funeral.contract_item FROM amparius_app;
REVOKE SELECT, INSERT ON funeral.contract_family_member FROM amparius_app;
REVOKE SELECT, INSERT ON funeral.contract_status_history FROM amparius_app;
REVOKE USAGE ON SCHEMA funeral FROM amparius_app;
DROP SCHEMA funeral CASCADE;
DELETE FROM identity.domain_value WHERE domain_code = 'funeral_contract_status';
