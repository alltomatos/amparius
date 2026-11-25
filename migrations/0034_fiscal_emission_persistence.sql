-- Amparius: persistencia do XML bruto e fechamento da idempotencia de emissao.
-- Forward-only: nenhuma migration fiscal anterior e editada.

-- +goose Up
ALTER TABLE fiscal.fiscal_document
    ADD COLUMN xml_raw bytea;

ALTER TABLE fiscal.emission_attempt
    ADD COLUMN request_xml bytea,
    ADD COLUMN response_raw bytea;

ALTER TABLE fiscal.fiscal_document
    ADD CONSTRAINT fiscal_document_xml_raw_hash_ck
    CHECK (xml_raw IS NULL OR xml_hash IS NOT NULL);

COMMENT ON COLUMN fiscal.fiscal_document.xml_raw IS
    'XML DPS/NFS-e bruto enviado, preservado para auditoria; nunca e sobrescrito.';
COMMENT ON COLUMN fiscal.emission_attempt.request_xml IS
    'XML bruto enviado nesta tentativa; a linha inteira e append-only.';
COMMENT ON COLUMN fiscal.emission_attempt.response_raw IS
    'Resposta bruta do transporte, preservada byte a byte; a linha e append-only.';

-- O documento permite apenas progresso do ciclo de emissao. Identidade,
-- valores e vinculos fiscais permanecem imutaveis; XML e hash so podem ser
-- preenchidos uma vez e nunca substituidos.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fiscal.validate_document_persistence() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
        OR NEW.tenant_id <> OLD.tenant_id
        OR NEW.company_id <> OLD.company_id
        OR NEW.establishment_id <> OLD.establishment_id
        OR NEW.competence <> OLD.competence
        OR NEW.kind <> OLD.kind
        OR NEW.series IS DISTINCT FROM OLD.series
        OR NEW.number IS DISTINCT FROM OLD.number
        OR NEW.party_id IS DISTINCT FROM OLD.party_id
        OR NEW.total <> OLD.total
        OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'identidade e valores do fiscal.fiscal_document sao imutaveis';
    END IF;
    IF OLD.xml_raw IS NOT NULL AND NEW.xml_raw IS DISTINCT FROM OLD.xml_raw THEN
        RAISE EXCEPTION 'xml_raw do fiscal.fiscal_document e imutavel';
    END IF;
    IF OLD.xml_hash IS NOT NULL AND NEW.xml_hash IS DISTINCT FROM OLD.xml_hash THEN
        RAISE EXCEPTION 'xml_hash do fiscal.fiscal_document e imutavel';
    END IF;
    IF OLD.xml_object_ref IS NOT NULL AND NEW.xml_object_ref IS DISTINCT FROM OLD.xml_object_ref THEN
        RAISE EXCEPTION 'xml_object_ref do fiscal.fiscal_document e imutavel';
    END IF;
    IF NEW.status = 'authorized' AND (NEW.xml_raw IS NULL OR NEW.xml_hash IS NULL) THEN
        RAISE EXCEPTION 'documento autorizado exige XML bruto e hash';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER fiscal_document_validate_persistence
    BEFORE UPDATE ON fiscal.fiscal_document
    FOR EACH ROW EXECUTE FUNCTION fiscal.validate_document_persistence();

-- O app cria e consulta registros, mas nao apaga historico fiscal nem altera
-- snapshots/tentativas. Status e metadados de emissao sao a unica mutacao
-- permitida no documento, protegida pelo trigger acima.
REVOKE DELETE ON fiscal.fiscal_document, fiscal.fiscal_item,
    fiscal.tax_calc, fiscal.emission_attempt FROM amparius_app;
REVOKE UPDATE ON fiscal.fiscal_item, fiscal.tax_calc,
    fiscal.emission_attempt FROM amparius_app;
REVOKE UPDATE ON fiscal.fiscal_document FROM amparius_app;
GRANT UPDATE (status, contingency_mode, issued_at, xml_object_ref, xml_hash,
    xml_raw, updated_at) ON fiscal.fiscal_document TO amparius_app;

-- +goose Down
REVOKE UPDATE (status, contingency_mode, issued_at, xml_object_ref, xml_hash,
    xml_raw, updated_at) ON fiscal.fiscal_document FROM amparius_app;
GRANT UPDATE ON fiscal.fiscal_document TO amparius_app;
GRANT UPDATE ON fiscal.fiscal_item, fiscal.tax_calc, fiscal.emission_attempt TO amparius_app;
GRANT DELETE ON fiscal.fiscal_document, fiscal.fiscal_item,
    fiscal.tax_calc, fiscal.emission_attempt TO amparius_app;
DROP TRIGGER fiscal_document_validate_persistence ON fiscal.fiscal_document;
DROP FUNCTION fiscal.validate_document_persistence();
ALTER TABLE fiscal.fiscal_document DROP CONSTRAINT fiscal_document_xml_raw_hash_ck;
ALTER TABLE fiscal.emission_attempt DROP COLUMN request_xml, DROP COLUMN response_raw;
ALTER TABLE fiscal.fiscal_document DROP COLUMN xml_raw;
