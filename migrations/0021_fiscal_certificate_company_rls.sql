-- Amparius: certificado fiscal e referencia de cofre por empresa, com RLS.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up

-- A consulta fail-closed da FIS-01B precisa ler o certificado do CNPJ do fato
-- gerador. O papel recebe somente SELECT: cadastro/rotacao de certificado nao
-- faz parte do contrato da aplicacao e continua no cofre/administracao.
GRANT SELECT ON fiscal.certificate TO amparius_app;

-- Espelha fiscal_config: tenant e empresa do contexto confiavel, nunca o corpo
-- HTTP. Sem app.company_id na transacao, nenhum certificado e visivel.
DROP POLICY certificate_tenant_isolation ON fiscal.certificate;

CREATE POLICY certificate_tenant_company_isolation ON fiscal.certificate
    USING (
        tenant_id = current_setting('app.tenant_id')::uuid
        AND company_id = NULLIF(current_setting('app.company_id', true), '')::uuid
    )
    WITH CHECK (
        tenant_id = current_setting('app.tenant_id')::uuid
        AND company_id = NULLIF(current_setting('app.company_id', true), '')::uuid
    );

-- +goose Down

DROP POLICY certificate_tenant_company_isolation ON fiscal.certificate;

CREATE POLICY certificate_tenant_isolation ON fiscal.certificate
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

REVOKE SELECT ON fiscal.certificate FROM amparius_app;
