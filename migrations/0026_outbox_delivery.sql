-- Amparius: registro idempotente de entrega da outbox (OUT-01B, #29).
-- Forward-only: nao edita 0007 nem 0025. O efeito minimo do worker e a
-- confirmacao de entrega, gravada na MESMA transacao do status 'processed'.
-- Nao carrega payload, PII nem segredo: so referencias opacas e metadados.

-- +goose Up
CREATE TABLE outbox.delivery (
    id uuid NOT NULL DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    event_id uuid NOT NULL,
    event_type text NOT NULL,
    attempt integer NOT NULL CHECK (attempt > 0),
    delivered_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT delivery_event_fk FOREIGN KEY (tenant_id, event_id)
        REFERENCES outbox.event (tenant_id, id),
    CONSTRAINT delivery_event_uq UNIQUE (tenant_id, event_id),
    CONSTRAINT delivery_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE INDEX delivery_tenant_idx ON outbox.delivery (tenant_id, delivered_at, id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.reject_delivery_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tabela outbox.delivery e append-only: % proibido', TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER delivery_append_only
    BEFORE UPDATE OR DELETE ON outbox.delivery
    FOR EACH ROW EXECUTE FUNCTION outbox.reject_delivery_mutation();

ALTER TABLE outbox.delivery ENABLE ROW LEVEL SECURITY;

CREATE POLICY delivery_tenant_isolation ON outbox.delivery
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- O papel de aplicacao so insere e le a propria entrega: sem UPDATE/DELETE.
GRANT SELECT, INSERT ON outbox.delivery TO amparius_app;

-- +goose Down
REVOKE ALL ON outbox.delivery FROM amparius_app;
DROP TABLE outbox.delivery;
DROP FUNCTION outbox.reject_delivery_mutation();
