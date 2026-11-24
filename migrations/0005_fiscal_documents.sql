-- Amparius: documentos fiscais, itens, snapshots de calculo e tentativas de emissao.
-- Forward-only: correcoes futuras devem ser novas migracoes.

-- +goose Up
INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('fiscal_document_kind', 'nfe', 'NF-e'),
    ('fiscal_document_kind', 'nfse', 'NFS-e'),
    ('fiscal_document_status', 'draft', 'Rascunho'),
    ('fiscal_document_status', 'issued', 'Emitido'),
    ('fiscal_document_status', 'authorized', 'Autorizado'),
    ('fiscal_document_status', 'rejected', 'Rejeitado'),
    ('fiscal_document_status', 'cancelled', 'Cancelado'),
    ('fiscal_contingency_mode', 'none', 'Sem contingencia'),
    ('fiscal_contingency_mode', 'offline', 'Contingencia offline'),
    ('fiscal_attempt_mode', 'normal', 'Normal'),
    ('fiscal_attempt_mode', 'contingency', 'Contingencia'),
    ('fiscal_attempt_result', 'pending', 'Pendente'),
    ('fiscal_attempt_result', 'authorized', 'Autorizado'),
    ('fiscal_attempt_result', 'rejected', 'Rejeitado'),
    ('fiscal_attempt_result', 'error', 'Erro')
ON CONFLICT (domain_code, code) DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fiscal.validate_document_domains() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'fiscal_document' THEN
        PERFORM fiscal.assert_domain_value('fiscal_document_kind', NEW.kind, 'kind');
        PERFORM fiscal.assert_domain_value('fiscal_document_status', NEW.status, 'status');
        PERFORM fiscal.assert_domain_value('fiscal_contingency_mode', NEW.contingency_mode, 'contingency_mode');
    ELSIF TG_TABLE_NAME = 'emission_attempt' THEN
        PERFORM fiscal.assert_domain_value('fiscal_attempt_mode', NEW.mode, 'mode');
        PERFORM fiscal.assert_domain_value('fiscal_attempt_result', NEW.result, 'result');
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- identity.establishment nao tinha chave alternativa composta no slice inicial.
-- A chave e criada nesta migration para preservar o isolamento tenant/company nas FKs.
CREATE UNIQUE INDEX establishment_tenant_id_uq ON identity.establishment (tenant_id, id);

CREATE TABLE fiscal.fiscal_document (
    id uuid NOT NULL DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    company_id uuid NOT NULL,
    establishment_id uuid NOT NULL,
    competence date NOT NULL,
    kind text NOT NULL,
    series text,
    number bigint,
    status text NOT NULL DEFAULT 'draft',
    contingency_mode text NOT NULL DEFAULT 'none',
    party_id uuid,
    total numeric(18,2) NOT NULL DEFAULT 0 CHECK (total >= 0),
    issued_at timestamptz,
    xml_object_ref text,
    xml_hash char(64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, competence),
    CONSTRAINT fiscal_document_tenant_id_uq UNIQUE (tenant_id, id, competence),
    CONSTRAINT fiscal_document_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT fiscal_document_establishment_fk FOREIGN KEY (tenant_id, establishment_id)
        REFERENCES identity.establishment (tenant_id, id),
    CONSTRAINT fiscal_document_number_ck CHECK (number IS NULL OR number > 0),
    CONSTRAINT fiscal_document_xml_hash_ck CHECK (xml_hash IS NULL OR (xml_hash ~ '^[0-9a-f]{64}$' AND xml_hash = lower(xml_hash))),
    CONSTRAINT fiscal_document_xml_ref_ck CHECK (xml_object_ref IS NULL OR length(btrim(xml_object_ref)) > 0)
) PARTITION BY RANGE (competence);

CREATE TABLE fiscal.fiscal_document_2026_11 PARTITION OF fiscal.fiscal_document
    FOR VALUES FROM ('2026-11-01') TO ('2026-12-01');
CREATE TABLE fiscal.fiscal_document_2026_12 PARTITION OF fiscal.fiscal_document
    FOR VALUES FROM ('2026-12-01') TO ('2027-01-01');
CREATE TABLE fiscal.fiscal_document_default PARTITION OF fiscal.fiscal_document DEFAULT;

CREATE TABLE fiscal.fiscal_item (
    id uuid NOT NULL DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    document_id uuid NOT NULL,
    competence date NOT NULL,
    item_id uuid,
    description text NOT NULL CHECK (length(btrim(description)) > 0),
    quantity numeric(18,6) NOT NULL CHECK (quantity > 0),
    unit_price numeric(18,2) NOT NULL CHECK (unit_price >= 0),
    total numeric(18,2) NOT NULL CHECK (total >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, competence),
    CONSTRAINT fiscal_item_tenant_id_uq UNIQUE (tenant_id, id, competence),
    CONSTRAINT fiscal_item_document_fk FOREIGN KEY (tenant_id, document_id, competence)
        REFERENCES fiscal.fiscal_document (tenant_id, id, competence),
    CONSTRAINT fiscal_item_total_ck CHECK (total = round(quantity * unit_price, 2))
) PARTITION BY RANGE (competence);

CREATE TABLE fiscal.fiscal_item_2026_11 PARTITION OF fiscal.fiscal_item
    FOR VALUES FROM ('2026-11-01') TO ('2026-12-01');
CREATE TABLE fiscal.fiscal_item_2026_12 PARTITION OF fiscal.fiscal_item
    FOR VALUES FROM ('2026-12-01') TO ('2027-01-01');
CREATE TABLE fiscal.fiscal_item_default PARTITION OF fiscal.fiscal_item DEFAULT;

CREATE TABLE fiscal.tax_calc (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    item_id uuid NOT NULL,
    competence date NOT NULL,
    tax_type text NOT NULL,
    base numeric(18,2) NOT NULL CHECK (base >= 0),
    rate numeric(18,6) CHECK (rate >= 0),
    amount numeric(18,2) NOT NULL CHECK (amount >= 0),
    rule_id uuid,
    rule_source_hash char(64),
    calculation_input jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tax_calc_item_fk FOREIGN KEY (tenant_id, item_id, competence)
        REFERENCES fiscal.fiscal_item (tenant_id, id, competence),
    CONSTRAINT tax_calc_source_hash_ck CHECK (rule_source_hash IS NULL OR (rule_source_hash ~ '^[0-9a-f]{64}$' AND rule_source_hash = lower(rule_source_hash)))
);

CREATE TABLE fiscal.emission_attempt (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL REFERENCES identity.tenant(id),
    document_id uuid NOT NULL,
    competence date NOT NULL,
    attempt integer NOT NULL CHECK (attempt > 0),
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) > 0),
    mode text NOT NULL,
    result text NOT NULL DEFAULT 'pending',
    protocol text,
    response_body text,
    response_hash char(64),
    response_content_type text,
    attempted_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT emission_attempt_document_fk FOREIGN KEY (tenant_id, document_id, competence)
        REFERENCES fiscal.fiscal_document (tenant_id, id, competence),
    CONSTRAINT emission_attempt_tenant_idempotency_uq UNIQUE (tenant_id, idempotency_key),
    CONSTRAINT emission_attempt_document_number_uq UNIQUE (tenant_id, document_id, competence, attempt),
    CONSTRAINT emission_attempt_response_hash_ck CHECK (response_hash IS NULL OR (response_hash ~ '^[0-9a-f]{64}$' AND response_hash = lower(response_hash)))
);

CREATE INDEX fiscal_document_tenant_competence_idx ON fiscal.fiscal_document (tenant_id, competence, id);
CREATE INDEX fiscal_item_tenant_document_idx ON fiscal.fiscal_item (tenant_id, document_id, competence, id);
CREATE INDEX tax_calc_tenant_item_idx ON fiscal.tax_calc (tenant_id, item_id, competence, tax_type);
CREATE INDEX emission_attempt_tenant_document_idx ON fiscal.emission_attempt (tenant_id, document_id, competence, attempted_at);

CREATE TRIGGER fiscal_document_validate_domains
    BEFORE INSERT OR UPDATE OF kind, status, contingency_mode ON fiscal.fiscal_document
    FOR EACH ROW EXECUTE FUNCTION fiscal.validate_document_domains();
CREATE TRIGGER emission_attempt_validate_domains
    BEFORE INSERT OR UPDATE OF mode, result ON fiscal.emission_attempt
    FOR EACH ROW EXECUTE FUNCTION fiscal.validate_document_domains();

ALTER TABLE fiscal.fiscal_document ENABLE ROW LEVEL SECURITY;
ALTER TABLE fiscal.fiscal_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE fiscal.tax_calc ENABLE ROW LEVEL SECURITY;
ALTER TABLE fiscal.emission_attempt ENABLE ROW LEVEL SECURITY;

CREATE POLICY fiscal_document_tenant_isolation ON fiscal.fiscal_document
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY fiscal_item_tenant_isolation ON fiscal.fiscal_item
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY tax_calc_tenant_isolation ON fiscal.tax_calc
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY emission_attempt_tenant_isolation ON fiscal.emission_attempt
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Documentos, itens, calculos e tentativas preservam o historico fiscal.
-- Atualizacoes de status do documento continuam permitidas para o ciclo de emissao.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fiscal.reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'tabela fiscal.% e append-only: % proibido', TG_TABLE_NAME, TG_OP;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER fiscal_document_history_preserve BEFORE DELETE ON fiscal.fiscal_document
    FOR EACH ROW EXECUTE FUNCTION fiscal.reject_mutation();
CREATE TRIGGER fiscal_item_append_only BEFORE UPDATE OR DELETE ON fiscal.fiscal_item
    FOR EACH ROW EXECUTE FUNCTION fiscal.reject_mutation();
CREATE TRIGGER tax_calc_append_only BEFORE UPDATE OR DELETE ON fiscal.tax_calc
    FOR EACH ROW EXECUTE FUNCTION fiscal.reject_mutation();
CREATE TRIGGER emission_attempt_append_only BEFORE UPDATE OR DELETE ON fiscal.emission_attempt
    FOR EACH ROW EXECUTE FUNCTION fiscal.reject_mutation();

-- +goose Down
DROP TABLE fiscal.emission_attempt;
DROP TABLE fiscal.tax_calc;
DROP TABLE fiscal.fiscal_item;
DROP TABLE fiscal.fiscal_document;
DROP FUNCTION fiscal.validate_document_domains();
DROP FUNCTION fiscal.reject_mutation();
DROP INDEX identity.establishment_tenant_id_uq;
DELETE FROM identity.domain_value WHERE domain_code LIKE 'fiscal_document_%'
    OR domain_code LIKE 'fiscal_contingency_%'
    OR domain_code LIKE 'fiscal_attempt_%';
