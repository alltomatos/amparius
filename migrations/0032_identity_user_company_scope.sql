-- Amparius: vínculo explícito entre usuário autenticado e empresa/CNPJ.
-- Forward-only: correções futuras devem ser novas migrações.

-- +goose Up
CREATE TABLE identity.user_company (
    tenant_id uuid NOT NULL REFERENCES identity.tenant (id),
    user_id uuid NOT NULL,
    company_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, company_id),
    CONSTRAINT user_company_tenant_user_fk
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES identity.app_user (tenant_id, id),
    CONSTRAINT user_company_tenant_company_fk
        FOREIGN KEY (tenant_id, company_id)
        REFERENCES identity.company (tenant_id, id)
);

CREATE INDEX user_company_tenant_user_idx
    ON identity.user_company (tenant_id, user_id, company_id);

ALTER TABLE identity.user_company ENABLE ROW LEVEL SECURITY;

CREATE POLICY user_company_tenant_isolation ON identity.user_company
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

GRANT SELECT ON identity.user_company TO amparius_app;

-- +goose Down
DROP TABLE identity.user_company;
