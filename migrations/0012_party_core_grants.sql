-- Amparius: grants mínimos para o cadastro base de party.
-- A migration é separada dos grants de deceased para preservar fronteiras
-- entre as fatias e permitir rollback independente.

-- +goose Up
GRANT SELECT, INSERT, UPDATE ON party.party TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT, UPDATE ON party.party FROM amparius_app;
