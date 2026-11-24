-- Amparius: outbox transacional, auditoria de tentativas e dispatch por tenant.
-- Forward-only: correcoes futuras devem ser novas migracoes.

-- +goose Up
CREATE SCHEMA IF NOT EXISTS outbox;

INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('outbox_event_status', 'pending', 'Pendente'),
    ('outbox_event_status', 'processing', 'Em processamento'),
    ('outbox_event_status', 'processed', 'Processado'),
    ('outbox_event_status', 'failed', 'Falhou'),
    ('outbox_attempt_outcome', 'claimed', 'Reivindicado'),
    ('outbox_attempt_outcome', 'succeeded', 'Sucesso'),
    ('outbox_attempt_outcome', 'retry', 'Nova tentativa'),
    ('outbox_attempt_outcome', 'failed', 'Falha definitiva')
ON CONFLICT (domain_code, code) DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.assert_domain_value(p_domain_code text, p_code text, p_field_name text)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF p_code IS NULL OR NOT EXISTS (SELECT 1 FROM identity.domain_value WHERE domain_code = p_domain_code AND code = p_code AND active) THEN
        RAISE EXCEPTION '% invalido para o dominio %: %', p_field_name, p_domain_code, p_code;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE TABLE outbox.event (
    id uuid NOT NULL DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    aggregate_type text NOT NULL CHECK (length(btrim(aggregate_type)) > 0),
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type ~ '^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$'),
    event_version integer NOT NULL CHECK (event_version > 0),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) > 0 AND length(idempotency_key) <= 200),
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 10 CHECK (max_attempts > 0 AND max_attempts <= 1000),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    PRIMARY KEY (id),
    CONSTRAINT event_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT event_tenant_idempotency_uq UNIQUE (tenant_id, idempotency_key),
    CONSTRAINT event_attempts_ck CHECK (attempts <= max_attempts),
    CONSTRAINT event_processed_ck CHECK ((status = 'processed') = (processed_at IS NOT NULL)),
    CONSTRAINT event_last_error_ck CHECK (last_error IS NULL OR length(btrim(last_error)) > 0)
);

CREATE TABLE outbox.event_attempt (
    id uuid NOT NULL DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    event_id uuid NOT NULL,
    attempt integer NOT NULL CHECK (attempt > 0),
    outcome text NOT NULL,
    error text,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT event_attempt_event_fk FOREIGN KEY (tenant_id, event_id)
        REFERENCES outbox.event (tenant_id, id),
    CONSTRAINT event_attempt_uq UNIQUE (tenant_id, event_id, attempt, outcome),
    CONSTRAINT event_attempt_error_ck CHECK (error IS NULL OR length(btrim(error)) > 0)
);

-- Indice de polling: apenas linhas elegiveis entram, mantendo o claim barato.
CREATE INDEX event_poll_idx ON outbox.event (next_attempt_at, created_at, id)
    WHERE status IN ('pending', 'processing');
CREATE INDEX event_tenant_status_idx ON outbox.event (tenant_id, status, next_attempt_at, id);
CREATE INDEX event_attempt_event_idx ON outbox.event_attempt (tenant_id, event_id, recorded_at);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.validate_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM outbox.assert_domain_value('outbox_event_status', NEW.status, 'status');
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'pending' THEN
            RAISE EXCEPTION 'outbox.event nasce pending, recebido %', NEW.status;
        END IF;
        IF NEW.attempts <> 0 OR NEW.processed_at IS NOT NULL THEN
            RAISE EXCEPTION 'outbox.event nasce sem tentativa e sem processed_at';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.id <> OLD.id OR NEW.tenant_id <> OLD.tenant_id OR NEW.aggregate_type <> OLD.aggregate_type
        OR NEW.aggregate_id <> OLD.aggregate_id OR NEW.event_type <> OLD.event_type
        OR NEW.event_version <> OLD.event_version OR NEW.payload <> OLD.payload
        OR NEW.idempotency_key <> OLD.idempotency_key OR NEW.max_attempts <> OLD.max_attempts
        OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'campos de identidade do outbox.event sao imutaveis';
    END IF;
    IF NEW.attempts < OLD.attempts OR NEW.attempts > OLD.attempts + 1 THEN
        RAISE EXCEPTION 'attempts avanca no maximo uma unidade por claim: % -> %', OLD.attempts, NEW.attempts;
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status = 'processing')
        OR (OLD.status = 'processing' AND NEW.status IN ('processing', 'processed', 'pending', 'failed'))
    ) THEN
        RAISE EXCEPTION 'transicao de status invalida: % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.validate_event_attempt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM outbox.assert_domain_value('outbox_attempt_outcome', NEW.outcome, 'outcome');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tabela outbox.% e append-only: % proibido', TG_TABLE_NAME, TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER event_validate
    BEFORE INSERT OR UPDATE ON outbox.event
    FOR EACH ROW EXECUTE FUNCTION outbox.validate_event();
CREATE TRIGGER event_history_preserve
    BEFORE DELETE ON outbox.event
    FOR EACH ROW EXECUTE FUNCTION outbox.reject_mutation();

CREATE TRIGGER event_attempt_validate
    BEFORE INSERT OR UPDATE ON outbox.event_attempt
    FOR EACH ROW EXECUTE FUNCTION outbox.validate_event_attempt();
CREATE TRIGGER event_attempt_append_only
    BEFORE UPDATE OR DELETE ON outbox.event_attempt
    FOR EACH ROW EXECUTE FUNCTION outbox.reject_mutation();

ALTER TABLE outbox.event ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox.event_attempt ENABLE ROW LEVEL SECURITY;

CREATE POLICY event_tenant_isolation ON outbox.event
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY event_attempt_tenant_isolation ON outbox.event_attempt
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Unica leitura cross-tenant do projeto: devolve apenas tenant_id de quem tem
-- evento elegivel, para o dispatcher abrir uma transacao por tenant sob RLS.
-- Executada como owner (migration role) para nao depender de BYPASSRLS no app.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION outbox.pending_tenants(p_limit integer DEFAULT 100)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT e.tenant_id
    FROM outbox.event e
    WHERE e.status IN ('pending', 'processing')
        AND e.next_attempt_at <= now()
        AND e.attempts < e.max_attempts
    GROUP BY e.tenant_id
    ORDER BY min(e.next_attempt_at)
    LIMIT greatest(1, least(coalesce(p_limit, 100), 1000))
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA outbox TO amparius_app;
GRANT SELECT, INSERT, UPDATE ON outbox.event TO amparius_app;
GRANT SELECT, INSERT ON outbox.event_attempt TO amparius_app;
REVOKE ALL ON FUNCTION outbox.pending_tenants(integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION outbox.pending_tenants(integer) TO amparius_app;

-- +goose Down
REVOKE EXECUTE ON FUNCTION outbox.pending_tenants(integer) FROM amparius_app;
REVOKE ALL ON SCHEMA outbox FROM amparius_app;
DROP SCHEMA outbox CASCADE;
DELETE FROM identity.domain_value WHERE domain_code IN ('outbox_event_status', 'outbox_attempt_outcome');
