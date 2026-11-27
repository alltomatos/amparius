-- Amparius: cadastro operacional de pessoa jurídica, contatos, endereços e
-- representante (issue #78 / E2E-01B7).
-- Forward-only: correções futuras devem ser novas migrations.

-- +goose Up
INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('party_representative_role', 'legal_representative', 'Representante legal'),
    ('party_representative_role', 'administrator', 'Administrador'),
    ('party_representative_role', 'attorney', 'Procurador'),
    ('party_representative_role', 'contact', 'Contato')
ON CONFLICT (domain_code, code) DO NOTHING;

CREATE TABLE party.company_representative (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    company_party_id uuid NOT NULL,
    person_party_id uuid NOT NULL,
    role text NOT NULL,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT company_representative_tenant_company_fk FOREIGN KEY (tenant_id, company_party_id)
        REFERENCES party.party_company (tenant_id, party_id),
    CONSTRAINT company_representative_tenant_person_fk FOREIGN KEY (tenant_id, person_party_id)
        REFERENCES party.person (tenant_id, party_id),
    CONSTRAINT company_representative_tenant_id_uq UNIQUE (tenant_id, id),
    CONSTRAINT company_representative_uq UNIQUE (tenant_id, company_party_id, person_party_id, role)
);

CREATE INDEX company_representative_tenant_company_idx
    ON party.company_representative (tenant_id, company_party_id, id);
CREATE INDEX company_representative_tenant_person_idx
    ON party.company_representative (tenant_id, person_party_id, id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION party.validate_company_representative_domains()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM party.assert_domain_value('party_representative_role', NEW.role, 'role');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER company_representative_validate_domains
    BEFORE INSERT OR UPDATE OF role ON party.company_representative
    FOR EACH ROW EXECUTE FUNCTION party.validate_company_representative_domains();

ALTER TABLE party.company_representative ENABLE ROW LEVEL SECURITY;
CREATE POLICY company_representative_tenant_isolation ON party.company_representative
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Os principais históricos são preservados no upgrade. O repositório
-- serializa a troca por escopo e rebaixa os anteriores na mesma transação.
CREATE INDEX address_tenant_party_primary_idx
    ON party.address (tenant_id, party_id) WHERE "primary";
CREATE INDEX contact_tenant_party_kind_primary_idx
    ON party.contact (tenant_id, party_id, kind) WHERE "primary";

-- +goose StatementBegin
CREATE FUNCTION party.validate_new_address_format()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state !~ '^[A-Z]{2}$' OR NEW.zip !~ '^[0-9]{8}$' THEN
        RAISE EXCEPTION 'Formato de UF ou CEP inválido' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER address_validate_new_format
    BEFORE INSERT OR UPDATE OF state, zip ON party.address
    FOR EACH ROW EXECUTE FUNCTION party.validate_new_address_format();

-- party.party_company, party.address e party.contact existem desde 0002 sem
-- nenhum grant para amparius_app; esta migration fecha essa lacuna para a
-- fatia de PJ/contatos/endereços/representante. USAGE no schema party e
-- SELECT em identity.domain_value já vêm de 0010 e 0013, portanto não são
-- repetidos aqui (evita revogar privilégio de outra migration no Down).
GRANT SELECT, INSERT ON party.party_company TO amparius_app;
GRANT SELECT, INSERT, UPDATE ON party.address, party.contact TO amparius_app;
GRANT SELECT, INSERT ON party.company_representative TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT ON party.company_representative FROM amparius_app;
REVOKE SELECT, INSERT, UPDATE ON party.address, party.contact FROM amparius_app;
REVOKE SELECT, INSERT ON party.party_company FROM amparius_app;

DROP TRIGGER address_validate_new_format ON party.address;
DROP FUNCTION party.validate_new_address_format();
DROP INDEX party.contact_tenant_party_kind_primary_idx;
DROP INDEX party.address_tenant_party_primary_idx;

DROP TABLE party.company_representative;
DROP FUNCTION party.validate_company_representative_domains();

DELETE FROM identity.domain_value WHERE domain_code = 'party_representative_role';
