-- Amparius: hash do snapshot tributario em tax_calc.
-- Forward-only: a migration 0005 nao e editada.

-- +goose Up
-- jsonb nao preserva a forma textual exata do snapshot. O hash SHA-256 do
-- snapshot canonico calculado pelo motor fica em coluna propria para auditoria
-- e reproducao sem depender da serializacao do PostgreSQL.
ALTER TABLE fiscal.tax_calc
    ADD COLUMN calculation_hash char(64);

ALTER TABLE fiscal.tax_calc
    ADD CONSTRAINT tax_calc_calculation_hash_ck
        CHECK (calculation_hash IS NULL OR (
            calculation_hash ~ '^[0-9a-f]{64}$'
            AND calculation_hash = lower(calculation_hash)
        ));

COMMENT ON COLUMN fiscal.tax_calc.calculation_hash IS
    'SHA-256 do snapshot canonico do motor de tributos; NULL em linhas anteriores a 0022.';

-- +goose Down
ALTER TABLE fiscal.tax_calc
    DROP CONSTRAINT tax_calc_calculation_hash_ck,
    DROP COLUMN calculation_hash;
