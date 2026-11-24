-- Amparius: contrato mínimo de usuário e onboarding inicial.
-- Forward-only: correções futuras devem ser novas migrações.

-- +goose Up
CREATE TABLE identity.app_user (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    email text NOT NULL CHECK (email = lower(btrim(email))),
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    status text NOT NULL DEFAULT 'pending_auth'
        CHECK (status IN ('pending_auth', 'active', 'disabled')),
    mfa_enabled boolean NOT NULL DEFAULT false,
    auth_subject text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_user_email_ck CHECK (
        email ~ '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'
    ),
    CONSTRAINT app_user_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT app_user_tenant_email_uq UNIQUE (tenant_id, email)
);

CREATE TABLE identity.role (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    code text NOT NULL CHECK (length(btrim(code)) > 0),
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT role_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT role_tenant_code_uq UNIQUE (tenant_id, code)
);

CREATE TABLE identity.user_role (
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    user_id uuid NOT NULL,
    role_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, role_id),
    CONSTRAINT user_role_tenant_user_fk FOREIGN KEY (tenant_id, user_id)
        REFERENCES identity.app_user (tenant_id, id),
    CONSTRAINT user_role_tenant_role_fk FOREIGN KEY (tenant_id, role_id)
        REFERENCES identity.role (tenant_id, id)
);

CREATE TABLE identity.onboarding_request (
    onboarding_key text PRIMARY KEY
        CHECK (length(btrim(onboarding_key)) BETWEEN 1 AND 200),
    tenant_id uuid NOT NULL,
    company_id uuid NOT NULL,
    establishment_id uuid NOT NULL,
    owner_user_id uuid NOT NULL,
    request_hash char(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT onboarding_tenant_fk FOREIGN KEY (tenant_id)
        REFERENCES identity.tenant (id),
    CONSTRAINT onboarding_tenant_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT onboarding_tenant_establishment_fk
        FOREIGN KEY (tenant_id, establishment_id)
        REFERENCES identity.establishment (tenant_id, id),
    CONSTRAINT onboarding_tenant_owner_fk FOREIGN KEY (tenant_id, owner_user_id)
        REFERENCES identity.app_user (tenant_id, id)
);

CREATE INDEX app_user_tenant_idx ON identity.app_user (tenant_id, id);
CREATE INDEX role_tenant_idx ON identity.role (tenant_id, id);
CREATE INDEX user_role_tenant_user_idx ON identity.user_role (tenant_id, user_id, role_id);
CREATE INDEX onboarding_tenant_idx ON identity.onboarding_request (tenant_id, created_at);

ALTER TABLE identity.app_user ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.role ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.user_role ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity.onboarding_request ENABLE ROW LEVEL SECURITY;

CREATE POLICY app_user_tenant_isolation ON identity.app_user
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY role_tenant_isolation ON identity.role
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY user_role_tenant_isolation ON identity.user_role
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY onboarding_tenant_isolation ON identity.onboarding_request
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- O papel da aplicação só lê o resultado sob RLS. Escrita de bootstrap passa
-- pela função abaixo; não há INSERT/UPDATE/DELETE direto nas tabelas novas.
GRANT USAGE ON SCHEMA identity TO amparius_app;
GRANT SELECT ON identity.tenant, identity.company, identity.establishment TO amparius_app;
GRANT SELECT ON identity.app_user, identity.role, identity.user_role,
    identity.onboarding_request TO amparius_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.create_onboarding(
    p_onboarding_key text,
    p_tenant_name text,
    p_tenant_document text,
    p_company_document text,
    p_company_name text,
    p_company_kind text,
    p_establishment_document text,
    p_establishment_name text,
    p_street text,
    p_number text,
    p_city text,
    p_state text,
    p_zip text,
    p_owner_email text,
    p_owner_name text
)
RETURNS TABLE (
    tenant_id uuid,
    company_id uuid,
    establishment_id uuid,
    owner_user_id uuid
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = identity, public
AS $$
DECLARE
    v_key text := btrim(p_onboarding_key);
    v_hash char(64);
    v_existing identity.onboarding_request%ROWTYPE;
    v_tenant_id uuid;
    v_company_id uuid;
    v_establishment_id uuid;
    v_owner_user_id uuid;
    v_role_id uuid;
BEGIN
    IF v_key IS NULL OR v_key = '' THEN
        RAISE EXCEPTION 'onboarding_key obrigatoria';
    END IF;

    IF p_owner_email IS NULL OR p_owner_email <> lower(btrim(p_owner_email)) THEN
        RAISE EXCEPTION 'owner_email deve estar normalizado em minusculas';
    END IF;

    v_hash := encode(digest(
        concat_ws(chr(31),
            v_key, p_tenant_name, p_tenant_document, p_company_document,
            p_company_name, p_company_kind, p_establishment_document,
            p_establishment_name, p_street, p_number, p_city, p_state,
            p_zip, p_owner_email, p_owner_name
        ), 'sha256'), 'hex');

    -- Serializa somente a mesma chave; não abre uma janela entre o replay e
    -- a criação dos registros vinculados.
    PERFORM pg_advisory_xact_lock(hashtextextended(v_key, 0));

    SELECT * INTO v_existing
      FROM identity.onboarding_request
     WHERE onboarding_key = v_key
     FOR UPDATE;
    IF FOUND THEN
        IF v_existing.request_hash <> v_hash THEN
            RAISE EXCEPTION 'onboarding_key ja usada com payload diferente';
        END IF;
        RETURN QUERY SELECT v_existing.tenant_id, v_existing.company_id,
            v_existing.establishment_id, v_existing.owner_user_id;
        RETURN;
    END IF;

    v_tenant_id := public.uuidv7();
    PERFORM set_config('app.tenant_id', v_tenant_id::text, true);

    INSERT INTO identity.tenant (id, name, document, regime, status)
    VALUES (v_tenant_id, p_tenant_name, p_tenant_document, 'economic_group', 'active');

    INSERT INTO identity.company (tenant_id, document, name, kind, status)
    VALUES (v_tenant_id, p_company_document, p_company_name, p_company_kind, 'active')
    RETURNING id INTO v_company_id;

    INSERT INTO identity.establishment (
        tenant_id, company_id, document, name, street, number, city, state, zip,
        is_headquarters, status
    )
    VALUES (
        v_tenant_id, v_company_id, p_establishment_document, p_establishment_name,
        p_street, p_number, p_city, p_state, p_zip, true, 'active'
    )
    RETURNING id INTO v_establishment_id;

    INSERT INTO identity.app_user (tenant_id, email, name, status)
    VALUES (v_tenant_id, p_owner_email, p_owner_name, 'pending_auth')
    RETURNING id INTO v_owner_user_id;

    INSERT INTO identity.role (tenant_id, code, name)
    VALUES (v_tenant_id, 'owner', 'Proprietario')
    RETURNING id INTO v_role_id;

    INSERT INTO identity.user_role (tenant_id, user_id, role_id)
    VALUES (v_tenant_id, v_owner_user_id, v_role_id);

    INSERT INTO identity.onboarding_request (
        onboarding_key, tenant_id, company_id, establishment_id, owner_user_id,
        request_hash
    )
    VALUES (
        v_key, v_tenant_id, v_company_id, v_establishment_id, v_owner_user_id,
        v_hash
    );

    RETURN QUERY SELECT v_tenant_id, v_company_id, v_establishment_id,
        v_owner_user_id;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.create_onboarding(
    text, text, text, text, text, text, text, text, text, text, text, text,
    text, text, text
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.create_onboarding(
    text, text, text, text, text, text, text, text, text, text, text, text,
    text, text, text
) TO amparius_app;

-- +goose Down
REVOKE ALL ON FUNCTION identity.create_onboarding(
    text, text, text, text, text, text, text, text, text, text, text, text,
    text, text, text
) FROM PUBLIC, amparius_app;
DROP FUNCTION identity.create_onboarding(
    text, text, text, text, text, text, text, text, text, text, text, text,
    text, text, text
);
REVOKE SELECT ON identity.tenant, identity.company, identity.establishment FROM amparius_app;
DROP TABLE identity.onboarding_request;
DROP TABLE identity.user_role;
DROP TABLE identity.role;
DROP TABLE identity.app_user;
