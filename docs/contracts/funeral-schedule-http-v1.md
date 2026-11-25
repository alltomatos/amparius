# Contrato HTTP — agenda funerária v1

Status: vigente para FUN-02A/FUN-02B desde 25/11/2026. As rotas são dev-only,
exigem o opt-in e entitlement `funeral` existentes e recebem tenant/empresa do
contexto autenticado; esses campos não são aceitos no JSON.

## Transições

`POST /_dev/funeral/schedules/{id}/confirm` e
`POST /_dev/funeral/schedules/{id}/cancel` exigem o header
`Idempotency-Key` (1–200 caracteres). O corpo pode ser omitido ou conter
somente:

```json
{"reason":"cliente confirmou"}
```

`reason` é opcional, limitado a 500 caracteres e fica no histórico append-only;
não é publicado na outbox. Confirmação aceita somente `scheduled` e produz
`confirmed`. Cancelamento aceita `scheduled` ou `confirmed` e produz
`cancelled`. `confirmed` continua reservando o local e o recurso.

Replay com a mesma chave e conteúdo devolve `200` com o mesmo agregado. A
mesma chave com conteúdo diferente devolve `409 IDEMPOTENCY_CONFLICT`.

Na criação, `service_order_id`, quando enviado, precisa pertencer ao mesmo
contrato, empresa e tenant. A API valida a referência antes do banco e devolve
somente código estável, sem expor a causa SQL.

## Respostas e erros

Sucesso:

```json
{"status":"ok","schedule":{"id":"<uuid>","status":"confirmed","version":2}}
```

O objeto completo inclui as referências UUID, intervalo UTC, timezone IANA,
versão e `status_history`. Os headers incluem `Content-Type`,
`X-Content-Type-Options: nosniff` e `Cache-Control: no-store`.

| HTTP | `error.code` | Quando |
|---:|---|---|
| 400 | `INVALID_JSON` | JSON ausente quando necessário, inválido ou com campo desconhecido |
| 400 | `INVALID_SCHEDULE` | transição ou dados inválidos |
| 404 | `SCHEDULE_REFERENCE_NOT_FOUND` | agenda ou referência fora do escopo |
| 409 | `SCHEDULE_CONFLICT` | local/recurso sobreposto |
| 409 | `IDEMPOTENCY_CONFLICT` | chave reutilizada com conteúdo diferente |
| 503 | `OUTBOX_UNAVAILABLE` | outbox indisponível |

Mensagens de erro são estáveis e não incluem SQL, nomes, descrição, party,
contato ou qualquer PII.
