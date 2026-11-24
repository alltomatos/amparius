-- Amparius: grants mínimos para o cadastro dev-only de falecido.
-- A tabela party.deceased já existe em 0002; esta migration só corrige
-- permissões do papel de aplicação sem alterar o modelo ou a política RLS.

-- +goose Up
-- 0010 já concede USAGE em party e SELECT/INSERT/UPDATE/DELETE em
-- party.person. Não repetimos esses grants aqui para que o Down não revogue
-- privilégios de uma migration anterior; o conjunto efetivo necessário inclui
-- USAGE em party e SELECT em party.person.
GRANT SELECT ON party.party TO amparius_app;
GRANT SELECT, INSERT ON party.deceased TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT ON party.deceased FROM amparius_app;
REVOKE SELECT ON party.party FROM amparius_app;
