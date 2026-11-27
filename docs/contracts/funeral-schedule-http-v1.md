# Contrato HTTP — agenda funerária v1

Status: vigente para FUN-02A/FUN-02B desde 25/11/2026 e ampliado pela leitura
autenticada da #79. As rotas `/_dev` exigem o opt-in e entitlement `funeral`
existentes e recebem tenant/empresa do contexto autenticado.

## Agenda da empresa

`GET /v1/funeral/agenda?date=2026-11-26&time_zone=America/Fortaleza` exige
Bearer, `funeral:read` e licença `funeral` vigente para a empresa do
principal, verificada no instante da consulta. Tenant e empresa vêm do
principal validado. O endpoint aceita `date` e `time_zone` obrigatórios e
`end_date` opcional, cada um uma vez. As datas são civis, no formato
`AAAA-MM-DD`, no fuso IANA informado. O intervalo `[date, end_date)` tem
de 1 a 31 dias civis. Ao omitir `end_date`, usa-se o início do próximo dia
no fuso. Transições de fuso podem produzir dias com 23 ou 25 horas.

A resposta usa `Cache-Control: no-store` e contém:

- `window` com `date`, `end_date`, `time_zone`, `starts_at` e `ends_at`.
- `items` com agendamentos que sobrepõem a janela, ordenados por
  `starts_at, id`, incluindo o status persistido (`scheduled`, `confirmed`,
  `completed` ou `cancelled`). Cada registro contém `id`, `contract_id`,
  `kind`, `status`, `location_id`, `starts_at`, `ends_at`, `time_zone` e,
  quando presentes, `service_order_id` e `resource_id`. Os instantes são UTC
  em RFC 3339. O fuso do item vem do agendamento.
- `pending` com `{"kind":"schedule_confirmation","schedule_id":"<uuid>"}`
  para cada item em `scheduled`. Pessoas, descrições, motivos do histórico,
  tarefas da fila local e pendências de outros módulos ficam fora da projeção.

Sem registros, devolve `200` com `items: []` e `pending: []`.
Os erros de `/v1` usam `{code, message, details, traceId}`.

| HTTP | Código | Quando |
| --- | --- | --- |
| 400 | `VALIDATION_ERROR` | data, fuso, janela ou parâmetro inválido |
| 401 | `AUTHENTICATION_REQUIRED` | credencial ausente ou inválida |
| 403 | `SCOPE_FORBIDDEN` | empresa fora do escopo |
| 403 | `PERMISSION_DENIED` | falta `funeral:read` |
| 403 | `ENTITLEMENT_MODULE_NOT_LICENSED` | licença ausente, expirada ou revogada |
| 503 | `AUTHENTICATION_UNAVAILABLE` | autenticador indisponível |
| 503 | `ENTITLEMENT_UNAVAILABLE` | fonte de entitlement indisponível |
| 503 | `FUNERAL_UNAVAILABLE` | leitura da agenda indisponível |

## Transições em desenvolvimento

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

## Respostas e erros em desenvolvimento

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
