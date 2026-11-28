-- Amparius: vincula o owner recém-criado pelo onboarding à empresa sede.
-- Forward-only: correções futuras devem ser novas migrações.
--
-- identity.create_onboarding (migration 0009) nunca inseriu em
-- identity.user_company porque essa tabela só foi criada na migration 0032,
-- sem retroalimentar a função. Resultado: nenhum owner onboardado consegue
-- logar em sua própria empresa (ResolveSubject/companyAllowed exigem o
-- vínculo). AUTH-01B expôs a lacuna nos testes de integração do login.

-- +goose Up
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
        -- Replay de uma chave já processada: garante o vínculo mesmo que a
        -- chamada original tenha ocorrido antes desta correção.
        INSERT INTO identity.user_company (tenant_id, user_id, company_id)
        VALUES (v_existing.tenant_id, v_existing.owner_user_id, v_existing.company_id)
        ON CONFLICT DO NOTHING;
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

    INSERT INTO identity.user_company (tenant_id, user_id, company_id)
    VALUES (v_tenant_id, v_owner_user_id, v_company_id);

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

-- Backfill: onboardings concluídos antes desta correção (se algum existir)
-- ganham o vínculo que a função anterior nunca criou.
INSERT INTO identity.user_company (tenant_id, user_id, company_id)
SELECT o.tenant_id, o.owner_user_id, o.company_id
  FROM identity.onboarding_request o
 WHERE NOT EXISTS (
    SELECT 1 FROM identity.user_company uc
     WHERE uc.tenant_id = o.tenant_id
       AND uc.user_id = o.owner_user_id
       AND uc.company_id = o.company_id
 );

-- +goose Down
DELETE FROM identity.user_company uc
 USING identity.onboarding_request o
WHERE uc.tenant_id = o.tenant_id
  AND uc.user_id = o.owner_user_id
  AND uc.company_id = o.company_id;

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
