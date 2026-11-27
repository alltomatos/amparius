# ADR 0047 — Sessões de caixa e conciliação

Decisão da #99 (FIN-02A, parte 1), aprovada em 27/11/2026.

A sessão pertence ao tenant e ao usuário que a abriu. Um índice parcial
garante uma sessão aberta por usuário. A baixa pode referenciar uma sessão
do mesmo tenant. Os movimentos e as baixas são registros append-only.

Fechamento, movimentos e baixas vinculadas coordenam o acesso à mesma linha
da sessão em PostgreSQL. O fechamento registra a contagem informada, o
esperado, a diferença e o usuário responsável. O esperado inclui somente
baixas em dinheiro, além do fundo de abertura e dos movimentos. A diferença
pode ser positiva ou negativa e não gera ajuste financeiro.

Após a confirmação no PostgreSQL, cada novo fechamento com diferença gera um log estruturado e incrementa um contador Prometheus sem rótulos.

As chaves de idempotência são separadas por tenant e operação. A abertura
e o fechamento registram chaves distintas na sessão. O fechamento guarda
também a nota informada para auditoria e comparação em replays. O papel da
aplicação recebe UPDATE apenas sobre as colunas usadas no fechamento.

O contrato online está em [finance-cash-sessions-v1.md](../contracts/finance-cash-sessions-v1.md).
OFX/CNAB, papéis específicos, política de tolerância, operação offline e
interface da Loja 04 pertencem a outras fatias.
