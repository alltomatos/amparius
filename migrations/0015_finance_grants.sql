-- Amparius: grants minimos para o papel da aplicacao no financeiro.
-- Forward-only: nao editar migrations aplicadas; correcoes futuras viram novas migrations.

-- +goose Up
GRANT USAGE ON SCHEMA finance TO amparius_app;
GRANT SELECT, INSERT ON finance.account,
    finance.cost_center,
    finance.bank_account,
    finance.title,
    finance.installment,
    finance.payment,
    finance.cash_session,
    finance.cash_movement,
    finance.ledger_entry TO amparius_app;
-- O contrato Go atual somente cria e consulta registros. UPDATE/DELETE nao sao
-- concedidos: pagamentos, movimentos, ledger e historico devem ser preservados.

-- +goose Down
REVOKE SELECT, INSERT ON finance.account,
    finance.cost_center,
    finance.bank_account,
    finance.title,
    finance.installment,
    finance.payment,
    finance.cash_session,
    finance.cash_movement,
    finance.ledger_entry FROM amparius_app;
REVOKE USAGE ON SCHEMA finance FROM amparius_app;
