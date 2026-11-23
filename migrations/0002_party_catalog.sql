-- Amparius: party e catalog.
-- Forward-only: correcao futura deve ser uma nova migracao.

-- +goose Up
CREATE SCHEMA IF NOT EXISTS party;
CREATE SCHEMA IF NOT EXISTS catalog;

-- Discriminadores sao dados de dominio, nao enums PostgreSQL.
INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('party_kind', 'person', 'Pessoa'),
    ('party_kind', 'company', 'Empresa'),
    ('party_status', 'active', 'Ativo'),
    ('party_status', 'inactive', 'Inativo'),
    ('party_gender', 'female', 'Feminino'),
    ('party_gender', 'male', 'Masculino'),
    ('party_gender', 'non_binary', 'Nao binario'),
    ('party_gender', 'unspecified', 'Nao informado'),
    ('party_contact_kind', 'phone', 'Telefone'),
    ('party_contact_kind', 'email', 'E-mail'),
    ('party_contact_kind', 'whatsapp', 'WhatsApp'),
    ('party_contact_kind', 'other', 'Outro'),
    ('party_family_relation', 'spouse', 'Conjuge'),
    ('party_family_relation', 'child', 'Filho'),
    ('party_family_relation', 'parent', 'Pai ou mae'),
    ('party_family_relation', 'sibling', 'Irmao ou irma'),
    ('party_family_relation', 'other', 'Outro'),
    ('catalog_item_kind', 'product', 'Produto'),
    ('catalog_item_kind', 'service', 'Servico'),
    ('catalog_unit', 'unit', 'Unidade'),
    ('catalog_unit', 'hour', 'Hora'),
    ('catalog_unit', 'month', 'Mes'),
    ('catalog_unit', 'service', 'Servico')
ON CONFLICT (domain_code, code) DO NOTHING;

-- A tabela de dominio e composta por (domain_code, code); PostgreSQL nao
-- permite subconsulta em CHECK, portanto a validacao e feita por trigger.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION party.assert_domain_value(
    p_domain_code text,
    p_code text,
    p_field_name text
)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    IF p_code IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM identity.domain_value
        WHERE domain_code = p_domain_code AND code = p_code AND active
    ) THEN
        RAISE EXCEPTION '% invalido para o dominio %: %', p_field_name, p_domain_code, p_code;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION party.validate_party_domains()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM party.assert_domain_value('party_kind', NEW.kind, 'kind');
    PERFORM party.assert_domain_value('party_status', NEW.status, 'status');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TABLE party.party (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    kind text NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    document text,
    status text NOT NULL,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT party_tenant_id_uq UNIQUE (tenant_id, id)
);
CREATE TRIGGER party_validate_domains
    BEFORE INSERT OR UPDATE OF kind, status ON party.party
    FOR EACH ROW EXECUTE FUNCTION party.validate_party_domains();

CREATE INDEX party_tenant_idx ON party.party (tenant_id, id);

CREATE TABLE party.person (
    party_id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    birth_date date,
    gender text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT person_tenant_party_fk FOREIGN KEY (tenant_id, party_id)
        REFERENCES party.party (tenant_id, id),
    CONSTRAINT person_tenant_party_uq UNIQUE (tenant_id, party_id)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION party.validate_person_domains()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM party.assert_domain_value('party_gender', NEW.gender, 'gender');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER person_validate_domains
    BEFORE INSERT OR UPDATE OF gender ON party.person
    FOR EACH ROW EXECUTE FUNCTION party.validate_person_domains();

CREATE TABLE party.party_company (
    party_id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    trade_name text,
    state_registration text,
    municipal_registration text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT party_company_tenant_party_fk FOREIGN KEY (tenant_id, party_id)
        REFERENCES party.party (tenant_id, id),
    CONSTRAINT party_company_tenant_party_uq UNIQUE (tenant_id, party_id)
);

CREATE TABLE party.address (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    party_id uuid NOT NULL,
    street text NOT NULL,
    number text NOT NULL,
    city text NOT NULL,
    state text NOT NULL,
    zip text NOT NULL,
    "primary" boolean NOT NULL DEFAULT false,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT address_tenant_party_fk FOREIGN KEY (tenant_id, party_id)
        REFERENCES party.party (tenant_id, id),
    CONSTRAINT address_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE TABLE party.contact (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    party_id uuid NOT NULL,
    kind text NOT NULL,
    value text NOT NULL,
    "primary" boolean NOT NULL DEFAULT false,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contact_tenant_party_fk FOREIGN KEY (tenant_id, party_id)
        REFERENCES party.party (tenant_id, id),
    CONSTRAINT contact_tenant_id_uq UNIQUE (tenant_id, id)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION party.validate_contact_domains()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM party.assert_domain_value('party_contact_kind', NEW.kind, 'kind');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER contact_validate_domains
    BEFORE INSERT OR UPDATE OF kind ON party.contact
    FOR EACH ROW EXECUTE FUNCTION party.validate_contact_domains();

CREATE TABLE party.deceased (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    person_id uuid NOT NULL,
    death_at timestamptz NOT NULL,
    death_certificate text,
    burial_site text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deceased_tenant_person_fk FOREIGN KEY (tenant_id, person_id)
        REFERENCES party.person (tenant_id, party_id),
    CONSTRAINT deceased_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE TABLE party.family_link (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    deceased_id uuid NOT NULL,
    person_id uuid NOT NULL,
    relation text NOT NULL,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT family_link_tenant_deceased_fk FOREIGN KEY (tenant_id, deceased_id)
        REFERENCES party.deceased (tenant_id, id),
    CONSTRAINT family_link_tenant_person_fk FOREIGN KEY (tenant_id, person_id)
        REFERENCES party.person (tenant_id, party_id),
    CONSTRAINT family_link_tenant_id_uq UNIQUE (tenant_id, id)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION party.validate_family_link_domains()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM party.assert_domain_value('party_family_relation', NEW.relation, 'relation');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER family_link_validate_domains
    BEFORE INSERT OR UPDATE OF relation ON party.family_link
    FOR EACH ROW EXECUTE FUNCTION party.validate_family_link_domains();

CREATE TABLE catalog.catalog_item (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    kind text NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    unit text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT catalog_item_tenant_id_uq UNIQUE (tenant_id, id)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION party.validate_catalog_item_domains()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM party.assert_domain_value('catalog_item_kind', NEW.kind, 'kind');
    PERFORM party.assert_domain_value('catalog_unit', NEW.unit, 'unit');
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER catalog_item_validate_domains
    BEFORE INSERT OR UPDATE OF kind, unit ON catalog.catalog_item
    FOR EACH ROW EXECUTE FUNCTION party.validate_catalog_item_domains();

CREATE TABLE catalog.price_list (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    valid_from date NOT NULL,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT price_list_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE TABLE catalog.price (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    item_id uuid NOT NULL,
    list_id uuid NOT NULL,
    amount numeric(18,2) NOT NULL,
    valid_from date NOT NULL,
    valid_to date,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT price_validity_ck CHECK (valid_to IS NULL OR valid_to >= valid_from),
    CONSTRAINT price_tenant_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES catalog.catalog_item (tenant_id, id),
    CONSTRAINT price_tenant_list_fk FOREIGN KEY (tenant_id, list_id)
        REFERENCES catalog.price_list (tenant_id, id),
    CONSTRAINT price_tenant_id_uq UNIQUE (tenant_id, id)
);

CREATE TABLE catalog.tax_classification (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    ncm text,
    cest text,
    cst text,
    class_trib text,
    service_code text,
    valid_from date NOT NULL
);

CREATE INDEX person_tenant_idx ON party.person (tenant_id, party_id);
CREATE INDEX party_company_tenant_idx ON party.party_company (tenant_id, party_id);
CREATE INDEX address_tenant_party_idx ON party.address (tenant_id, party_id, id);
CREATE INDEX contact_tenant_party_idx ON party.contact (tenant_id, party_id, id);
CREATE INDEX deceased_tenant_person_idx ON party.deceased (tenant_id, person_id, id);
CREATE INDEX family_link_tenant_deceased_idx ON party.family_link (tenant_id, deceased_id, id);
CREATE INDEX family_link_tenant_person_idx ON party.family_link (tenant_id, person_id, id);
CREATE INDEX catalog_item_tenant_idx ON catalog.catalog_item (tenant_id, id);
CREATE INDEX price_list_tenant_idx ON catalog.price_list (tenant_id, id);
CREATE INDEX price_tenant_item_idx ON catalog.price (tenant_id, item_id, valid_from);
CREATE INDEX price_tenant_list_idx ON catalog.price (tenant_id, list_id, valid_from);

ALTER TABLE party.party ENABLE ROW LEVEL SECURITY;
ALTER TABLE party.person ENABLE ROW LEVEL SECURITY;
ALTER TABLE party.party_company ENABLE ROW LEVEL SECURITY;
ALTER TABLE party.address ENABLE ROW LEVEL SECURITY;
ALTER TABLE party.contact ENABLE ROW LEVEL SECURITY;
ALTER TABLE party.deceased ENABLE ROW LEVEL SECURITY;
ALTER TABLE party.family_link ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog.catalog_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog.price_list ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog.price ENABLE ROW LEVEL SECURITY;

CREATE POLICY party_tenant_isolation ON party.party
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY person_tenant_isolation ON party.person
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY party_company_tenant_isolation ON party.party_company
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY address_tenant_isolation ON party.address
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY contact_tenant_isolation ON party.contact
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY deceased_tenant_isolation ON party.deceased
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY family_link_tenant_isolation ON party.family_link
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY catalog_item_tenant_isolation ON catalog.catalog_item
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY price_list_tenant_isolation ON catalog.price_list
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY price_tenant_isolation ON catalog.price
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- +goose Down
DROP SCHEMA catalog CASCADE;
DROP SCHEMA party CASCADE;
DELETE FROM identity.domain_value
WHERE domain_code IN (
    'party_kind', 'party_status', 'party_gender', 'party_contact_kind',
    'party_family_relation', 'catalog_item_kind', 'catalog_unit'
);
