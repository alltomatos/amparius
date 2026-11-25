-- Amparius: escopo, vigencia e inscricoes da configuracao fiscal.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up

-- A configuracao antiga tinha company_id como chave primaria. O id proprio
-- permite manter historico de configuracoes por empresa/estabelecimento sem
-- sobrescrever uma vigencia anterior.
ALTER TABLE fiscal.fiscal_config
    ADD COLUMN id uuid DEFAULT public.uuidv7();

ALTER TABLE fiscal.fiscal_config
    ALTER COLUMN id SET NOT NULL;

ALTER TABLE fiscal.fiscal_config
    DROP CONSTRAINT fiscal_config_pkey,
    ADD CONSTRAINT fiscal_config_pkey PRIMARY KEY (id);

ALTER TABLE identity.establishment
    ADD CONSTRAINT establishment_tenant_company_id_uq
        UNIQUE (tenant_id, company_id, id);

ALTER TABLE fiscal.fiscal_config
    ADD COLUMN establishment_id uuid,
    ADD COLUMN state_registration text,
    ADD COLUMN municipal_registration text,
    ADD COLUMN valid_from date NOT NULL DEFAULT DATE '1970-01-01',
    ADD COLUMN valid_to date;

ALTER TABLE fiscal.fiscal_config
    ALTER COLUMN valid_from DROP DEFAULT;

ALTER TABLE fiscal.fiscal_config
    ADD CONSTRAINT fiscal_config_establishment_fk
        FOREIGN KEY (tenant_id, company_id, establishment_id)
        REFERENCES identity.establishment (tenant_id, company_id, id),
    ADD CONSTRAINT fiscal_config_validity_ck
        CHECK (valid_to IS NULL OR valid_to > valid_from),
    ADD CONSTRAINT fiscal_config_state_registration_ck
        CHECK (state_registration IS NULL OR length(btrim(state_registration)) > 0),
    ADD CONSTRAINT fiscal_config_municipal_registration_ck
        CHECK (municipal_registration IS NULL OR length(btrim(municipal_registration)) > 0);

-- NULL em establishment_id representa a configuracao-padrao da empresa. O
-- sentinel apenas torna esse escopo comparavel dentro da exclusao GiST.
ALTER TABLE fiscal.fiscal_config
    ADD CONSTRAINT fiscal_config_no_overlapping_validity
    EXCLUDE USING gist (
        tenant_id WITH =,
        company_id WITH =,
        COALESCE(establishment_id, '00000000-0000-0000-0000-000000000000'::uuid) WITH =,
        daterange(valid_from, COALESCE(valid_to, 'infinity'::date), '[)') WITH &&
    );

CREATE INDEX fiscal_config_tenant_company_establishment_idx
    ON fiscal.fiscal_config (tenant_id, company_id, establishment_id, valid_from, valid_to);

-- A aplicacao pode consultar e criar uma nova vigencia, e atualizar somente
-- o estado/metadados da configuracao. Remocao direta nao faz parte do contrato.
GRANT SELECT, INSERT, UPDATE ON fiscal.fiscal_config TO amparius_app;

COMMENT ON COLUMN fiscal.fiscal_config.establishment_id IS
    'Estabelecimento da configuracao; NULL representa o padrao da empresa.';
COMMENT ON COLUMN fiscal.fiscal_config.state_registration IS
    'Inscricao estadual informada pelo contribuinte; nao e regra de aliquota.';
COMMENT ON COLUMN fiscal.fiscal_config.municipal_registration IS
    'Inscricao municipal informada pelo contribuinte; nao e regra de aliquota.';
COMMENT ON COLUMN fiscal.fiscal_config.valid_from IS
    'Inicio inclusivo da vigencia da configuracao.';
COMMENT ON COLUMN fiscal.fiscal_config.valid_to IS
    'Fim exclusivo da vigencia da configuracao; NULL significa sem fim definido.';

COMMENT ON COLUMN fiscal.certificate.storage_ref IS
    'Referencia opaca ao Vault/KMS; nunca armazenar segredo, PIN, chave ou bytes aqui.';

-- +goose Down

REVOKE SELECT, INSERT, UPDATE ON fiscal.fiscal_config FROM amparius_app;
DROP INDEX fiscal.fiscal_config_tenant_company_establishment_idx;
ALTER TABLE fiscal.fiscal_config
    DROP CONSTRAINT fiscal_config_no_overlapping_validity,
    DROP CONSTRAINT fiscal_config_municipal_registration_ck,
    DROP CONSTRAINT fiscal_config_state_registration_ck,
    DROP CONSTRAINT fiscal_config_validity_ck,
    DROP CONSTRAINT fiscal_config_establishment_fk,
    DROP COLUMN valid_to,
    DROP COLUMN valid_from,
    DROP COLUMN municipal_registration,
    DROP COLUMN state_registration,
    DROP COLUMN establishment_id;

ALTER TABLE fiscal.fiscal_config
    DROP CONSTRAINT fiscal_config_pkey;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM fiscal.fiscal_config
         GROUP BY company_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'nao e possivel reverter 0019: ha varias configuracoes por empresa';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE fiscal.fiscal_config
    DROP COLUMN id,
    ADD CONSTRAINT fiscal_config_pkey PRIMARY KEY (company_id);

ALTER TABLE identity.establishment
    DROP CONSTRAINT establishment_tenant_company_id_uq;
