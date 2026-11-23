-- Amparius: identidade e cadastro base.
-- Forward-only: correções futuras devem ser novas migrações.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS identity;

-- UUIDv7 compatível com PostgreSQL 16: 48 bits de epoch em milissegundos,
-- versão 7 e variante RFC 9562. A função não expõe sequência incremental.
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

-- Chamado pelo backend após validar o tenant no contexto autenticado.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.set_tenant_context(p_tenant_id uuid)
RETURNS void
LANGUAGE sql
VOLATILE
AS $$
    SELECT set_config('app.tenant_id', p_tenant_id::text, false);
$$;
-- +goose StatementEnd

CREATE TABLE identity.tenant (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    document text NOT NULL,
    regime text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE UNIQUE INDEX tenant_document_uq ON identity.tenant (document);

CREATE TABLE identity.domain_value (
    domain_code text NOT NULL,
    code text NOT NULL,
    label text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    PRIMARY KEY (domain_code, code)
);

INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('tenant_regime', 'economic_group', 'Grupo econômico'),
    ('tenant_status', 'active', 'Ativo'),
    ('tenant_status', 'suspended', 'Suspenso'),
    ('company_kind', 'funeral', 'Funerária'),
    ('company_kind', 'cemetery', 'Cemitério'),
    ('company_kind', 'floriculture', 'Floricultura'),
    ('company_status', 'active', 'Ativo'),
    ('company_status', 'suspended', 'Suspenso'),
    ('establishment_status', 'active', 'Ativo'),
    ('establishment_status', 'suspended', 'Suspenso');

CREATE TABLE identity.company (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    document text NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    kind text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT company_tenant_document_uq UNIQUE (tenant_id, document),
    CONSTRAINT company_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE INDEX company_tenant_idx ON identity.company (tenant_id, id);

CREATE TABLE identity.establishment (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    company_id uuid NOT NULL,
    document text NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    state_registration text,
    municipal_registration text,
    street text,
    number text,
    city text,
    state text,
    zip text,
    is_headquarters boolean NOT NULL DEFAULT false,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT establishment_company_same_tenant_fk
        FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT establishment_tenant_document_uq UNIQUE (tenant_id, document)
);

CREATE INDEX establishment_tenant_company_idx
    ON identity.establishment (tenant_id, company_id, id);

ALTER TABLE identity.tenant ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.company ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.establishment ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON identity.tenant
    USING (id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (id = current_setting('app.tenant_id')::uuid);

CREATE POLICY company_tenant_isolation ON identity.company
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE POLICY establishment_tenant_isolation ON identity.establishment
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- +goose Down
DROP SCHEMA identity CASCADE;
DROP FUNCTION public.set_tenant_context(uuid);
DROP FUNCTION public.uuidv7();
