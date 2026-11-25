-- Amparius: trilha de auditoria e evidência de privacidade por tenant.
-- Forward-only: correções futuras devem ser novas migrações.

-- +goose Up
CREATE SCHEMA IF NOT EXISTS audit;
CREATE SCHEMA IF NOT EXISTS privacy;

CREATE TABLE audit.log (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    actor_user_id uuid,
    actor_kind text NOT NULL DEFAULT 'system'
        CHECK (actor_kind IN ('user', 'system', 'service')),
    company_id uuid,
    action text NOT NULL CHECK (action ~ '^[a-z][a-z0-9_.:-]{1,119}$'),
    resource_type text NOT NULL CHECK (resource_type ~ '^[a-z][a-z0-9_.:-]{1,119}$'),
    resource_id uuid,
    outcome text NOT NULL CHECK (outcome IN ('success', 'denied', 'failure')),
    correlation_id text CHECK (correlation_id IS NULL OR correlation_id ~ '^[A-Za-z0-9._:-]{8,128}$'),
    trace_id text CHECK (trace_id IS NULL OR trace_id ~ '^[0-9a-f]{32}$'),
    detail jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(detail) = 'object' AND octet_length(detail::text) <= 16384),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    retention_until timestamptz NOT NULL,
    CONSTRAINT audit_actor_tenant_fk FOREIGN KEY (tenant_id, actor_user_id)
        REFERENCES identity.app_user (tenant_id, id),
    CONSTRAINT audit_company_tenant_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT audit_retention_after_occurrence_ck CHECK (retention_until > occurred_at),
    CONSTRAINT audit_actor_kind_ck CHECK (
        (actor_kind = 'user' AND actor_user_id IS NOT NULL)
        OR (actor_kind IN ('system', 'service') AND actor_user_id IS NULL)
    )
);

CREATE INDEX audit_log_tenant_occurred_idx ON audit.log (tenant_id, occurred_at DESC, id DESC);
CREATE INDEX audit_log_tenant_actor_idx ON audit.log (tenant_id, actor_user_id, occurred_at DESC);

CREATE TABLE privacy.consent_record (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    subject_type text NOT NULL CHECK (subject_type ~ '^[a-z][a-z0-9_.:-]{1,79}$'),
    subject_id uuid NOT NULL,
    purpose_code text NOT NULL CHECK (purpose_code ~ '^[a-z][a-z0-9_.:-]{1,119}$'),
    legal_basis_code text NOT NULL CHECK (legal_basis_code ~ '^[a-z][a-z0-9_.:-]{1,119}$'),
    status text NOT NULL CHECK (status IN ('granted', 'withdrawn', 'denied', 'not_required')),
    evidence_ref text CHECK (evidence_ref IS NULL OR evidence_ref ~ '^[A-Za-z0-9._:-]{1,255}$'),
    recorded_by_user_id uuid,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    retention_until timestamptz NOT NULL,
    CONSTRAINT consent_actor_tenant_fk FOREIGN KEY (tenant_id, recorded_by_user_id)
        REFERENCES identity.app_user (tenant_id, id),
    CONSTRAINT consent_retention_after_recording_ck CHECK (retention_until > recorded_at),
    CONSTRAINT consent_status_evidence_ck CHECK (
        status NOT IN ('granted', 'withdrawn') OR evidence_ref IS NOT NULL
    )
);

CREATE INDEX consent_tenant_subject_idx
    ON privacy.consent_record (tenant_id, subject_type, subject_id, recorded_at DESC);

-- A auditoria e os registros de consentimento são históricos. Alteração ou
-- remoção exige uma nova ocorrência, preservando a evidência original.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit.reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tabela % e append-only: % proibido', TG_TABLE_NAME, TG_OP;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_log_append_only
    BEFORE UPDATE OR DELETE ON audit.log
    FOR EACH ROW EXECUTE FUNCTION audit.reject_mutation();
CREATE TRIGGER consent_record_append_only
    BEFORE UPDATE OR DELETE ON privacy.consent_record
    FOR EACH ROW EXECUTE FUNCTION audit.reject_mutation();

-- A guarda de payload da outbox já implementa a varredura de chaves/valores
-- sensíveis. Reutilizá-la mantém o mesmo fail-closed para trilhas e eventos;
-- a API Go também valida uma allowlist de detalhe antes do INSERT.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit.validate_detail() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM outbox.assert_payload_safe(NEW.detail);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_log_detail_guard
    BEFORE INSERT ON audit.log
    FOR EACH ROW EXECUTE FUNCTION audit.validate_detail();

ALTER TABLE audit.log ENABLE ROW LEVEL SECURITY;
ALTER TABLE privacy.consent_record ENABLE ROW LEVEL SECURITY;

CREATE POLICY audit_log_tenant_isolation ON audit.log
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY consent_tenant_isolation ON privacy.consent_record
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

GRANT USAGE ON SCHEMA audit, privacy TO amparius_app;
GRANT SELECT, INSERT ON audit.log, privacy.consent_record TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT ON audit.log, privacy.consent_record FROM amparius_app;
REVOKE USAGE ON SCHEMA audit, privacy FROM amparius_app;
DROP SCHEMA privacy CASCADE;
DROP SCHEMA audit CASCADE;
