-- Amparius: remove mutacoes que nao fazem parte do contrato Finance MVP.
-- Forward-only: esta migration corrige grants efetivos sem editar 0015 ou 0003.

-- +goose Up
REVOKE UPDATE, DELETE ON finance.account,
    finance.cost_center,
    finance.bank_account,
    finance.title,
    finance.installment,
    finance.payment,
    finance.cash_session,
    finance.cash_movement,
    finance.ledger_entry FROM amparius_app;

-- +goose Down
-- Nenhum UPDATE/DELETE e restaurado: esses privilegios nao pertencem ao
-- contrato Finance MVP e podem ter vindo de grants manuais anteriores.
