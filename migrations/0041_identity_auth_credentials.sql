-- Amparius: emissor próprio de credencial do tenant (AUTH-01B, ADR 0048).
-- Forward-only: correções futuras devem ser novas migrações.

-- +goose Up
ALTER TABLE identity.app_user
    ADD COLUMN password_hash text,
    ADD COLUMN password_algo text,
    ADD COLUMN password_updated_at timestamptz,
    ADD COLUMN failed_attempts int NOT NULL DEFAULT 0,
    ADD COLUMN locked_until timestamptz;

-- GRANT por coluna (ADR 0048 §2): a aplicação nunca ganha permissão de
-- alterar email/tenant_id/name pelo caminho de autenticação.
GRANT UPDATE (password_hash, password_algo, password_updated_at,
    failed_attempts, locked_until, status, auth_subject)
    ON identity.app_user TO amparius_app;

CREATE TABLE identity.refresh_token (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    user_id uuid NOT NULL,
    company_id uuid NOT NULL,
    family_id uuid NOT NULL,
    token_hash char(64) NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    issued_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    rotated_at timestamptz,
    revoked_at timestamptz,
    revoked_reason text,
    CONSTRAINT refresh_token_tenant_user_fk FOREIGN KEY (tenant_id, user_id)
        REFERENCES identity.app_user (tenant_id, id),
    -- Guarda a empresa escolhida na sessão: POST /v1/auth/refresh sem
    -- companyId precisa manter a mesma empresa do token anterior (contrato
    -- auth-login-http-v1), e o refresh não recebe Authorization/claims.
    CONSTRAINT refresh_token_tenant_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id)
);
CREATE UNIQUE INDEX refresh_token_tenant_hash_uq
    ON identity.refresh_token (tenant_id, token_hash);
CREATE INDEX refresh_token_tenant_family_idx
    ON identity.refresh_token (tenant_id, family_id);
CREATE INDEX refresh_token_tenant_user_idx
    ON identity.refresh_token (tenant_id, user_id);

ALTER TABLE identity.refresh_token ENABLE ROW LEVEL SECURITY;
CREATE POLICY refresh_token_tenant_isolation ON identity.refresh_token
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Rotação/revogação acontecem já com o tenant resolvido (sessão existente);
-- só a resolução inicial por hash cru precisa da função SECURITY DEFINER
-- abaixo, que nunca concede BYPASSRLS.
GRANT SELECT, INSERT ON identity.refresh_token TO amparius_app;
GRANT UPDATE (rotated_at, revoked_at, revoked_reason)
    ON identity.refresh_token TO amparius_app;

CREATE TABLE identity.login_audit (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    user_id uuid,
    result text NOT NULL CHECK (result IN (
        'success', 'invalid_credentials', 'locked', 'pending_auth',
        'refresh_reuse_detected', 'logout', 'revoke'
    )),
    at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT login_audit_tenant_user_fk FOREIGN KEY (tenant_id, user_id)
        REFERENCES identity.app_user (tenant_id, id)
);
CREATE INDEX login_audit_tenant_at_idx ON identity.login_audit (tenant_id, at);

ALTER TABLE identity.login_audit ENABLE ROW LEVEL SECURITY;
CREATE POLICY login_audit_tenant_isolation ON identity.login_audit
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Nunca e-mail, senha, token ou IP cru nesta tabela (ADR 0048 §11).
GRANT SELECT, INSERT ON identity.login_audit TO amparius_app;

CREATE TABLE identity.credential_token (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    user_id uuid NOT NULL,
    token_hash char(64) NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    purpose text NOT NULL CHECK (purpose IN ('bootstrap', 'recovery')),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT credential_token_tenant_user_fk FOREIGN KEY (tenant_id, user_id)
        REFERENCES identity.app_user (tenant_id, id)
);
CREATE UNIQUE INDEX credential_token_tenant_hash_uq
    ON identity.credential_token (tenant_id, token_hash);
CREATE INDEX credential_token_tenant_user_idx
    ON identity.credential_token (tenant_id, user_id);

ALTER TABLE identity.credential_token ENABLE ROW LEVEL SECURITY;
CREATE POLICY credential_token_tenant_isolation ON identity.credential_token
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

GRANT SELECT, INSERT ON identity.credential_token TO amparius_app;

-- Resolução de tenant no login (ADR 0048 §5, SQL literal do desenho). Dono é
-- o papel de migração: ignora RLS por ser dono das tabelas, então
-- amparius_app continua sem BYPASSRLS.
-- +goose StatementBegin
CREATE FUNCTION identity.resolve_login_candidates(p_email text)
RETURNS TABLE (
    tenant_id uuid, user_id uuid, password_hash text, password_algo text,
    status text, failed_attempts int, locked_until timestamptz
)
LANGUAGE sql SECURITY DEFINER SET search_path = identity, public
AS $$
    SELECT tenant_id, id, password_hash, password_algo, status,
           failed_attempts, locked_until
      FROM identity.app_user
     WHERE email = lower(btrim(p_email));
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.resolve_login_candidates(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.resolve_login_candidates(text) TO amparius_app;

-- Resolução de refresh token por hash, sem tenant conhecido ainda (mesmo
-- padrão de §5, necessário para POST /v1/auth/refresh). Nunca expõe o token
-- cru (só o hash já recebido) nem outra coluna fora do minimo operacional.
-- +goose StatementBegin
CREATE FUNCTION identity.resolve_refresh_token(p_token_hash text)
RETURNS TABLE (
    tenant_id uuid, id uuid, user_id uuid, company_id uuid, family_id uuid,
    expires_at timestamptz, rotated_at timestamptz, revoked_at timestamptz
)
LANGUAGE sql SECURITY DEFINER SET search_path = identity, public
AS $$
    SELECT tenant_id, id, user_id, company_id, family_id, expires_at, rotated_at, revoked_at
      FROM identity.refresh_token
     WHERE token_hash = p_token_hash;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.resolve_refresh_token(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.resolve_refresh_token(text) TO amparius_app;

-- Resolução de token de bootstrap/recuperação por hash, mesmo padrão.
-- +goose StatementBegin
CREATE FUNCTION identity.resolve_credential_token(p_token_hash text)
RETURNS TABLE (
    tenant_id uuid, user_id uuid, purpose text,
    expires_at timestamptz, consumed_at timestamptz
)
LANGUAGE sql SECURITY DEFINER SET search_path = identity, public
AS $$
    SELECT tenant_id, user_id, purpose, expires_at, consumed_at
      FROM identity.credential_token
     WHERE token_hash = p_token_hash;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.resolve_credential_token(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.resolve_credential_token(text) TO amparius_app;

-- +goose StatementBegin
-- Consome o token de bootstrap/recuperação e ativa a senha em uma única
-- transação (evita corrida entre resolver e consumir o mesmo token).
CREATE OR REPLACE FUNCTION identity.complete_credential(
    p_token_hash text, p_purpose text, p_password_hash text, p_password_algo text
)
RETURNS TABLE (tenant_id uuid, user_id uuid)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = identity, public
AS $$
DECLARE
    v_token identity.credential_token%ROWTYPE;
BEGIN
    SELECT * INTO v_token
      FROM identity.credential_token
     WHERE token_hash = p_token_hash
     FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'credential_token nao encontrado';
    END IF;
    IF v_token.purpose <> p_purpose THEN
        RAISE EXCEPTION 'credential_token com finalidade divergente';
    END IF;
    IF v_token.consumed_at IS NOT NULL THEN
        RAISE EXCEPTION 'credential_token ja consumido';
    END IF;
    IF v_token.expires_at <= now() THEN
        RAISE EXCEPTION 'credential_token expirado';
    END IF;

    UPDATE identity.credential_token
       SET consumed_at = now()
     WHERE token_hash = p_token_hash;

    UPDATE identity.app_user
       SET password_hash = p_password_hash,
           password_algo = p_password_algo,
           password_updated_at = now(),
           auth_subject = id::text,
           failed_attempts = 0,
           locked_until = NULL,
           status = CASE WHEN p_purpose = 'bootstrap' THEN 'active' ELSE status END,
           updated_at = now()
     WHERE identity.app_user.tenant_id = v_token.tenant_id
       AND identity.app_user.id = v_token.user_id;

    RETURN QUERY SELECT v_token.tenant_id, v_token.user_id;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.complete_credential(text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.complete_credential(text, text, text, text) TO amparius_app;

-- +goose StatementBegin
-- Cria o usuário convidado (pending_auth) com papéis e empresas vinculados,
-- mesmo padrão privilegiado de identity.create_onboarding (migration 0009):
-- dono ignora RLS, amparius_app só ganha EXECUTE, nunca INSERT direto em
-- app_user/user_role/user_company pelo caminho de convite.
CREATE OR REPLACE FUNCTION identity.create_invite(
    p_tenant_id uuid, p_email text, p_name text,
    p_role_codes text[], p_company_ids uuid[]
)
RETURNS TABLE (user_id uuid)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = identity, public
AS $$
DECLARE
    v_user_id uuid;
    v_role_id uuid;
    v_code text;
    v_company_id uuid;
BEGIN
    IF p_tenant_id IS NULL OR p_email IS NULL OR p_email <> lower(btrim(p_email)) THEN
        RAISE EXCEPTION 'dados de convite invalidos';
    END IF;

    INSERT INTO identity.app_user (tenant_id, email, name, status)
    VALUES (p_tenant_id, p_email, p_name, 'pending_auth')
    RETURNING id INTO v_user_id;

    FOREACH v_code IN ARRAY p_role_codes LOOP
        SELECT id INTO v_role_id FROM identity.role
         WHERE tenant_id = p_tenant_id AND code = v_code;
        IF v_role_id IS NULL THEN
            RAISE EXCEPTION 'papel desconhecido no convite: %', v_code;
        END IF;
        INSERT INTO identity.user_role (tenant_id, user_id, role_id)
        VALUES (p_tenant_id, v_user_id, v_role_id);
    END LOOP;

    FOREACH v_company_id IN ARRAY p_company_ids LOOP
        INSERT INTO identity.user_company (tenant_id, user_id, company_id)
        VALUES (p_tenant_id, v_user_id, v_company_id);
    END LOOP;

    RETURN QUERY SELECT v_user_id;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION identity.create_invite(uuid, text, text, text[], uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION identity.create_invite(uuid, text, text, text[], uuid[]) TO amparius_app;

-- +goose Down
REVOKE ALL ON FUNCTION identity.create_invite(uuid, text, text, text[], uuid[]) FROM PUBLIC, amparius_app;
DROP FUNCTION identity.create_invite(uuid, text, text, text[], uuid[]);

REVOKE ALL ON FUNCTION identity.complete_credential(text, text, text, text) FROM PUBLIC, amparius_app;
DROP FUNCTION identity.complete_credential(text, text, text, text);

REVOKE ALL ON FUNCTION identity.resolve_credential_token(text) FROM PUBLIC, amparius_app;
DROP FUNCTION identity.resolve_credential_token(text);

REVOKE ALL ON FUNCTION identity.resolve_refresh_token(text) FROM PUBLIC, amparius_app;
DROP FUNCTION identity.resolve_refresh_token(text);

REVOKE ALL ON FUNCTION identity.resolve_login_candidates(text) FROM PUBLIC, amparius_app;
DROP FUNCTION identity.resolve_login_candidates(text);

DROP TABLE identity.credential_token;
DROP TABLE identity.login_audit;
DROP TABLE identity.refresh_token;

REVOKE UPDATE (password_hash, password_algo, password_updated_at,
    failed_attempts, locked_until, status, auth_subject)
    ON identity.app_user FROM amparius_app;
ALTER TABLE identity.app_user
    DROP COLUMN password_hash,
    DROP COLUMN password_algo,
    DROP COLUMN password_updated_at,
    DROP COLUMN failed_attempts,
    DROP COLUMN locked_until;
