# Contrato HTTP financeiro v1 — títulos e pagamentos

Status: implementado, 26/11/2026 (issue #76 / E2E-01B5, fatia de financeiro
online autenticado).

Base `/v1`. Todas as rotas exigem `Authorization: Bearer <token>`; tenant e
empresa vêm do principal autenticado e nunca do corpo ou de cabeçalhos. Os
valores de domínio (`direction`, `status`, `origin`, `method`) são validados
pelo gatilho `finance.validate_domains` contra `identity.domain_value`
(fail-closed). `finance.payment`, `finance.title` e `finance.installment` são
append-only: a trilha não é sobrescrita nem removida.

## `POST /v1/finance/titles`

Exige `finance:write`. Cria um título no tenant do principal.

```json
{"direction":"receivable","amount_cents":10000,"due_date":"2026-12-01","status":"open","origin":"sale"}
```

`direction`, `amount_cents`, `due_date`, `status` e `origin` são obrigatórios;
`party_id` e `description` são opcionais. `origin` aceita `sale`, `purchase` ou
`manual`; `direction` aceita `receivable` ou `payable`.

Resposta `201 Created` com o título (`id`, `direction`, `party_id`,
`amount_cents`, `due_date`, `status`, `origin`, `description`).

## `GET /v1/finance/titles` e `GET /v1/finance/titles/{id}`

Exigem `finance:read`. O middleware autentica e valida o vínculo do usuário
à empresa do principal. O título pertence ao **tenant inteiro** e o schema
vigente não tem `company_id`. Membros autorizados de empresas distintas do
mesmo tenant veem o mesmo conjunto financeiro. Um id de outro tenant
responde `404 FINANCE_REFERENCE_NOT_FOUND`.

A lista ordena por `due_date ASC, id ASC` e aceita os parâmetros opcionais
abaixo, uma vez cada:

| Parâmetro | Valores |
| --- | --- |
| `limit` | Inteiro positivo. Sem valor, ou acima de 1.000, usa 100 como no contrato anterior. |
| `status` | `open`, `partially_paid`, `paid`, `cancelled`. Usa o estado persistido no título. |
| `direction` | `receivable`, `payable`. |
| `due_from`, `due_to` | Datas civis `AAAA-MM-DD` inclusivas, independentes. |
| `cursor` | Valor opaco de `next_cursor` da resposta anterior. Repita os mesmos filtros. |

Exemplo: `GET /v1/finance/titles?direction=receivable&status=open&due_to=2026-11-30&limit=50`.
A resposta continua usando `{"items":[...]}` e inclui `next_cursor` somente
quando há outra página. Uma página vazia retorna `200 {"items":[]}`. A
próxima página começa estritamente depois do último par de vencimento e ID.
Cada requisição consulta o estado atual, inclusive quando há alterações entre
páginas. Vencimento é a data do título. Saldo e situação das parcelas exigem
consulta própria.

## `POST /v1/finance/payments`

Exige `finance:write` e o cabeçalho `Idempotency-Key` (1 a 200 caracteres).
Registra uma baixa (pagamento) sobre uma parcela existente.

```json
{"installment_id":"<uuid>","amount_cents":5000,"method":"pix","reference":"replay"}
```

`installment_id`, `amount_cents` (positivo) e `method` são obrigatórios;
`paid_at` e `reference` são opcionais. `method` aceita `cash`, `pix`,
`bank_transfer`, `card` ou `boleto`.

O gatilho `finance.validate_payment_balance` rejeita quando a soma dos
pagamentos da parcela excede o valor dela (`409 FINANCE_CONFLICT`), com o lock
`SELECT ... FOR UPDATE` serializando escritores concorrentes. Repetir a mesma
`Idempotency-Key` com o mesmo corpo devolve o mesmo `id` sem duplicar o
lançamento; a chave de idempotência é persistida em
`finance.payment.idempotency_key`.

Resposta `201 Created` com o pagamento (`id`, `installment_id`,
`bank_account_id`, `amount_cents`, `paid_at`, `method`, `reference`,
`idempotency_key`).

## Erros

Envelope `{code, message, details, traceId}`.

| Código | HTTP | Quando |
| --- | --- | --- |
| `AUTHENTICATION_REQUIRED` | 401 | credencial ausente ou inválida |
| `SCOPE_FORBIDDEN` | 403 | empresa/tenant fora do escopo do principal |
| `PERMISSION_DENIED` | 403 | papel sem `finance:read`/`finance:write` |
| `VALIDATION_ERROR` | 400 | corpo, campo ou filtro inválido, `Idempotency-Key` ausente/inválida |
| `FINANCE_REFERENCE_NOT_FOUND` | 404 | título, parcela ou referência inexistente/outro tenant |
| `FINANCE_CONFLICT` | 409 | domínio rejeitado, sobrepagamento ou duplicidade |
| `FINANCE_UNAVAILABLE` | 503 | repositório indisponível |

Nenhuma mensagem de erro inclui PII.

## Limite da fatia

A jornada contrato → ordem de serviço → agenda (funeral) e a consulta de
título com parcelas continuam em fatias próprias da #76. O financeiro
compartilha o mesmo repositório do modo desenvolvimento (`/_dev/finance/*`),
que permanece fora do edge público.

A integração financeira da Loja 04 deve exibir a abrangência de grupo
econômico enquanto o modelo for tenant-wide. Veja o [recorte e a verificação
da #79](../design/mvp-store-dashboard-read-evidence.md).
