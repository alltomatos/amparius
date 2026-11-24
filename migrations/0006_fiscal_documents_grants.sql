-- Amparius: permissao do papel da aplicacao para a trilha fiscal.
-- Forward-only: esta migration complementa 0005 sem editar migration aplicada.

-- +goose Up
GRANT USAGE ON SCHEMA fiscal TO amparius_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON fiscal.fiscal_document,
    fiscal.fiscal_item, fiscal.tax_calc, fiscal.emission_attempt TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT, UPDATE, DELETE ON fiscal.fiscal_document,
    fiscal.fiscal_item, fiscal.tax_calc, fiscal.emission_attempt FROM amparius_app;
REVOKE USAGE ON SCHEMA fiscal FROM amparius_app;
