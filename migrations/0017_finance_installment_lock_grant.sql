-- Amparius: privilegio necessario ao lock da parcela no trigger de pagamento.
-- Forward-only: corrige o contrato sem editar migrations aplicadas.

-- +goose Up
-- finance.validate_payment_balance() usa SELECT ... FOR UPDATE para
-- serializar pagamentos concorrentes. PostgreSQL exige UPDATE para esse lock;
-- o trigger continua sendo a protecao contra sobrepagamento.
GRANT UPDATE ON finance.installment TO amparius_app;

-- +goose Down
REVOKE UPDATE ON finance.installment FROM amparius_app;
