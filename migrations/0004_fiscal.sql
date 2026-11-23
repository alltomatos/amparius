-- Amparius: espinha dorsal fiscal RTC-first.
-- Forward-only: correcoes futuras devem ser novas migracoes.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE SCHEMA IF NOT EXISTS fiscal;

INSERT INTO identity.domain_value (domain_code, code, label) VALUES
    ('fiscal_regime', 'geral', 'Geral'),
    ('fiscal_regime', 'simples_nacional', 'Simples Nacional'),
    ('fiscal_environment', 'homologacao', 'Homologacao'),
    ('fiscal_environment', 'producao', 'Producao'),
    ('fiscal_config_status', 'active', 'Ativa'),
    ('fiscal_config_status', 'suspended', 'Suspensa'),
    ('fiscal_tax_type', 'cbs', 'CBS'), ('fiscal_tax_type', 'ibs_uf', 'IBS estadual'),
    ('fiscal_tax_type', 'ibs_mun', 'IBS municipal'), ('fiscal_tax_type', 'is', 'Imposto Seletivo'),
    ('fiscal_tax_type', 'icms', 'ICMS'), ('fiscal_tax_type', 'fcp', 'FCP'),
    ('fiscal_tax_type', 'iss', 'ISS'), ('fiscal_tax_type', 'pis', 'PIS'),
    ('fiscal_tax_type', 'cofins', 'COFINS'), ('fiscal_tax_type', 'ipi', 'IPI'),
    ('fiscal_scope_kind', 'nfe', 'NF-e'), ('fiscal_scope_kind', 'nfse', 'NFS-e'),
    ('fiscal_operation_kind', 'interna', 'Interna'),
    ('fiscal_operation_kind', 'interestadual', 'Interestadual'),
    ('fiscal_operation_kind', 'exportacao', 'Exportacao'),
    ('fiscal_operation_kind', 'consumo_final', 'Consumo final'),
    ('fiscal_basis_kind', 'percentual', 'Percentual'),
    ('fiscal_basis_kind', 'faixa_simples', 'Faixa do Simples'),
    ('fiscal_basis_kind', 'por_unidade', 'Por unidade'),
    ('fiscal_basis_kind', 'isencao', 'Isencao'),
    ('fiscal_rate_unit', 'percent', 'Percentual'),
    ('fiscal_rate_unit', 'brl_por_unidade', 'BRL por unidade'),
    ('fiscal_simples_annex', 'I', 'Anexo I'), ('fiscal_simples_annex', 'II', 'Anexo II'),
    ('fiscal_simples_annex', 'III', 'Anexo III'), ('fiscal_simples_annex', 'IV', 'Anexo IV'),
    ('fiscal_simples_annex', 'V', 'Anexo V'),
    ('fiscal_source_kind', 'rfb', 'Receita Federal'), ('fiscal_source_kind', 'confaz', 'CONFAZ'),
    ('fiscal_source_kind', 'municipio', 'Municipio'), ('fiscal_source_kind', 'estado', 'Estado'),
    ('fiscal_source_kind', 'lc214', 'Lei Complementar 214'),
    ('fiscal_source_kind', 'lc123', 'Lei Complementar 123'),
    ('fiscal_source_kind', 'lc116', 'Lei Complementar 116'),
    ('fiscal_certificate_kind', 'a1', 'A1'), ('fiscal_certificate_kind', 'a3', 'A3'),
    ('fiscal_certificate_status', 'active', 'Ativo'),
    ('fiscal_certificate_status', 'expired', 'Expirado'),
    ('fiscal_certificate_status', 'revoked', 'Revogado'),
    ('fiscal_secret_store', 'vault', 'Vault'), ('fiscal_secret_store', 'kms', 'KMS')
ON CONFLICT (domain_code, code) DO NOTHING;

-- A tabela de dominio e validada por trigger, pois CHECK nao aceita subconsulta.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fiscal.assert_domain_value(p_domain_code text, p_code text, p_field_name text)
RETURNS void LANGUAGE plpgsql AS $$
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
CREATE OR REPLACE FUNCTION fiscal.validate_domains() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'fiscal_config' THEN
        PERFORM fiscal.assert_domain_value('fiscal_regime', NEW.regime, 'regime');
        PERFORM fiscal.assert_domain_value('fiscal_environment', NEW.environment, 'environment');
        PERFORM fiscal.assert_domain_value('fiscal_config_status', NEW.status, 'status');
    ELSIF TG_TABLE_NAME = 'certificate' THEN
        PERFORM fiscal.assert_domain_value('fiscal_certificate_kind', NEW.kind, 'kind');
        PERFORM fiscal.assert_domain_value('fiscal_secret_store', NEW.storage_kind, 'storage_kind');
        PERFORM fiscal.assert_domain_value('fiscal_certificate_status', NEW.status, 'status');
    ELSIF TG_TABLE_NAME = 'tax_rule' THEN
        PERFORM fiscal.assert_domain_value('fiscal_tax_type', NEW.tax_type, 'tax_type');
        PERFORM fiscal.assert_domain_value('fiscal_regime', NEW.regime, 'regime');
        PERFORM fiscal.assert_domain_value('fiscal_scope_kind', NEW.scope_kind, 'scope_kind');
        PERFORM fiscal.assert_domain_value('fiscal_operation_kind', NEW.operation_kind, 'operation_kind');
        PERFORM fiscal.assert_domain_value('fiscal_basis_kind', NEW.basis_kind, 'basis_kind');
        PERFORM fiscal.assert_domain_value('fiscal_rate_unit', NEW.rate_unit, 'rate_unit');
        PERFORM fiscal.assert_domain_value('fiscal_simples_annex', NEW.simples_annex, 'simples_annex');
        PERFORM fiscal.assert_domain_value('fiscal_source_kind', NEW.source_kind, 'source_kind');
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TABLE fiscal.fiscal_config (
    company_id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    regime text NOT NULL,
    environment text NOT NULL DEFAULT 'homologacao',
    uf char(2),
    municipality_code char(7),
    series_nfse text,
    series_nfe text,
    status text NOT NULL DEFAULT 'active',
    config_version integer NOT NULL DEFAULT 1 CHECK (config_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fiscal_config_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT fiscal_config_uf_ck CHECK (uf IS NULL OR uf ~ '^[A-Z]{2}$'),
    CONSTRAINT fiscal_config_municipality_ck CHECK (municipality_code IS NULL OR municipality_code ~ '^[0-9]{7}$')
);

CREATE TABLE fiscal.certificate (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid NOT NULL,
    company_id uuid NOT NULL,
    kind text NOT NULL,
    subject_cn text NOT NULL,
    issuer_cn text,
    serial_number text NOT NULL,
    fingerprint_sha256 char(64) NOT NULL,
    valid_from timestamptz NOT NULL,
    valid_until timestamptz NOT NULL,
    storage_kind text NOT NULL,
    storage_ref text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT certificate_company_fk FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id),
    CONSTRAINT certificate_validity_ck CHECK (valid_until > valid_from),
    CONSTRAINT certificate_fingerprint_ck CHECK (fingerprint_sha256 ~ '^[0-9a-f]{64}$' AND fingerprint_sha256 = lower(fingerprint_sha256)),
    CONSTRAINT certificate_storage_ref_ck CHECK (length(btrim(storage_ref)) > 0),
    CONSTRAINT certificate_tenant_fingerprint_uq UNIQUE (tenant_id, company_id, fingerprint_sha256)
);

CREATE UNIQUE INDEX certificate_one_active_company_uq
    ON fiscal.certificate (tenant_id, company_id) WHERE status = 'active';

CREATE TABLE fiscal.tax_rule (
    id uuid PRIMARY KEY DEFAULT public.uuidv7(),
    tenant_id uuid,
    tax_type text NOT NULL,
    regime text NOT NULL,
    scope_kind text NOT NULL,
    ncm text,
    cest text,
    cst text,
    class_trib text,
    cfop text,
    service_code text,
    municipality_code text,
    uf text,
    operation_kind text,
    simples_annex text,
    rbt12_from numeric(18,2),
    rbt12_to numeric(18,2),
    basis_kind text NOT NULL,
    rate numeric(18,6),
    rate_unit text,
    deduction_amount numeric(18,2),
    base_reduction_percent numeric(9,4),
    valid_from date NOT NULL,
    valid_to date,
    source_kind text NOT NULL,
    source_name text NOT NULL,
    source_url text NOT NULL,
    source_version text NOT NULL,
    source_published_at date NOT NULL,
    source_hash char(64) NOT NULL,
    import_batch uuid NOT NULL,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    notes text,
    CONSTRAINT tax_rule_ncm_ck CHECK (ncm IS NULL OR ncm ~ '^[0-9]{8}$'),
    CONSTRAINT tax_rule_cest_ck CHECK (cest IS NULL OR cest ~ '^[0-9]{7}$'),
    CONSTRAINT tax_rule_cfop_ck CHECK (cfop IS NULL OR cfop ~ '^[0-9]{4}$'),
    CONSTRAINT tax_rule_uf_ck CHECK (uf IS NULL OR uf ~ '^[A-Z]{2}$'),
    CONSTRAINT tax_rule_municipality_ck CHECK (municipality_code IS NULL OR municipality_code ~ '^[0-9]{7}$'),
    CONSTRAINT tax_rule_hash_ck CHECK (source_hash ~ '^[0-9a-f]{64}$' AND source_hash = lower(source_hash)),
    CONSTRAINT tax_rule_validity_ck CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT tax_rule_nonnegative_ck CHECK (
        (rbt12_from IS NULL OR rbt12_from >= 0) AND (rbt12_to IS NULL OR rbt12_to >= 0)
        AND (rate IS NULL OR rate >= 0) AND (deduction_amount IS NULL OR deduction_amount >= 0)
        AND (base_reduction_percent IS NULL OR base_reduction_percent BETWEEN 0 AND 100)
    ),
    CONSTRAINT tax_rule_rbt12_order_ck CHECK (rbt12_to IS NULL OR rbt12_from IS NULL OR rbt12_to >= rbt12_from),
    CONSTRAINT tax_rule_basis_ck CHECK (
        (basis_kind = 'percentual' AND rate IS NOT NULL AND rate_unit IS NOT NULL)
        OR (basis_kind = 'faixa_simples' AND simples_annex IS NOT NULL AND rbt12_from IS NOT NULL
            AND rbt12_to IS NOT NULL AND rate IS NOT NULL AND deduction_amount IS NOT NULL AND rate_unit = 'percent')
        OR (basis_kind = 'por_unidade' AND rate IS NOT NULL AND rate_unit = 'brl_por_unidade')
        OR (basis_kind = 'isencao' AND rate IS NULL AND rate_unit IS NULL)
    ),
    CONSTRAINT tax_rule_tax_type_ck CHECK (
        (tax_type = 'iss' AND service_code IS NOT NULL AND municipality_code IS NOT NULL AND scope_kind = 'nfse'
            AND ncm IS NULL AND cfop IS NULL AND class_trib IS NULL)
        OR (tax_type IN ('icms', 'fcp') AND ncm IS NOT NULL AND cfop IS NOT NULL AND uf IS NOT NULL
            AND operation_kind IS NOT NULL AND scope_kind = 'nfe' AND service_code IS NULL AND municipality_code IS NULL)
        OR (tax_type IN ('pis', 'cofins', 'ipi') AND cst IS NOT NULL AND scope_kind = 'nfe' AND class_trib IS NULL)
        OR (tax_type IN ('cbs', 'ibs_uf', 'ibs_mun') AND class_trib IS NOT NULL
            AND (ncm IS NOT NULL OR service_code IS NOT NULL) AND cfop IS NULL)
        OR (tax_type = 'is' AND class_trib IS NOT NULL AND ncm IS NOT NULL
            AND service_code IS NULL AND municipality_code IS NULL)
    )
);

CREATE INDEX fiscal_config_tenant_idx ON fiscal.fiscal_config (tenant_id, company_id);
CREATE INDEX certificate_tenant_company_idx ON fiscal.certificate (tenant_id, company_id, valid_until);
CREATE INDEX tax_rule_tenant_validity_idx ON fiscal.tax_rule (tenant_id, valid_from, valid_to);

CREATE TRIGGER fiscal_config_validate_domains BEFORE INSERT OR UPDATE OF regime, environment, status
    ON fiscal.fiscal_config FOR EACH ROW EXECUTE FUNCTION fiscal.validate_domains();
CREATE TRIGGER certificate_validate_domains BEFORE INSERT OR UPDATE OF kind, storage_kind, status
    ON fiscal.certificate FOR EACH ROW EXECUTE FUNCTION fiscal.validate_domains();
CREATE TRIGGER tax_rule_validate_domains BEFORE INSERT OR UPDATE OF tax_type, regime, scope_kind, operation_kind, basis_kind, rate_unit, simples_annex, source_kind
    ON fiscal.tax_rule FOR EACH ROW EXECUTE FUNCTION fiscal.validate_domains();

-- Regras oficiais globais sao imutaveis. Novas publicacoes entram como novas linhas.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fiscal.reject_global_tax_rule_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.tenant_id IS NULL THEN
        RAISE EXCEPTION 'regra fiscal oficial global e append-only: % proibido', TG_OP;
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER tax_rule_global_append_only BEFORE UPDATE OR DELETE ON fiscal.tax_rule
    FOR EACH ROW EXECUTE FUNCTION fiscal.reject_global_tax_rule_mutation();

ALTER TABLE fiscal.tax_rule
    ADD CONSTRAINT tax_rule_no_overlap EXCLUDE USING gist (
        COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid) WITH =,
        tax_type WITH =, regime WITH =, scope_kind WITH =,
        COALESCE(ncm, '') WITH =, COALESCE(cest, '') WITH =, COALESCE(cst, '') WITH =,
        COALESCE(class_trib, '') WITH =, COALESCE(cfop, '') WITH =,
        COALESCE(service_code, '') WITH =, COALESCE(municipality_code, '') WITH =,
        COALESCE(uf, '') WITH =, COALESCE(operation_kind, '') WITH =, COALESCE(simples_annex, '') WITH =,
        COALESCE(rbt12_from, -1::numeric) WITH =, COALESCE(rbt12_to, -1::numeric) WITH =,
        basis_kind WITH =, COALESCE(rate_unit, '') WITH =,
        daterange(valid_from, COALESCE(valid_to, 'infinity'::date), '[)') WITH &&
    );

ALTER TABLE fiscal.fiscal_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE fiscal.certificate ENABLE ROW LEVEL SECURITY;
ALTER TABLE fiscal.tax_rule ENABLE ROW LEVEL SECURITY;

CREATE POLICY fiscal_config_tenant_isolation ON fiscal.fiscal_config
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY certificate_tenant_isolation ON fiscal.certificate
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY tax_rule_tenant_isolation ON fiscal.tax_rule
    FOR SELECT USING (tenant_id IS NULL OR tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY tax_rule_tenant_write ON fiscal.tax_rule
    FOR INSERT WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY tax_rule_tenant_update ON fiscal.tax_rule
    FOR UPDATE USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
CREATE POLICY tax_rule_tenant_delete ON fiscal.tax_rule
    FOR DELETE USING (tenant_id = current_setting('app.tenant_id')::uuid);

-- +goose Down
DROP SCHEMA fiscal CASCADE;
DELETE FROM identity.domain_value WHERE domain_code LIKE 'fiscal_%';
