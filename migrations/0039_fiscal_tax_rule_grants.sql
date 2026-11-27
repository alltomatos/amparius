-- Amparius: grants minimos de fiscal.tax_rule para o papel da aplicacao.
-- Forward-only: nao editar migrations aplicadas; correcoes futuras viram novas migrations.
--
-- Achado da homologacao de 2026-11-27: as migrations 0004..0035 habilitam RLS e
-- policies em fiscal.tax_rule, mas nenhuma concede privilegio de tabela a
-- amparius_app. Em banco migrado do zero, fiscal.Checker.LookupTaxRule e o motor
-- de tributos falham com "permission denied for table tax_rule" (fail-closed,
-- porem inoperante). Ambientes que passavam dependiam de GRANT manual nao
-- versionado.
--
-- Somente SELECT e INSERT: regra tributaria e datada (valid_from/valid_to) e
-- uma correcao vira nova linha; UPDATE/DELETE nao sao concedidos. A policy de
-- INSERT continua limitando a escrita ao tenant corrente; regras globais
-- (tenant_id NULL) so entram por papel administrativo.

-- +goose Up
GRANT SELECT, INSERT ON fiscal.tax_rule TO amparius_app;

-- +goose Down
REVOKE SELECT, INSERT ON fiscal.tax_rule FROM amparius_app;
