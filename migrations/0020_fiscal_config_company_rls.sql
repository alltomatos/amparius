-- Amparius: RLS da configuracao fiscal tambem respeita a empresa do contexto.
-- Forward-only: correcoes futuras devem ser novas migrations.

-- +goose Up
DROP POLICY fiscal_config_tenant_isolation ON fiscal.fiscal_config;

CREATE POLICY fiscal_config_tenant_company_isolation ON fiscal.fiscal_config
    USING (
        tenant_id = current_setting('app.tenant_id')::uuid
        AND company_id = NULLIF(current_setting('app.company_id', true), '')::uuid
    )
    WITH CHECK (
        tenant_id = current_setting('app.tenant_id')::uuid
        AND company_id = NULLIF(current_setting('app.company_id', true), '')::uuid
    );

-- +goose Down
DROP POLICY fiscal_config_tenant_company_isolation ON fiscal.fiscal_config;

CREATE POLICY fiscal_config_tenant_isolation ON fiscal.fiscal_config
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
