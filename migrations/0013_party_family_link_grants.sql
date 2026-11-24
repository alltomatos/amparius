-- Amparius: grants mínimos para o vínculo familiar dev-only.
-- party.family_link já existe em 0002; esta migration só corrige permissões do
-- papel de aplicação, sem alterar modelo, política RLS ou dados.

-- +goose Up
-- SELECT para consultar o vínculo e INSERT para criá-lo. Não há UPDATE/DELETE
-- porque esta fatia não expõe alteração nem remoção.
GRANT SELECT, INSERT ON party.family_link TO amparius_app;

-- A validação de relation consulta identity.domain_value e o trigger
-- party.validate_family_link_domains faz a mesma leitura na inserção; sem este
-- SELECT o próprio INSERT falha com "permission denied". O catálogo é global,
-- somente leitura, e não carrega dado de tenant. O USAGE no schema identity já
-- vem de 0009, portanto não é repetido aqui para não ser revogado no Down.
GRANT SELECT ON identity.domain_value TO amparius_app;

-- +goose Down
REVOKE SELECT ON identity.domain_value FROM amparius_app;
REVOKE SELECT, INSERT ON party.family_link FROM amparius_app;
