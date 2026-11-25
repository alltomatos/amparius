-- Amparius: documento autorizado exige o XML bruto e seu hash.
-- Forward-only: reforca a 0034 sem editar migration aplicada.

-- +goose Up
ALTER TABLE fiscal.fiscal_document
    ADD CONSTRAINT fiscal_document_authorized_xml_ck
    CHECK (status <> 'authorized' OR (xml_raw IS NOT NULL AND xml_hash IS NOT NULL));

-- +goose Down
ALTER TABLE fiscal.fiscal_document
    DROP CONSTRAINT fiscal_document_authorized_xml_ck;
