-- Amparius: grants mínimos para a fatia dev-only de catálogo.
-- catalog.catalog_item, catalog.price_list e catalog.price já existem em
-- 0002 com RLS; esta migration só ajusta permissões do papel de aplicação.
-- Forward-only: correções futuras devem ser novas migrations.

-- +goose Up
GRANT USAGE ON SCHEMA catalog TO amparius_app;
-- SELECT para consulta/listagem e INSERT para criação. Não há UPDATE/DELETE
-- porque esta fatia não expõe alteração nem remoção.
GRANT SELECT, INSERT ON catalog.catalog_item TO amparius_app;
GRANT SELECT, INSERT ON catalog.price_list TO amparius_app;
GRANT SELECT, INSERT ON catalog.price TO amparius_app;
-- A leitura de identity.domain_value usada pela validação do repositório e pelo
-- trigger catalog_item_validate_domains já vem de 0013_party_family_link_grants.
-- O USAGE em party, exigido pela chamada party.assert_domain_value do trigger,
-- já vem de 0010_party_social_name. Nenhum dos dois é repetido aqui para que o
-- Down desta migration não revogue um grant de outra fatia.

-- +goose Down
REVOKE SELECT, INSERT ON catalog.price FROM amparius_app;
REVOKE SELECT, INSERT ON catalog.price_list FROM amparius_app;
REVOKE SELECT, INSERT ON catalog.catalog_item FROM amparius_app;
REVOKE USAGE ON SCHEMA catalog FROM amparius_app;
