# Contrato HTTP do contexto do control plane v1

Status: implementado como leitura segura, 25/11/2026.

Base: `/v1`. Todas as rotas exigem `Authorization: Bearer <token>` e são
montadas por `http.AuthenticatedRoute`. O tenant vem do principal autenticado;
`X-Tenant-Id` é ignorado para autorização.

## `GET /v1/context`

Retorna o tenant e a empresa fixados no token autenticado. A empresa precisa
estar vinculada ao usuário em `identity.user_company` e pertencer ao tenant sob
RLS.

```json
{
  "tenant": {"id": "uuid", "name": "Grupo", "status": "active"},
  "company": {
    "id": "uuid",
    "tenantId": "uuid",
    "legalName": "Funerária",
    "cnpj": "**********0199",
    "kind": "funeral",
    "status": "active",
    "createdAt": "2026-11-25T12:00:00Z"
  }
}
```

## `GET /v1/companies`

Lista somente empresas/CNPJs vinculados ao usuário autenticado. Aceita
`limit` de 1 a 100 (padrão 50) e `cursor` opaco. A ordenação é estável por
`createdAt` e `id`; `nextCursor` é `null` na última página. O CNPJ é mascarado
na listagem.

```json
{"items": [], "nextCursor": null}
```

## `GET /v1/companies/{companyId}/entitlements`

Lista os módulos ativos do catálogo para uma empresa que o usuário pode
acessar. Aceita a mesma paginação por cursor. Módulos `core` aparecem como
`active`; opcionais sem licença aparecem como `inactive`; uma licença ativa
com `validTo` vencido aparece como `expired`. `used` é a ocupação vigente da
cota por módulo no tenant.

```json
{
  "items": [{
    "id": "uuid ou null",
    "tenantId": "uuid",
    "companyId": "uuid",
    "module": "funeral",
    "moduleName": "Funerária",
    "kind": "optional",
    "status": "active",
    "quota": 1,
    "quotaConfigured": true,
    "used": 1,
    "validFrom": "2026-11-01",
    "validTo": null,
    "updatedAt": "2026-11-25T12:00:00Z"
  }],
  "nextCursor": null
}
```

## Erros

Todos os endpoints devolvem o envelope versionado abaixo, inclusive falhas de
autenticação do middleware:

```json
{
  "code": "TENANT_MISMATCH",
  "message": "empresa fora do escopo autenticado",
  "details": {},
  "traceId": "0123456789abcdef0123456789abcdef"
}
```

Os códigos usados nesta fatia são `UNAUTHENTICATED`, `PERMISSION_DENIED`,
`TENANT_MISMATCH`, `VALIDATION_ERROR`, `CONTEXT_UNAVAILABLE` e
`ENTITLEMENT_UNAVAILABLE`.

## Limite explícito da fatia

O ADR 0024 define `company_id` como escopo candidato do token e o middleware
compõe o contexto a partir dele. Portanto, esta fatia lista e valida os
contextos permitidos, mas não altera o token nem cria sessão paralela para
trocar a empresa de requisições futuras. A troca efetiva exige contrato de
sessão/token ainda não definido; até lá, uma nova autenticação deve carregar a
empresa selecionada. Nenhum header ou corpo HTTP substitui essa regra.

