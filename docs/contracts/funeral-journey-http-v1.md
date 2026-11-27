# Contrato HTTP — jornada funerária v1

Status: vigente para FUN-02C (#40) desde 25/11/2026. A rota dev-only (`/_dev`)
só é montada quando o modo de desenvolvimento está ativo; entitlement do módulo
é verificado antes da operação.

Desde 26/11/2026 (issue #76 / E2E-01B5) a jornada também é exposta em rota
**autenticada**: `POST /v1/funeral/contracts/{contractId}/journey` com a
permissão `funeral:write`. O escopo de `tenant_id`/`company_id` vem do
principal autenticado; o payload, a idempotência, a atomicidade, os eventos e
os códigos de erro são os mesmos das duas superfícies. O envelope de erro de
produção é `{code, message, details, traceId}`.

## Criar a jornada

`POST /v1/funeral/contracts/{contractId}/journey` (autenticada) e
`POST /_dev/funeral/contracts/{contract_id}/journey` (dev-only) exigem
`Idempotency-Key` de até 160 caracteres. O contrato precisa estar `active`.
`tenant_id` e `company_id` vêm exclusivamente do contexto confiável, nunca do
JSON.

```json
{
  "service_order": {
    "kind": "wake",
    "description": "atendimento operacional",
    "scheduled_for": "2026-11-25T12:00:00-03:00"
  },
  "schedule": {
    "kind": "wake",
    "location_id": "<location_id>",
    "resource_id": "<resource_id>",
    "starts_at": "2026-11-25T12:00:00-03:00",
    "ends_at": "2026-11-25T14:00:00-03:00",
    "time_zone": "America/Fortaleza"
  }
}
```

O retorno `201` contém `journey.id`, `contract_id`, a OS e a agenda com seus
IDs opacos, estados, versões e históricos. Repetir a mesma chave e o mesmo
corpo retorna o mesmo conjunto de IDs; reutilizar a chave com outro corpo
retorna `409 IDEMPOTENCY_CONFLICT`.

## Atomicidade e eventos

Contrato ativo, OS, agenda, históricos, `funeral.journey_mutation` e eventos
`funeral.service_order.created`, `funeral.schedule.created` e
`funeral.journey.created` v1 são confirmados na mesma transação. Conflito do
local/recurso retorna `409 SCHEDULE_CONFLICT`; nenhum registro parcial fica
persistido.

Erros usam somente códigos estáveis e mensagens genéricas:

| HTTP | Código | Significado |
| --- | --- | --- |
| 400 | `INVALID_JOURNEY` | dados, estado ou contexto inválido |
| 404 | `JOURNEY_REFERENCE_NOT_FOUND` | contrato, local ou recurso fora do escopo |
| 409 | `IDEMPOTENCY_CONFLICT` | chave repetida com corpo diferente |
| 409 | `SCHEDULE_CONFLICT` | janela indisponível |
| 503 | `OUTBOX_UNAVAILABLE` | publicação transacional indisponível |

Nenhum erro ou evento publica nome, documento, contato, endereço, descrição ou
texto livre de atendimento.
