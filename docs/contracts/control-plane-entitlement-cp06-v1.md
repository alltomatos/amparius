# Contrato HTTP do entitlement CP-06 v1

Status: implementado em 26/11/2026. Base: `/v1`.

Todas as rotas exigem Bearer OIDC/MFA, tenant e empresa no principal/contexto.
`X-Tenant-Id` e qualquer tenant/empresa do JSON são ignorados para autorização.
O CNPJ é representado somente pela empresa já autorizada; quando aparecer em
outra resposta, permanece mascarado.

## Leitura

### `GET /v1/companies/{companyId}/entitlements/{module}`

Retorna o detalhe do catálogo e da licença da empresa. O retorno contém
`id`, `tenantId`, `companyId`, `module`, `moduleName`, `kind`, `status`,
`quota`, `quotaConfigured`, `used`, `validFrom`, `validTo`, `version` e
`updatedAt`. Módulo core pode ter `id = null`; ele continua `active` sem
assignment.

### Auditoria

`GET /v1/companies/{companyId}/entitlements/{module}/audit` lista os eventos
do módulo/empresa. Também existe
`GET /v1/entitlements/{entitlementId}/audit` para assignment persistida.
Ambas aceitam `limit` de 1 a 100 e `cursor` opaco, retornando:

```json
{"items":[{"id":"uuid","tenantId":"uuid","companyId":"uuid","module":"funeral","actorId":"uuid","action":"activated","origin":"admin","at":"2026-11-26T03:00:00Z"}],"nextCursor":null}
```

`origin` pode filtrar `admin` ou `self_service`. A trilha não expõe PII nem
payload livre. Alterações de quota ficam em `entitlement.quota_event` e são
append-only no banco.

## Escrita administrativa

As escritas exigem `entitlement:write`, `Idempotency-Key` de 1 a 200 caracteres
e são executadas com `origin = admin` e `actorId` do principal autenticado.

### `PUT /v1/companies/{companyId}/entitlements/{module}`

Concede ou reativa uma licença opcional. Corpo:

```json
{"validFrom":"2026-12-01","validTo":null,"version":1}
```

`version` é opcional no PUT; quando enviado, evita sobrescrever uma versão
concorrente.

### `PATCH /v1/companies/{companyId}/entitlements/{module}`

Exige `version`. Para licença, aceita `status` (`active`, `inactive` ou
`revoked`), `validFrom`, `validTo` e `clearValidTo`. Para quota, aceita
`quota` inteira não negativa. Quando a quota ainda não foi configurada, o
detalhe expõe `version = 0`, valor válido somente para a primeira configuração.
A alteração de quota é a cota do módulo no
tenant, com a empresa no caminho como escopo autorizado; não existe quota
independente por filial/CNPJ neste modelo.

Uma requisição PATCH altera licença ou quota, não as duas simultaneamente.

## Idempotência, concorrência e erros

A chave, hash do pedido e resultado são persistidos em
`entitlement.mutation_idempotency` na mesma transação da mudança. Repetir a
mesma chave e corpo devolve o resultado original sem novo efeito/auditoria;
reutilizar a chave com outro corpo retorna `409 IDEMPOTENCY_CONFLICT`.

`409 STALE_VERSION` indica versão obsoleta; `409 LICENSE_POOL_EXHAUSTED`, cota
esgotada; `409 LICENSE_NOT_AVAILABLE`, quota não configurada; e
`409 CORE_MODULE_IMMUTABLE`, tentativa sobre módulo núcleo. Erros seguem o
envelope comum com `code`, `message`, `details` e `traceId`.

Migration: `migrations/control/0003_entitlement_cp06.sql`.
