-- Amparius control plane: unicidade de licenca ativa e grants do papel de aplicacao.
-- Forward-only: nao edita 0001_entitlement.sql. Aplicar com:
--   goose -dir migrations/control postgres "$AMPA_CONTROL_POSTGRES_DSN" up
--
-- A unicidade parcial garante "1 licenca ativa = 1 modulo em 1 empresa (CNPJ)".
-- Reativacao atualiza a mesma linha (id e vigencia preservados); nao cria uma
-- segunda licenca ativa concorrente. Historico inativo/revogado continua livre.
-- Se houver duplicidade ativa pre-existente, a migration falha de proposito e
-- exige reconciliacao antes de aplicar.

-- +goose Up
CREATE UNIQUE INDEX license_assignment_one_active_per_company_uq
    ON entitlement.license_assignment (tenant_id, module_code, company_id)
    WHERE status = 'active';

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'amparius_control_app') THEN
        GRANT USAGE ON SCHEMA entitlement TO amparius_control_app;
        GRANT SELECT ON entitlement.module,
                        entitlement.module_kind,
                        entitlement.license_status,
                        entitlement.entitlement_action,
                        entitlement.entitlement_origin
            TO amparius_control_app;
        GRANT SELECT, INSERT, UPDATE ON entitlement.license_assignment,
                                        entitlement.license_pool,
                                        entitlement.user_quota
            TO amparius_control_app;
        GRANT SELECT, INSERT ON entitlement.entitlement_event TO amparius_control_app;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP INDEX entitlement.license_assignment_one_active_per_company_uq;
