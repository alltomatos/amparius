-- Amparius: jornada atomica contrato -> ordem de servico -> agenda.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up
CREATE TABLE funeral.journey_mutation (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    contract_id uuid NOT NULL,
    service_order_id uuid NOT NULL,
    schedule_id uuid NOT NULL,
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) BETWEEN 1 AND 200),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT funeral_journey_contract_fk FOREIGN KEY (tenant_id, contract_id)
        REFERENCES funeral.contract (tenant_id, id),
    CONSTRAINT funeral_journey_service_order_fk FOREIGN KEY (tenant_id, service_order_id)
        REFERENCES funeral.service_order (tenant_id, id),
    CONSTRAINT funeral_journey_schedule_fk FOREIGN KEY (tenant_id, schedule_id)
        REFERENCES funeral.schedule (tenant_id, id),
    CONSTRAINT funeral_journey_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT funeral_journey_idempotency_uq UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX funeral_journey_contract_idx
    ON funeral.journey_mutation (tenant_id, contract_id, created_at, id);

-- A jornada e um registro de idempotencia auditavel; nao pode ser alterada ou removida.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION funeral.reject_journey_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tabela funeral.% e append-only: % proibido', TG_TABLE_NAME, TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER funeral_journey_append_only
    BEFORE UPDATE OR DELETE ON funeral.journey_mutation
    FOR EACH ROW EXECUTE FUNCTION funeral.reject_journey_mutation();

ALTER TABLE funeral.journey_mutation ENABLE ROW LEVEL SECURITY;
CREATE POLICY funeral_journey_tenant_isolation ON funeral.journey_mutation
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

GRANT SELECT, INSERT ON funeral.journey_mutation TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT ON funeral.journey_mutation FROM amparius_app;
DROP TABLE funeral.journey_mutation;
DROP FUNCTION funeral.reject_journey_mutation();
