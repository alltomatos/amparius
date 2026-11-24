-- Amparius: metadados de transporte da emissão fiscal.
-- Forward-only: correções futuras devem ser novas migrations.

-- +goose Up
INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('fiscal_emission_environment', 'producao_restrita', 'Produção restrita'),
    ('fiscal_emission_environment', 'producao', 'Produção')
ON CONFLICT (domain_code, code) DO NOTHING;

ALTER TABLE fiscal.emission_attempt
    ADD COLUMN http_status integer,
    ADD COLUMN ambiente text,
    ADD COLUMN chave_acesso text,
    ADD COLUMN dps_id text,
    ADD COLUMN request_hash char(64);

ALTER TABLE fiscal.emission_attempt
    ADD CONSTRAINT emission_attempt_http_status_ck
        CHECK (http_status IS NULL OR http_status BETWEEN 100 AND 599),
    ADD CONSTRAINT emission_attempt_request_hash_ck
        CHECK (request_hash IS NULL OR (request_hash ~ '^[0-9a-f]{64}$' AND request_hash = lower(request_hash))),
    ADD CONSTRAINT emission_attempt_chave_acesso_ck
        CHECK (chave_acesso IS NULL OR length(btrim(chave_acesso)) > 0),
    ADD CONSTRAINT emission_attempt_dps_id_ck
        CHECK (dps_id IS NULL OR length(btrim(dps_id)) > 0);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fiscal.validate_emission_attempt_metadata() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.ambiente IS NOT NULL THEN
        PERFORM fiscal.assert_domain_value('fiscal_emission_environment', NEW.ambiente, 'ambiente');
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER emission_attempt_validate_metadata
    BEFORE INSERT OR UPDATE OF ambiente ON fiscal.emission_attempt
    FOR EACH ROW EXECUTE FUNCTION fiscal.validate_emission_attempt_metadata();

COMMENT ON COLUMN fiscal.emission_attempt.http_status IS 'Status HTTP cru do transporte, quando houve resposta.';
COMMENT ON COLUMN fiscal.emission_attempt.ambiente IS 'Ambiente do adaptador: producao_restrita ou producao.';
COMMENT ON COLUMN fiscal.emission_attempt.chave_acesso IS 'Chave retornada pelo fisco, sem segredo.';
COMMENT ON COLUMN fiscal.emission_attempt.dps_id IS 'Identificador da DPS para reconciliação.';
COMMENT ON COLUMN fiscal.emission_attempt.request_hash IS 'SHA-256 hexadecimal minúsculo do XML enviado.';

-- +goose Down
DROP TRIGGER emission_attempt_validate_metadata ON fiscal.emission_attempt;
DROP FUNCTION fiscal.validate_emission_attempt_metadata();
ALTER TABLE fiscal.emission_attempt
    DROP CONSTRAINT emission_attempt_http_status_ck,
    DROP CONSTRAINT emission_attempt_request_hash_ck,
    DROP CONSTRAINT emission_attempt_chave_acesso_ck,
    DROP CONSTRAINT emission_attempt_dps_id_ck,
    DROP COLUMN http_status,
    DROP COLUMN ambiente,
    DROP COLUMN chave_acesso,
    DROP COLUMN dps_id,
    DROP COLUMN request_hash;
DELETE FROM identity.domain_value WHERE domain_code = 'fiscal_emission_environment';
