# Contrato HTTP v1 de sessões de caixa

A fatia FIN-02A da #99 opera no SaaS online. Todas as operações usam o
tenant autenticado. Para abertura e fechamento, a API usa o ID do usuário
autenticado. Cada usuário pode ter uma sessão aberta por tenant.
As leituras usam `finance:read` e as escritas usam `finance:write`.

| Método e caminho | Corpo ou filtros | Resposta |
| --- | --- | --- |
| `POST /v1/finance/cash-sessions` | `{"opening_amount_cents":10000}` | 201 e sessão aberta |
| `GET /v1/finance/cash-sessions` | `status=open\|closed`, `limit`, `cursor` opcionais | 200 com `items` e `next_cursor` quando houver outra página |
| `GET /v1/finance/cash-sessions/{id}` | UUID no caminho | 200 com sessão e totais |
| `POST /v1/finance/cash-sessions/{id}/close` | `{"counted_amount_cents":14470,"note":"Conferência de turno"}` | 200 e sessão fechada. `note` é opcional |
| `POST /v1/finance/cash-movements` | `{"session_id":"<uuid>","kind":"out","amount_cents":500,"reason":"Sangria"}` | 201 e movimento. `kind` aceita `in` (suprimento) ou `out` (sangria) |
| `GET /v1/finance/cash-sessions/{id}/movements` | `limit` opcional | 200 com `items` em ordem de `occurred_at,id` |
| `GET /v1/finance/cash-sessions/{id}/reconciliation` | UUID no caminho | 200 com sessão e totais |
| `POST /v1/finance/payments` | `cash_session_id` opcional no corpo da baixa | 201 com o vínculo da baixa. [Contrato de pagamentos](finance-titles-payments-v1.md) |

A listagem ordena por `opened_at,id`. O cursor opaco aponta para o último
item da página anterior. Reutilize os filtros ao passar o cursor. O limite
padrão é 100 e valores acima de 1000 usam o padrão, como na listagem de
títulos.

A sessão expõe `id`, `user_id`, `status`, `opened_at`, `closed_at`,
`opening_amount_cents`, `expected_amount_cents`, `counted_amount_cents`,
`difference_amount_cents`, `closed_by` e `note`. Os campos do fechamento
são nulos enquanto a sessão está aberta. No detalhe e na conciliação o
esperado de sessão aberta é calculado na leitura. Essas duas respostas
também incluem `movement_in_cents`, `movement_out_cents` e
`payments_by_method_cents`. O mapa de pagamentos inclui os métodos com
baixas vinculadas. Um movimento expõe `id`, `session_id`, `kind`,
`amount_cents`, `reason`, `occurred_at` e `idempotency_key`. O servidor
atribui os horários de caixa e os retorna em UTC.

Use `reason` (movimento) e `note` (fechamento) apenas para registrar o contexto operacional. Não inclua dados pessoais, como nomes, CPF ou contatos.

## Conciliação

`expected_amount_cents = opening_amount_cents + movement_in_cents - movement_out_cents + payments_by_method_cents.cash`.
Sem baixa em dinheiro, o termo correspondente vale zero.
`difference_amount_cents = counted_amount_cents - expected_amount_cents`.
Pix, transferência, cartão e boleto entram no detalhamento por método.
Valores monetários são inteiros em centavos. Abertura e contagem aceitam
zero. Movimentos e baixas exigem valores positivos.

Exemplo com abertura de 10000, suprimento de 2000, sangria de 500, baixa em
dinheiro de 3000 e Pix de 4000. Se a contagem for de 14470 centavos, o
fechamento persiste a diferença de -30 centavos e preserva os lançamentos.

Cada novo fechamento confirmado com diferença emite `finance_cash_session_close_difference` e incrementa `amparius_finance_cash_session_close_difference_total`; a API expõe o contador em `127.0.0.1:$API_METRICS_PORT/metrics` quando a porta é configurada.

```json
{
  "status": "closed",
  "opening_amount_cents": 10000,
  "movement_in_cents": 2000,
  "movement_out_cents": 500,
  "payments_by_method_cents": {"cash": 3000, "pix": 4000},
  "expected_amount_cents": 14500,
  "counted_amount_cents": 14470,
  "difference_amount_cents": -30
}
```

## Idempotência e erros

Cada escrita exige `Idempotency-Key` (1 a 200 caracteres após remover
espaços nas pontas). A chave é única por tenant e tipo de operação. Com
a mesma chave e os mesmos dados, o replay devolve o registro existente,
inclusive após o fechamento. Chaves de abertura e fechamento podem ter o
mesmo texto porque são de operações distintas. Na abertura e no fechamento,
o usuário autenticado também compõe a identidade da operação. Strings do
corpo são normalizadas removendo espaços nas pontas.

Erros usam o envelope `{code, message, details, traceId}`.

| HTTP | Código | Condição |
| --- | --- | --- |
| 400 | `VALIDATION_ERROR` | Corpo, parâmetro, UUID ou chave inválida ou ausente quando exigida |
| 401 | `AUTHENTICATION_REQUIRED` / `UNAUTHENTICATED` | Credencial inválida ou usuário ausente em escrita de caixa |
| 403 | `PERMISSION_DENIED` / `SCOPE_FORBIDDEN` | Papel ou escopo rejeitado |
| 404 | `FINANCE_REFERENCE_NOT_FOUND` | Sessão ou referência inexistente no tenant |
| 409 | `CASH_SESSION_ALREADY_OPEN` | Sessão aberta para o usuário quando uma nova abertura é pedida |
| 409 | `CASH_SESSION_ALREADY_CLOSED` | Nova operação de fechar, movimentar ou vincular baixa em sessão fechada |
| 409 | `IDEMPOTENCY_CONFLICT` | Chave já registrada com outros dados |
| 409 | `FINANCE_CONFLICT` | Regra financeira rejeitou a operação, como sobrepagamento |
| 503 | `FINANCE_UNAVAILABLE` | Repositório indisponível |

## Implantação

Aplique a migração `0040` antes de disponibilizar o código. Registros legados
fechados recebem o valor de abertura como esperado e contado, diferença zero
e `closed_at=opened_at` quando o horário de fechamento estiver ausente.
Esses valores históricos identificam um preenchimento de migração.
Mais de uma sessão legada aberta para o mesmo usuário no tenant fará a
migração falhar atomicamente para evitar fechamento implícito.

OFX/CNAB, papéis específicos de caixa, tolerância, relay/offline e Loja 04
são tratados em outras fatias.

## Latência local

Medição em 27/11/2026 no Windows, com PostgreSQL 16 em Docker descartável
via loopback. As requisições passaram pelo roteador HTTP, autenticação
sintética, repositório e PostgreSQL real com o papel `amparius_app`.
O teste executou 20 ciclos de aquecimento e 200 ciclos sequenciais de
abertura, sangria, baixa em dinheiro, fechamento e consulta de conciliação.
Cada duração inclui o processamento HTTP no processo de teste e a
transação no banco. Percentis pelo posto mais próximo, em milissegundos.
A medição local não estabelece um SLO de produção.

| Operação | n | p50 (ms) | p95 (ms) | p99 (ms) |
| --- | ---: | ---: | ---: | ---: |
| Abrir | 200 | 10,347 | 13,904 | 18,015 |
| Movimentar | 200 | 11,889 | 16,010 | 19,182 |
| Baixar | 200 | 12,128 | 16,088 | 22,050 |
| Fechar | 200 | 11,913 | 16,109 | 19,306 |
| Conciliar | 200 | 5,960 | 7,845 | 9,882 |

Com o banco descartável migrado até `0040` e os DSNs de aplicação e
administração configurados em `AMPA_TEST_POSTGRES_DSN` e
`AMPA_TEST_POSTGRES_ADMIN_DSN`, execute no PowerShell:

```powershell
$env:AMPA_FIN02A_LATENCY = '1'
go test ./internal/modules/finance -run '^TestCashSessionHTTPPostgres$' -v -count=1
Remove-Item Env:AMPA_FIN02A_LATENCY
```
