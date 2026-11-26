-- Amparius control plane: contratos administrativos CP-06.
-- Forward-only: adiciona concorrência otimista, idempotência persistida e
-- auditoria append-only de alterações de quota.

-- +goose Up

ALTER TABLE entitlement.license_assignment
    ADD COLUMN version bigint NOT NULL DEFAULT 1
        CHECK (version > 0);

ALTER TABLE entitlement.license_pool
    ADD COLUMN version bigint NOT NULL DEFAULT 1
        CHECK (version > 0);

CREATE TABLE entitlement.mutation_idempotency (
    tenant_id uuid NOT NULL,
    idempotency_key text NOT NULL,
    operation text NOT NULL,
    request_hash text NOT NULL,
    result jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mutation_idempotency_pk PRIMARY KEY (tenant_id, idempotency_key),
    CONSTRAINT mutation_idempotency_key_ck CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    CONSTRAINT mutation_idempotency_hash_ck CHECK (length(request_hash) = 64)
);

CREATE TABLE entitlement.quota_event (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    module_code text NOT NULL REFERENCES entitlement.module (code),
    actor_id uuid NOT NULL,
    previous_quota integer NOT NULL CHECK (previous_quota >= 0),
    new_quota integer NOT NULL CHECK (new_quota >= 0),
    "at" timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT quota_event_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE INDEX quota_event_tenant_module_at_idx
    ON entitlement.quota_event (tenant_id, module_code, "at", id);

ALTER TABLE entitlement.mutation_idempotency ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlement.quota_event ENABLE ROW LEVEL SECURITY;

CREATE POLICY mutation_idempotency_tenant_isolation ON entitlement.mutation_idempotency
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY quota_event_tenant_isolation ON entitlement.quota_event
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION entitlement.reject_quota_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'entitlement.quota_event e append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER quota_event_no_update
    BEFORE UPDATE ON entitlement.quota_event
    FOR EACH ROW EXECUTE FUNCTION entitlement.reject_quota_event_mutation();
CREATE TRIGGER quota_event_no_delete
    BEFORE DELETE ON entitlement.quota_event
    FOR EACH ROW EXECUTE FUNCTION entitlement.reject_quota_event_mutation();

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'amparius_control_app') THEN
        GRANT SELECT, INSERT, UPDATE ON entitlement.mutation_idempotency TO amparius_control_app;
        GRANT SELECT, INSERT ON entitlement.quota_event TO amparius_control_app;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER quota_event_no_delete ON entitlement.quota_event;
DROP TRIGGER quota_event_no_update ON entitlement.quota_event;
DROP FUNCTION entitlement.reject_quota_event_mutation();
DROP TABLE entitlement.quota_event;
DROP TABLE entitlement.mutation_idempotency;
ALTER TABLE entitlement.license_pool DROP COLUMN version;
ALTER TABLE entitlement.license_assignment DROP COLUMN version;
