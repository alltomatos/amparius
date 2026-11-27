# Contrato — Painel SaaS: tenants e licenças (`/admin/v1`) v1

- **Fatia:** ADM-02A (issue #80) — **parcial**, ver "Limites".
- **ADR:** 0040 (escopo de plataforma), 0007 (entitlement), 0045 (superfícies frontend).
- **Superfície:** `manager.amparius.com`; nunca `/v1` nem `app.amparius.com`.

## Autenticação e autorização

- Bearer OIDC de plataforma com MFA e papel `platform_admin`. Permissões
  `platform:read` (GET) e `platform:write` (PUT/POST). Nenhum papel de tenant
  recebe `platform:*`, e `platform_admin` não recebe permissão de `/v1`.
- Sem verificador configurado: `503 AUTHENTICATION_UNAVAILABLE`. Sem bearer ou
  token inválido: `401`. MFA ausente, escopo ou papel insuficiente: `403`.
- O tenant alvo é sempre parâmetro de caminho; nunca vem de claim de sessão.
- Toda resposta é `Cache-Control: no-store` e gera o log estruturado
  `platform_audit` (`actor_id`, `target_tenant_id`, `action`, `status`,
  `result`, `trace_id`). Sem PII.

## Leitura (views `reporting.platform_*`)

| Rota | Permissão | Resposta |
| --- | --- | --- |
| `GET /admin/v1/tenants?limit&cursor` | read | `{scope, items[], nextCursor}`; item: `tenantId`, `modulesWithQuota`, `quotaTotal`, `assignmentsTotal`, `assignmentsActive`, `userSeats`, `userUsed`, `lastEventAt` |
| `GET /admin/v1/tenants/{tenantId}/licenses?limit&cursor` | read | `{scope, tenant, pools[], assignments[], nextCursor}`; pool: `module`, `quota`, `used`, `version`; licença: `id`, `companyId`, `module`, `status`, `validFrom`, `validTo`, `version` |
| `GET /admin/v1/tenants/{tenantId}/entitlement-events?limit&cursor` | read | `{scope, items[], nextCursor}`, mais recente primeiro; evento: `actorId`, `action`, `origin`, `at` |

`limit` 1–100 (padrão 50); cursor opaco (keyset por UUID). Erros: `400
VALIDATION_ERROR` (cursor/limite/tenantId), `404 TENANT_NOT_FOUND`, `503
PLATFORM_REPORT_UNAVAILABLE` quando a view está ausente, sem permissão ou
defasada (nunca devolve número estimado).

## Escrita (serviço de entitlement com tenant explícito)

Todas exigem `Idempotency-Key` (1–200 caracteres) e gravam `origin=admin` com o
ator de plataforma em `entitlement_event`/`quota_event`.

| Rota | Corpo | Observações |
| --- | --- | --- |
| `PUT /admin/v1/tenants/{t}/companies/{c}/entitlements/{module}` | `{validFrom?, validTo?, version?}` | concede/reativa; `409 LICENSE_POOL_EXHAUSTED` sem cota; `409 STALE_VERSION` |
| `POST /admin/v1/tenants/{t}/companies/{c}/entitlements/{module}/revoke` | `{version}` (obrigatória) | `409 STALE_VERSION` com versão obsoleta |
| `PUT /admin/v1/tenants/{t}/modules/{module}/quota` | `{quota, version}` | recusa cota abaixo do uso |

Replay com a mesma chave devolve o mesmo resultado sem novo evento; chave
reutilizada com outro pedido é `409 IDEMPOTENCY_CONFLICT`.

## Evidência

- Unitários: `internal/platform/auth/platform_test.go` (bearer, MFA, papel de
  tenant recusado, gate nil → 503) e
  `internal/modules/controlplane/admin_http_test.go` (401/403/503/404, tenant,
  ator e origem repassados, validação, auditoria).
- Integração PostgreSQL descartável (`postgres:16-alpine`, migrations control
  0001–0004, papel `amparius_control_app` sem `SUPERUSER`/`BYPASSRLS`):
  `admin_integration_test.go` com dois tenants — cota, grant, replay
  idempotente, cota esgotada (409), revoke obsoleto (409) e correto, listagem
  cross-tenant paginada, ausência de vazamento entre tenants, eventos com ator
  e `origin=admin`, escrita em view recusada e ausência de colunas sensíveis.
- Rodar: `AMPA_CONTROL_POSTGRES_DSN=... go test ./internal/modules/controlplane
  -run 'Admin|Platform'`. Sem o DSN os testes de integração são `SKIP`.
- Observação: o cleanup dos testes de integração ignora erro de `DELETE`
  porque o papel de aplicação não tem `DELETE`; use banco descartável.

## Limites (pendências)

- Diretório de tenants/empresas (nome, CNPJ mascarado) depende de view no
  banco da célula: `ADM-02B`.
- Adaptador OIDC/MFA de plataforma real não existe; `cmd/api` não monta
  `PlatformAuthenticator`, então as rotas respondem `503` até lá.
- Auditoria durável de leituras de plataforma: hoje só log estruturado.
- Saúde por módulo, vencimento e IA ficam em `ADM-01`/`ADM-03`.
