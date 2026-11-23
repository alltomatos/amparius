-- Amparius control plane: entitlement por modulo e por CNPJ.
-- Banco alvo: amparius_control. A celula usa outro DSN e outro diretorio.
-- tenant_id/company_id sao resolvidos da sessao autenticada; nunca do corpo HTTP.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE SCHEMA IF NOT EXISTS entitlement;

-- UUIDv7 compatível com PostgreSQL 16.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.uuidv7()
RETURNS uuid
LANGUAGE plpgsql
VOLATILE
AS $$
DECLARE
    timestamp_hex text;
    random_hex text;
    variant_hex text;
BEGIN
    timestamp_hex := lpad(to_hex(floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint), 12, '0');
    random_hex := encode(gen_random_bytes(10), 'hex');
    variant_hex := to_hex((get_byte(decode(substr(random_hex, 4, 2), 'hex'), 0) & 3) | 8);
    RETURN (
        substr(timestamp_hex, 1, 8) || '-' ||
        substr(timestamp_hex, 9, 4) || '-' ||
        '7' || substr(random_hex, 1, 3) || '-' ||
        variant_hex || substr(random_hex, 5, 3) || '-' ||
        substr(random_hex, 8, 12)
    )::uuid;
END;
$$;
-- +goose StatementEnd

CREATE TABLE entitlement.module_kind (
    code text PRIMARY KEY,
    name text NOT NULL CHECK (length(btrim(name)) > 0)
);

CREATE TABLE entitlement.license_status (
    code text PRIMARY KEY,
    name text NOT NULL CHECK (length(btrim(name)) > 0)
);

CREATE TABLE entitlement.entitlement_action (
    code text PRIMARY KEY,
    name text NOT NULL CHECK (length(btrim(name)) > 0)
);

CREATE TABLE entitlement.entitlement_origin (
    code text PRIMARY KEY,
    name text NOT NULL CHECK (length(btrim(name)) > 0)
);

INSERT INTO entitlement.module_kind (code, name) VALUES
    ('core', 'Nucleo'), ('optional', 'Opcional');
INSERT INTO entitlement.license_status (code, name) VALUES
    ('active', 'Ativa'), ('inactive', 'Inativa'), ('revoked', 'Revogada');
INSERT INTO entitlement.entitlement_action (code, name) VALUES
    ('activated', 'Ativada'), ('deactivated', 'Desativada'), ('revoked', 'Revogada');
INSERT INTO entitlement.entitlement_origin (code, name) VALUES
    ('self_service', 'Autoatendimento'), ('admin', 'Administrador SaaS');

CREATE TABLE entitlement.module (
    code text PRIMARY KEY,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    kind text NOT NULL REFERENCES entitlement.module_kind (code),
    sort_order integer NOT NULL CHECK (sort_order > 0),
    active boolean NOT NULL DEFAULT true
);

INSERT INTO entitlement.module (code, name, kind, sort_order, active) VALUES
    ('identity', 'Identity', 'core', 1, true),
    ('party', 'Party', 'core', 2, true),
    ('catalog', 'Catalog', 'core', 3, true),
    ('finance', 'Financeiro', 'core', 4, true),
    ('fiscal', 'Fiscal', 'core', 5, true),
    ('sync', 'Sincronizacao', 'core', 6, true),
    ('reporting', 'Relatorios', 'core', 7, true),
    ('funeral', 'Funeraria', 'optional', 8, true),
    ('cemetery', 'Cemiterio', 'optional', 9, true),
    ('cremation', 'Cremacao', 'optional', 10, true),
    ('floriculture', 'Floricultura', 'optional', 11, true),
    ('inventory', 'Estoque', 'optional', 12, true),
    ('documents', 'Documentos', 'optional', 13, true),
    ('ai', 'Inteligencia Artificial', 'optional', 14, true);

CREATE TABLE entitlement.license_pool (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    module_code text NOT NULL REFERENCES entitlement.module (code),
    quota integer NOT NULL CHECK (quota >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT license_pool_tenant_module_uq UNIQUE (tenant_id, module_code)
);

CREATE TABLE entitlement.license_assignment (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    module_code text NOT NULL REFERENCES entitlement.module (code),
    company_id uuid NOT NULL,
    valid_from date NOT NULL,
    valid_to date,
    status text NOT NULL REFERENCES entitlement.license_status (code),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT license_assignment_validity_ck CHECK (valid_to IS NULL OR valid_to >= valid_from),
    CONSTRAINT license_assignment_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE TABLE entitlement.user_quota (
    tenant_id uuid PRIMARY KEY,
    seats integer NOT NULL CHECK (seats >= 0),
    used integer NOT NULL DEFAULT 0 CHECK (used >= 0 AND used <= seats),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE entitlement.entitlement_event (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    module_code text NOT NULL REFERENCES entitlement.module (code),
    company_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action text NOT NULL REFERENCES entitlement.entitlement_action (code),
    origin text NOT NULL REFERENCES entitlement.entitlement_origin (code),
    "at" timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entitlement_event_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE INDEX license_pool_tenant_module_idx
    ON entitlement.license_pool (tenant_id, module_code);
CREATE INDEX license_assignment_tenant_module_company_idx
    ON entitlement.license_assignment (tenant_id, module_code, company_id, valid_from);
CREATE INDEX entitlement_event_tenant_module_company_idx
    ON entitlement.entitlement_event (tenant_id, module_code, company_id, "at");

ALTER TABLE entitlement.license_pool ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlement.license_assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlement.user_quota ENABLE ROW LEVEL SECURITY;
ALTER TABLE entitlement.entitlement_event ENABLE ROW LEVEL SECURITY;

CREATE POLICY license_pool_tenant_isolation ON entitlement.license_pool
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY license_assignment_tenant_isolation ON entitlement.license_assignment
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY user_quota_tenant_isolation ON entitlement.user_quota
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY entitlement_event_tenant_isolation ON entitlement.entitlement_event
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Eventos de auditoria sao append-only, inclusive para o owner da tabela.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION entitlement.reject_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'entitlement.entitlement_event e append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER entitlement_event_no_update
    BEFORE UPDATE ON entitlement.entitlement_event
    FOR EACH ROW EXECUTE FUNCTION entitlement.reject_event_mutation();
CREATE TRIGGER entitlement_event_no_delete
    BEFORE DELETE ON entitlement.entitlement_event
    FOR EACH ROW EXECUTE FUNCTION entitlement.reject_event_mutation();

-- +goose Down
DROP SCHEMA entitlement CASCADE;
DROP FUNCTION public.uuidv7();
