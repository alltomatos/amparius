# Caixa, sessões e conciliação

ID da ajuda: `caixa-sessoes-e-conciliacao`

Use este guia para operar e conferir o caixa no Amparius online. As
consultas exigem `finance:read` e os registros exigem `finance:write`.

## Abrir o caixa e lançar movimentos

1. Informe o valor em dinheiro disponível no início do turno. O valor pode
   ser zero. Cada usuário pode ter uma sessão aberta por grupo econômico
   (tenant). Se já houver uma, consulte essa sessão. Uma nova abertura
   retorna `409 CASH_SESSION_ALREADY_OPEN`.
2. Registre cada suprimento como entrada e cada sangria como saída. Informe
   a sessão aberta, o tipo, o valor positivo e o motivo (`reason`). Use
   descrições operacionais como “Suprimento de troco” ou “Sangria”.
3. Ao dar baixa em uma parcela do título, informe a parcela, o valor
   positivo e o meio de pagamento. Para que a baixa apareça na conciliação,
   vincule-a a uma sessão aberta do mesmo grupo econômico (`cash_session_id`).
   O vínculo é opcional. Baixas vinculadas em dinheiro entram no valor
   esperado. Pix, transferência, cartão e boleto aparecem no detalhamento
   por meio de pagamento.

## Fechar e conferir a diferença

Conte o dinheiro físico e informe o total contado, que pode ser zero.
A nota de fechamento (`note`) é opcional. Nos campos livres `reason` e
`note`, registre apenas o contexto da operação. Nunca digite dados pessoais
como nomes, CPF, telefone ou e-mail.

- **Esperado** = valor de abertura + entradas − saídas + baixas vinculadas em dinheiro.
- **Diferença** = valor contado − valor esperado. Um valor negativo indica
  falta, um positivo indica sobra e zero indica que a contagem bateu.

Exemplo fictício. Abertura de R$ 100,00, entrada de R$ 20,00, saída de
R$ 5,00 e baixa em dinheiro de R$ 30,00 resultam em R$ 145,00 esperados.
Uma baixa de R$ 40,00 em Pix aparece no detalhamento. Se a contagem der
R$ 144,70, o fechamento registra uma falta de R$ 0,30. A diferença fica
gravada para conferência. Os lançamentos são preservados e o sistema não
gera um ajuste financeiro automático.

Consulte as sessões abertas ou fechadas. No detalhe e na conciliação,
confira os totais de entradas, saídas e baixas por meio de pagamento.
Enquanto a sessão estiver aberta, o valor esperado é calculado na consulta.
Depois do fechamento, confira também o valor contado e a diferença gravada.

## Sessão fechada e repetição segura

Se a sessão já estiver fechada, uma nova tentativa de fechar, movimentar
ou vincular uma baixa retorna `409 CASH_SESSION_ALREADY_CLOSED`. Consulte
a conciliação da sessão para conferir o fechamento.

Se faltar confirmação de uma abertura, movimento, baixa ou fechamento,
repita a mesma operação com os mesmos dados e a mesma chave de idempotência
(`Idempotency-Key`). Se a operação já tiver sido gravada, o Amparius devolve
o registro existente, mesmo após o fechamento da sessão. Reutilizar a chave
da mesma operação com dados diferentes retorna `409 IDEMPOTENCY_CONFLICT`.

## Escopo da primeira parte do FIN-02A (#99)

Esta entrega cobre a operação online pelo contrato do SaaS. OFX/CNAB,
papéis específicos de caixa, política de tolerância para diferenças,
operação offline e a tela Loja 04 pertencem a outras fatias.

[Contrato de caixa](../contracts/finance-cash-sessions-v1.md) ·
[Contrato de títulos e pagamentos](../contracts/finance-titles-payments-v1.md)
