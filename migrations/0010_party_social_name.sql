-- Amparius: nome social opcional da pessoa.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up
ALTER TABLE party.person
    ADD COLUMN social_name text NULL;

-- Ausencia e valor vazio nao representam nome social. A normalizacao fica no
-- limite do banco para manter a mesma semantica em SaaS, relay e clientes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION party.normalize_person_social_name()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.social_name := NULLIF(btrim(NEW.social_name), '');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER person_normalize_social_name
    BEFORE INSERT OR UPDATE OF social_name ON party.person
    FOR EACH ROW EXECUTE FUNCTION party.normalize_person_social_name();

GRANT USAGE ON SCHEMA party TO amparius_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON party.person TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT, UPDATE, DELETE ON party.person FROM amparius_app;
REVOKE USAGE ON SCHEMA party FROM amparius_app;
DROP TRIGGER person_normalize_social_name ON party.person;
DROP FUNCTION party.normalize_person_social_name();
ALTER TABLE party.person DROP COLUMN social_name;
