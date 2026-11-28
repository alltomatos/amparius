# Evidências de AUTH-01B — backend do login do tenant (#103/#104)

Validação em worktree `D:\dev\amparius-auth-01b`, branch `feat/auth-01b-backend`,
baseada em `92eb0da` (main, merge de AUTH-01A/#103). Autoridade: `AGENTS.md`,
`docs/adr/0048-auth-emissor-proprio-login.md`,
`docs/contracts/auth-login-v1.md`. Entrega feita em worktree separado do
checkout principal (`D:\dev\amparius`) para não tocar
`skills/nfse-documentacao-tecnica` nem outras mudanças pendentes ali.

## Escopo entregue

- Migração `migrations/0041_identity_auth_credentials.sql`: tabelas
  `identity.credential_token`, `identity.refresh_token`, `identity.login_audit`
  (sem PII — só `tenant_id`, `user_id`, `result`, `at`), RLS por tenant em
  todas, e a função `identity.resolve_login_candidates` (`SECURITY DEFINER`,
  sem `BYPASSRLS` para `amparius_app`) junto com `identity.complete_credential`.
- Migração `migrations/0042_identity_onboarding_owner_company_link.sql`: ver
  "Bugs encontrados e corrigidos" — corrige `identity.create_onboarding` para
  vincular o owner à empresa sede em `identity.user_company`.
- `internal/platform/auth/password.go` (+ teste): hash/verificação argon2id,
  hash-decoy estável para resposta anti-enumeração, sinal de rehash em
  parâmetros desatualizados.
- `internal/platform/auth/jwt.go` (+ teste): emissão/verificação de access
  token Ed25519 de 10 min, rotação de chave com aceitação da anterior por
  `kid`.
- `internal/platform/auth/auth.go`, `middleware.go`: `MFAPolicy` (desligada
  nesta fatia — MFA de plataforma é AUTH-01A/fora de escopo), integração do
  emissor de tenant ao `auth.Service`/`auth.Require*`.
- `internal/modules/identity/{login_service,credentials,login_audit,refresh,
  token,ratelimit,login_http}.go`: `LoginService` (login, refresh rotativo
  com revogação de família em reuso, logout, revoke, bootstrap/complete,
  invite), rotas `POST /v1/auth/{login,refresh,logout,revoke,
  bootstrap/complete,invite}`, bloqueio de 5 tentativas/15 min em
  `identity.app_user`, rate-limit adicional em memória por IP+e-mail
  (20/15 min), resposta uniforme anti-enumeração via hash-decoy.
- `cmd/api/main.go`: `buildTenantAuthentication` monta o emissor só quando a
  chave de assinatura está configurada; sem ela, `/v1/auth/*` e as demais
  rotas autenticadas de tenant respondem 503 (fail-closed), sem impedir o
  boot do processo. `/admin/v1/auth/login` (plataforma) continua 503 por
  falta de OIDC/MFA externo — inalterado nesta fatia.
- `internal/platform/config/config.go`: `AUTH_SIGNING_KEY_SEED/ID` e
  `AUTH_PREVIOUS_SIGNING_KEY_SEED/ID` alimentam o `EnvKeyProvider` de
  desenvolvimento/teste (ver lacuna de cofre/KMS abaixo).
- `internal/modules/identity/login_integration_test.go` (novo): 9 testes de
  integração contra PostgreSQL descartável — RLS por tenant nas 3 tabelas
  novas, ausência de `BYPASSRLS`/`SUPERUSER` em `amparius_app`, resolução de
  candidatos cross-tenant, anti-enumeração + lockout + recovery em 503,
  rotação de refresh com detecção de reuso e revogação de família, replay
  idempotente, logout idempotente respeitando ownership, revoke exigindo
  senha e invalidando todas as sessões, bootstrap via convite single-use,
  ausência de PII em `login_audit`, e latência p95 do login.

## Bugs encontrados e corrigidos durante a validação

Nenhuma migração já aplicada em algum ambiente foi editada; os três
problemas abaixo foram corrigidos em migrações próprias desta fatia (0041,
nova nesta entrega) ou por uma nova migração forward-only (0042).

1. **Goose `StatementBegin`/`StatementEnd` ausente em 3 funções SQL de
   `0041`** — corrigido ainda durante a escrita da migração, antes de
   qualquer aplicação bem-sucedida.
2. **Coluna `tenant_id` ambígua em `identity.complete_credential`
   (SQLSTATE 42702)** — a função declara `RETURNS TABLE (tenant_id uuid,
   user_id uuid)`, e o `plpgsql.variable_conflict = error` padrão trata esses
   nomes como variáveis OUT, colidindo com a referência não qualificada em
   `UPDATE identity.app_user ... WHERE tenant_id = v_token.tenant_id`.
   Corrigido qualificando a cláusula com `identity.app_user.tenant_id`/`.id`.
3. **Defeito pré-existente, anterior a AUTH-01B: `identity.create_onboarding`
   (migração 0009) nunca vinculava o owner recém-criado em
   `identity.user_company`** — essa tabela só existe desde a migração 0032,
   que não retroagiu a função. Resultado: nenhum owner recém-onboarded
   conseguia fazer login/bootstrap na própria empresa
   (`ErrCompanyNotAllowed`). Corrigido via nova migração 0042
   (`CREATE OR REPLACE FUNCTION` com o mesmo nome/assinatura, inserindo o
   vínculo no caminho novo e no replay idempotente, mais backfill para
   onboardings já existentes e `-- +goose Down` simétrico). Como
   consequência, testes de integração pré-existentes que faziam esse vínculo
   manualmente (para contornar o bug) passaram a colidir
   (`user_company_pkey` duplicada) ou a precisar de um `DELETE` extra no
   cleanup; ajustados sem mudar o que cada teste verifica:
   `internal/modules/identity/{auth_integration_test.go,
   context_integration_test.go, company_integration_test.go,
   integration_test.go}`, `internal/modules/party/{production_http_integration_test.go,
   production_legal_entity_http_integration_test.go,
   production_deceased_family_link_http_integration_test.go}`,
   `internal/modules/funeral/production_http_integration_test.go`,
   `internal/modules/finance/production_http_integration_test.go`,
   `internal/modules/catalog/production_http_integration_test.go`,
   `cmd/api/{store_dashboard_integration_test.go,
   homolog_control_plane_ui_test.go}`.

## Ambiente de teste (PostgreSQL descartável)

`scripts/homolog-integration.ps1` (porta 55432, `postgres:16-alpine`, sem
volume) — recriado do zero após os ajustes de fixture acima, com
`goose up` aplicando as migrações 1–42 (célula) e 1–4 (control plane):

```
2026/11/28 00:52:50 OK   0041_identity_auth_credentials.sql (197.29ms)
2026/11/28 00:52:50 OK   0042_identity_onboarding_owner_company_link.sql (14.58ms)
2026/11/28 00:52:50 goose: successfully migrated database to version: 42
```

### `go build` / `go vet` / `gofmt`

```
go build ./...   # sem saída, exit 0
go vet ./...     # sem saída, exit 0
```

`gofmt -l .` sinaliza todo o repositório por causa de `core.autocrlf=true`
(CRLF no checkout Windows, pré-existente e fora do escopo desta fatia).
Confirmado com `gofmt -d` que a diferença é só de fim de linha em todos os
arquivos não tocados por AUTH-01B. Nos arquivos efetivamente editados nesta
fatia, cada um foi comparado ao próprio `gofmt` ignorando CRLF; o único
ajuste real de formatação (alinhamento de campo quebrado por uma edição em
`internal/modules/funeral/production_http_integration_test.go`) foi
aplicado e tem CRLF preservado para não introduzir ruído de diff.

### `go test ./... -count=1 -p 1` (suíte completa, container reconstruído do zero)

```
ok  	github.com/alltomatos/amparius/cmd/api	10.950s
ok  	github.com/alltomatos/amparius/cmd/worker	1.366s
ok  	github.com/alltomatos/amparius/internal/modules/catalog	2.492s
ok  	github.com/alltomatos/amparius/internal/modules/controlplane	0.553s
ok  	github.com/alltomatos/amparius/internal/modules/entitlement	2.941s
ok  	github.com/alltomatos/amparius/internal/modules/finance	3.025s
ok  	github.com/alltomatos/amparius/internal/modules/fiscal	4.252s
ok  	github.com/alltomatos/amparius/internal/modules/fiscal/nfse	2.450s
ok  	github.com/alltomatos/amparius/internal/modules/funeral	4.716s
ok  	github.com/alltomatos/amparius/internal/modules/identity	10.524s
ok  	github.com/alltomatos/amparius/internal/modules/outbox	4.887s
ok  	github.com/alltomatos/amparius/internal/modules/party	14.193s
ok  	github.com/alltomatos/amparius/internal/platform/audit	0.484s
ok  	github.com/alltomatos/amparius/internal/platform/auth	1.848s
ok  	github.com/alltomatos/amparius/internal/platform/config	0.958s
ok  	github.com/alltomatos/amparius/internal/platform/erros	0.856s
ok  	github.com/alltomatos/amparius/internal/platform/http	2.708s
ok  	github.com/alltomatos/amparius/internal/platform/log	0.915s
ok  	github.com/alltomatos/amparius/internal/platform/observability	1.051s
ok  	github.com/alltomatos/amparius/internal/platform/readiness	0.187s
```

Zero regressões em nenhum pacote do repositório.

### Testes de integração do AUTH-01B (`-run TestAUTH01B -v`)

```
=== RUN   TestAUTH01BResolveLoginCandidatesAcrossTenantsAndNoBypassRLS
--- PASS: TestAUTH01BResolveLoginCandidatesAcrossTenantsAndNoBypassRLS (0.28s)
=== RUN   TestAUTH01BLoginAntiEnumerationLockoutAndRecovery
--- PASS: TestAUTH01BLoginAntiEnumerationLockoutAndRecovery (0.91s)
=== RUN   TestAUTH01BRefreshRotationReuseDetectionAndIdempotentReplay
--- PASS: TestAUTH01BRefreshRotationReuseDetectionAndIdempotentReplay (0.63s)
=== RUN   TestAUTH01BLogoutIsIdempotentAndRespectsOwnership
--- PASS: TestAUTH01BLogoutIsIdempotentAndRespectsOwnership (0.44s)
=== RUN   TestAUTH01BRevokeRequiresPasswordAndInvalidatesAllSessions
--- PASS: TestAUTH01BRevokeRequiresPasswordAndInvalidatesAllSessions (0.51s)
=== RUN   TestAUTH01BInviteBootstrapTokenSingleUseExpiryAndUnknownToken
--- PASS: TestAUTH01BInviteBootstrapTokenSingleUseExpiryAndUnknownToken (0.45s)
=== RUN   TestAUTH01BNewTablesEnforceTenantIsolation
--- PASS: TestAUTH01BNewTablesEnforceTenantIsolation (0.41s)
=== RUN   TestAUTH01BLoginAuditNeverPersistsPII
--- PASS: TestAUTH01BLoginAuditNeverPersistsPII (0.39s)
=== RUN   TestAUTH01BLoginLatencyP95
    login_integration_test.go:608: AUTH-01B login latency: n=60 avg=78.351818ms p95=92.3802ms max=165.4281ms
--- PASS: TestAUTH01BLoginLatencyP95 (5.01s)
PASS
ok  	github.com/alltomatos/amparius/internal/modules/identity	9.332s
```

`TestAUTH01BResolveLoginCandidatesAcrossTenantsAndNoBypassRLS` confirma
`current_user = amparius_app` sem `rolsuper`/`rolbypassrls`; a única forma de
contornar RLS é a função `SECURITY DEFINER` de `identity.resolve_login_candidates`.
`TestAUTH01BNewTablesEnforceTenantIsolation` exercita isolamento por tenant
nas 3 tabelas novas de `0041`. `TestAUTH01BLoginAuditNeverPersistsPII` valida
que `identity.login_audit` só tem `id, tenant_id, user_id, result, at`.

### Latência (p95)

Medida com `argon2id` real (não mockado) contra o Postgres descartável,
caminho de sucesso com um único candidato por e-mail, 60 iterações com
`clientIP` sintético distinto por chamada (para não acionar o rate-limiter
de 20/15min, que não é o alvo da medição):

| Métrica | Valor |
| --- | --- |
| Iterações | 60 |
| Média | 78,35 ms |
| p95 | 92,38 ms |
| Máximo | 165,43 ms |

A latência é dominada pelo custo intencional do argon2id (defesa contra
força bruta); não inclui rede/TLS de um ambiente real, nem o rate-limiter
em memória (que é O(1) por chamada). O teste falha se o p95 superar 500 ms
nesse ambiente de teste.

## Correções do parecer do Analista (AUTH-01B aprovado com 3 demandas)

PR #106 (mesma branch `feat/auth-01b-backend`, base `92eb0da`). As 3 demandas
abaixo foram corrigidas sem reabrir escopo; nenhuma migração nova foi
necessária (código Go apenas).

1. **`ACCOUNT_PENDING_AUTH` inalcançável via `Login()`** —
   `identity.complete_credential` (migração 0041) grava `password_hash` e
   `status = 'active'` na mesma transação, então um candidato ainda
   `pending_auth` tem sempre `password_hash` vazio. O laço de verificação de
   senha em `Login()` nunca tinha um match real contra esse candidato e caía
   sempre no hash-decoy, devolvendo `ErrInvalidCredentials` em vez de
   `ErrAccountPendingAuth` — o critério de aceite 1 do ADR 0048 nunca era
   alcançável na prática. Corrigido em
   `internal/modules/identity/login_service.go`: quando há exatamente um
   candidato (`len(candidates) == 1`) e ele está `pending_auth`, `Login()`
   devolve `403 ACCOUNT_PENDING_AUTH` (via `ErrAccountPendingAuth`) antes do
   laço de senha, gravando a auditoria `pending_auth`. Com `N>1` candidatos a
   ambiguidade entre tenants do ADR 0048 §5 continua intacta — um candidato
   `pending_auth` nunca pode ser o resolvido por senha correta nesse caminho,
   então não há regressão na proteção multi-tenant.
   **Enumeração**: devolver um código distinto para `pending_auth` revela que
   a conta existe mas não concluiu o bootstrap. Isso **já é um risco residual
   aceito e documentado** pelo desenho original, não uma nova decisão desta
   correção: `docs/contracts/auth-login-v1.md` (linha 78) já descreve
   `ACCOUNT_PENDING_AUTH` como "usuário existe mas não concluiu bootstrap
   (ADR 0048 §12)", e `docs/adr/0048-auth-emissor-proprio-login.md` (linhas
   195-202) registra esse enumeração como "risco residual registrado, não
   eliminado por desenho"; o critério de aceite 1 do ADR (linhas 424-430)
   exige explicitamente que contas `pending_auth` "sejam recusadas com código
   distinto". Esta correção apenas restaura o comportamento já especificado
   — não amplia a superfície de enumeração além do que o ADR/contrato já
   aceitam. Teste de integração novo:
   `TestAUTH01BLoginPendingAuthIsReachableViaLogin`
   (`internal/modules/identity/login_integration_test.go`), cobrindo o
   caminho via `Login()` e a auditoria `pending_auth`, mais a confirmação de
   que o mesmo e-mail autentica normalmente após `CompleteBootstrap`.

2. **`clientIPFrom` confiava em `X-Forwarded-For` sem allowlist** — o
   cabeçalho é informado pelo próprio chamador HTTP; sem uma lista de
   proxies confiáveis, qualquer cliente podia forjar o IP usado no
   rate-limit de login (20/15min por IP+e-mail) só enviando o cabeçalho, e
   assim escapar do limite. Corrigido com o novo tipo `TrustedProxies`
   (`internal/modules/identity/trusted_proxies.go`): só confia no primeiro
   salto de `X-Forwarded-For` quando o peer TCP imediato (`RemoteAddr`) está
   em uma allowlist de CIDRs; vazio/nil (padrão) ignora o cabeçalho sempre e
   usa `RemoteAddr`. Nova configuração opcional `AUTH_TRUSTED_PROXY_CIDRS`
   (lista separada por vírgulas, validada com `net.ParseCIDR` em
   `config.validate()` — CIDR inválido falha o boot, nunca falha em tempo de
   requisição) em `internal/platform/config/config.go`; sem a variável, o
   comportamento é idêntico ao anterior à correção (ignora o cabeçalho).
   **Limitação documentada**: esta fatia não tem nenhum proxy reverso real
   configurado em produção — `AUTH_TRUSTED_PROXY_CIDRS` fica vazio por
   padrão até que exista um load balancer/proxy TLS-terminator documentado
   na frente da API; até então, o rate-limit de login sempre usa o IP do
   peer TCP direto, o que é seguro mas pode agrupar vários usuários atrás de
   um único NAT/proxy não configurado sob o mesmo IP. Testes unitários
   novos: `TestClientIPFromIgnoresForwardedHeaderWithoutTrustedProxy`,
   `TestClientIPFromIgnoresForwardedHeaderFromUntrustedPeer`,
   `TestClientIPFromTrustsForwardedHeaderFromAllowlistedProxy`,
   `TestClientIPFromFallsBackToRemoteAddrWithoutHeader`,
   `TestNewTrustedProxiesRejectsInvalidCIDR`
   (`internal/modules/identity/trusted_proxies_test.go`, `package identity`
   para acessar `clientIPFrom`/`TrustedProxies` não exportados).

3. **Boot parcial quando a chave de assinatura existe mas RBAC/autenticador
   falham** — em `buildTenantAuthentication` (`cmd/api/main.go`), com a
   chave de assinatura presente mas `platformauth.DefaultRolePolicy()` ou
   `platformauth.NewService(...)` falhando, o código anterior devolvia
   `loginService` não-nil mesmo sem `authenticator`: login/refresh/bootstrap
   continuavam funcionando enquanto logout/revoke/invite ficavam 503 por
   `Authenticator` nil — um estado parcial inconsistente. Corrigido para
   devolver `nil, nil` nos dois ramos de falha, logando um alerta claro (sem
   segredo) antes de cada retorno. Como consequência, nenhuma
   `PublicRoute`/`AuthenticatedRoute` de `/v1/auth/*` é registrada nesse
   estado; adicionado um fallback `s.mux.HandleFunc("/v1/auth/",
   s.handleAuthUnavailable)` em `internal/platform/http/server.go`
   (`AUTHENTICATION_UNAVAILABLE`, 503) para que todo `/v1/auth/*`
   inteiramente indisponível responda 503 — nunca parcialmente de pé — em
   qualquer um dos dois casos (sem chave de assinatura alguma, ou com chave
   presente e RBAC/autenticador quebrado). Rotas exatas com método (ex.:
   `POST /v1/auth/login`) continuam tendo precedência sobre o fallback
   quando registradas e funcionando (regra de especificidade do
   `net/http.ServeMux` do Go 1.22+). Cobertura: gate geral de
   build/vet/test; sem teste dedicado adicional, já que simular falha de
   `platformauth.NewService` exigiria injetar uma falha interna da
   biblioteca de auth fora do escopo desta correção.

### `go test ./... -count=1 -p 1` com as 3 correções (container reconstruído do zero)

```
ok  	github.com/alltomatos/amparius/cmd/api	10.826s
ok  	github.com/alltomatos/amparius/cmd/worker	1.369s
ok  	github.com/alltomatos/amparius/internal/modules/catalog	2.878s
ok  	github.com/alltomatos/amparius/internal/modules/controlplane	0.622s
ok  	github.com/alltomatos/amparius/internal/modules/entitlement	3.053s
ok  	github.com/alltomatos/amparius/internal/modules/finance	2.778s
ok  	github.com/alltomatos/amparius/internal/modules/fiscal	4.217s
ok  	github.com/alltomatos/amparius/internal/modules/fiscal/nfse	2.688s
ok  	github.com/alltomatos/amparius/internal/modules/funeral	5.950s
ok  	github.com/alltomatos/amparius/internal/modules/identity	12.385s
ok  	github.com/alltomatos/amparius/internal/modules/outbox	5.006s
ok  	github.com/alltomatos/amparius/internal/modules/party	12.825s
ok  	github.com/alltomatos/amparius/internal/platform/audit	0.570s
ok  	github.com/alltomatos/amparius/internal/platform/auth	1.947s
ok  	github.com/alltomatos/amparius/internal/platform/config	0.980s
ok  	github.com/alltomatos/amparius/internal/platform/erros	0.961s
ok  	github.com/alltomatos/amparius/internal/platform/http	2.928s
ok  	github.com/alltomatos/amparius/internal/platform/log	1.051s
ok  	github.com/alltomatos/amparius/internal/platform/observability	1.316s
ok  	github.com/alltomatos/amparius/internal/platform/readiness	0.265s
```

Zero regressões. `go build ./...` e `go vet ./...` seguem limpos (sem saída,
exit 0).

### Testes novos desta correção (`-v`, contra o mesmo Postgres descartável)

```
=== RUN   TestClientIPFromIgnoresForwardedHeaderWithoutTrustedProxy
--- PASS: TestClientIPFromIgnoresForwardedHeaderWithoutTrustedProxy (0.00s)
=== RUN   TestClientIPFromIgnoresForwardedHeaderFromUntrustedPeer
--- PASS: TestClientIPFromIgnoresForwardedHeaderFromUntrustedPeer (0.00s)
=== RUN   TestClientIPFromTrustsForwardedHeaderFromAllowlistedProxy
--- PASS: TestClientIPFromTrustsForwardedHeaderFromAllowlistedProxy (0.00s)
=== RUN   TestClientIPFromFallsBackToRemoteAddrWithoutHeader
--- PASS: TestClientIPFromFallsBackToRemoteAddrWithoutHeader (0.00s)
=== RUN   TestNewTrustedProxiesRejectsInvalidCIDR
--- PASS: TestNewTrustedProxiesRejectsInvalidCIDR (0.00s)
=== RUN   TestAUTH01BLoginPendingAuthIsReachableViaLogin
--- PASS: TestAUTH01BLoginPendingAuthIsReachableViaLogin (0.46s)
PASS
ok  	github.com/alltomatos/amparius/internal/modules/identity	0.743s
```

## Desvios do ADR/contrato nesta fatia

- **MFA/OIDC de plataforma**: fora de escopo (AUTH-01A). `MFAPolicyDisabled`
  é usado ao montar o autenticador de tenant; `/admin/v1/auth/login`
  continua 503.
- **Rate-limit e idempotência em memória, não Redis**: `go.mod` não tem
  cliente Redis nesta fatia; `rateLimiter` e `idempotencyCache`
  (`internal/modules/identity/ratelimit.go`) são por processo, não
  compartilhados entre réplicas e não sobrevivem a reinício. O contador
  autoritativo contra força bruta continua sendo
  `failed_attempts`/`locked_until` em `identity.app_user` (nunca falha
  aberto).
- **Funções `SECURITY DEFINER` e `GRANT`s além dos exemplos literais do
  ADR**: `identity.complete_credential` foi adicionada (além de
  `resolve_login_candidates`) para cobrir bootstrap/recovery sem expor
  `password_hash`/colunas sensíveis a `amparius_app` fora da função.
- **`tenantHint` no corpo do login**: aceito pelo contrato mas não usado
  nesta implementação (resolução por candidatos cobre o caso sem o hint);
  não é um no-op perigoso porque não afeta o resultado, só deixa de ser uma
  otimização.
- **Chave de assinatura via variável de ambiente (`AUTH_SIGNING_KEY_SEED`),
  não cofre/KMS**: lacuna documentada no ADR 0048 (produção depende de um
  cofre/KMS real, fora desta fatia); em testes e nesta validação, a chave
  é injetada via `EnvKeyProvider`, nunca commitada.
- **`credential_token not found`** mapeado para `400 VALIDATION_ERROR` em
  vez de um código dedicado — tratado como entrada inválida, consistente
  com a resposta uniforme anti-enumeração.
- **Hardening extra no logout**: a verificação de ownership do refresh
  token antes de invalidar vai além do texto literal do contrato
  (que não detalha esse caso), para impedir que um token de outro usuário
  seja invalidado por engano.
- **Bugs de migração e o defeito pré-existente em `identity.create_onboarding`**:
  ver seção "Bugs encontrados e corrigidos durante a validação" acima.

## Fora de escopo (confirmado intacto)

- Migração 0036 (#82): não aplicada nesta fatia.
- Telas (D) e matriz de papéis (C): não tocadas.
- `/admin/v1/auth/login`: continua 503, sem alteração de contrato.
