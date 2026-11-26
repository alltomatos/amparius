# ADR 0041 — Contrato autenticado de cadastro de pessoa física `/v1/parties`

Status: aceito para implementação sequencial · 26/11/2026. Backend
implementado; bootstrap de autenticação permanece bloqueado (mesmo gate do
ADR 0038/CP-06).

## Contexto

A issue #72 (E2E-01B1, mockup Loja 02, PRD RF-003) pede cadastro operacional
de pessoa física e busca, hoje só acessíveis pelas rotas dev-only
`/_dev/parties` (cabeçalho de tenant, sem autenticação real). O DER §1 define
`party.party{id, tenant_id, kind, name, document, status}` sem `company_id`:
Party é um cadastro **tenant-scoped**, não por empresa. O ADR 0024 já define
o principal autenticado (tenant, empresa, papel) e o middleware
`auth.Require`; o repositório `party.Repository` (criado nas fatias #9–#13)
já implementa `Create`/`Search` com `WithTenantTx` e RLS. Faltava apenas a
superfície HTTP de produção.

## Decisões

1. Nova rota versionada, exclusivamente para pessoa física (`kind=person`):
   - `POST /v1/parties` — cria, exige `party:write`. Resposta `201` traz o
     cadastro no corpo (campo `id`); **sem** cabeçalho `Location`, porque
     esta fatia não expõe `GET /v1/parties/{id}`.
   - `GET /v1/parties` — busca por `q` (nome civil, nome social ou
     documento; **obrigatório**, válido após trim e dentro do limite de
     tamanho existente) e `limit` (1–100, padrão 25), exige `party:read`.
     O filtro `kind=person` é aplicado no repositório: `Repository.SearchPersons`
     delega ao núcleo `search` com `kind=person` no parâmetro `$4`, dentro do
     `WHERE`, **antes** de `ORDER BY`/`LIMIT` — não em memória depois do corte —
     para que uma pessoa jurídica do mesmo tenant e termo nunca ocupe a posição
     de uma pessoa física dentro do `limit`. `Repository.Search` (sem filtro de
     kind, `$4` vazio) continua existindo só para as rotas dev-only.
   Ambas usam `httpserver.AuthenticatedRoute`, logo ficam fail-closed (503
   `AUTHENTICATION_UNAVAILABLE`) sem `auth.Gate` configurado — mesmo
   comportamento do CP-06, não "corrigido" nesta fatia.
2. `PermissionPartyRead`/`PermissionPartyWrite` já existiam em
   `internal/platform/auth/auth.go` e já estavam concedidas a
   owner/admin/manager/operator (+auditor para leitura) em
   `DefaultRolePolicy()`; nenhuma mudança de RBAC foi necessária.
3. Tenant e empresa do cadastro vêm exclusivamente do contexto HTTP composto
   pelo principal autenticado (`db.TenantFromContext`/`db.CompanyFromContext`
   comparados a `principal.TenantID`/`principal.CompanyID`); nenhum campo do
   corpo ou cabeçalho (`tenant_id`, `company_id`, `kind`, `status`) é aceito.
   Requisição sem credencial válida falha com `401 AUTHENTICATION_REQUIRED`;
   usuário sem a permissão do verbo, com `403 PERMISSION_DENIED` (checado pelo
   próprio `auth.Require` antes do handler); empresa sem vínculo
   `identity.user_company` com o usuário, com `403 SCOPE_FORBIDDEN`. MFA desativado ou sem confirmação
   pelo verificador retorna `403 MFA_REQUIRED`. São
   os mesmos códigos que `auth.Require` já documenta em
   `docs/contracts/auth-http-v1.md`, nunca códigos inventados por esta fatia,
   e a falha não é silenciosamente ignorada. Essa checagem redundante do
   handler (`scope()`) nunca é alcançada por tráfego real, porque
   `auth.Require` já garante tenant/empresa consistentes antes de chamar o
   handler; existe só para que uma eventual regressão do middleware produza
   um envelope já documentado em vez de um 500.
4. Como Party é tenant-scoped (sem `company_id` no DER), o contrato **não**
   introduz isolamento por empresa dentro do mesmo tenant: qualquer usuário
   autorizado por papel em qualquer empresa do tenant lê/cria o mesmo
   cadastro de pessoas físicas do tenant. Isso é fidelidade ao modelo de
   dados existente, não uma lacuna desta fatia.
5. Envelope de erro e convenção de campo seguem
   `docs/contracts/control-plane-context-v1.md`: `{code, message, details,
   traceId}`, JSON de resposta em camelCase (`civilName`, `socialName`,
   `displayName`), novo código `PARTY_UNAVAILABLE` seguindo o padrão
   `<DOMÍNIO>_UNAVAILABLE`.
6. Nenhuma migration nova: as tabelas e grants de `party.party`/`party.person`
   já existem (0009–0013); esta fatia só adiciona superfície HTTP sobre o
   repositório já testado. Nenhuma chave de idempotência foi introduzida —
   criação de pessoa física não é operação fiscal nem financeira
   (AGENTS.md regra de idempotência) e o schema atual não tem unicidade por
   pessoa.

## Consequências e limites

- As rotas `/_dev/parties` continuam ativas e inalteradas em modo
  desenvolvimento; `/v1/parties` é a única superfície de produção e não as
  substitui nesta fatia.
- `cmd/api` configura o pool do banco da célula e registra
  `/v1/parties` sempre que o processo não estiver em modo dev, independente
  de `AMPA_CONTROL_POSTGRES_DSN`: cadastro de pessoa física é núcleo
  (identity/party), não depende de entitlement/control plane. As rotas CP-06
  continuam condicionadas a `AMPA_CONTROL_POSTGRES_DSN` e se somam às mesmas
  `AuthenticatedRoutes`.
- Com dev e control plane desabilitados, o pool da célula começa sem Ping
  e abre conexões sob demanda. `/healthz` responde `200` e `/readyz`
  responde `503` durante falhas de dependência. Com o control plane
  configurado, a checagem de conexão no bootstrap é mantida.
- `httpserver.Options.Authenticator` continua sem valor neste bootstrap; a
  decisão de provedor OIDC/MFA real permanece a mesma pendência registrada no
  ADR 0038. Até essa decisão, `/v1/parties` responde `503
  AUTHENTICATION_UNAVAILABLE` para qualquer tráfego, igual a CP-06.
- Esta fatia cobre apenas criação e busca de pessoa física. Atualização,
  remoção, PJ e vínculos (endereço, contato, representante) do RF-003 ficam
  para fatias futuras e não foram implementados aqui.

Verificação reproduzível:

```text
go build ./...
go vet ./...
go test ./cmd/api ./internal/platform/auth ./internal/platform/http -count=1
go test ./internal/modules/party/... -run 'TestProduction' -count=1 -v
AMPA_TEST_POSTGRES_DSN=... AMPA_TEST_POSTGRES_ADMIN_DSN=... \
  go test -p 1 ./internal/modules/party/... -run 'TestProductionPartiesIntegration' -count=1 -v
```

(Os dois últimos comandos exigem `AMPA_TEST_POSTGRES_DSN`/
`AMPA_TEST_POSTGRES_ADMIN_DSN` com um papel `NOSUPERUSER/NOBYPASSRLS` para os
subtestes de integração; sem essas variáveis, esses subtestes são pulados
explicitamente, nunca simulados. `TestCP06SemBootstrapOIDCMFAFalhaFechado` e
`TestBuildHandlerSobreviveCelulaIndisponivelSemDevESemControlPlane`, em
`cmd/api`, não dependem de Postgres real.)

Validação da issue #72 em 26/11/2026. PostgreSQL 16
temporário, Goose v3.24.3 até a migration 0035, papel `amparius_app` com
`rolsuper=f` e `rolbypassrls=f`. `go test -p 1 ./internal/modules/party -run
'^TestProductionPartiesIntegration' -count=1 -v` retornou 8 PASS em 3,544 s, e
as contagens `identity.tenant` e `party.party` ficaram em 0 após o cleanup. A
suíte de integração usa um verificador OIDC de teste que mapeia tokens
sintéticos a identidades; isso não é o provedor de produção (ADR 0038 segue
pendente). `go test -p 1 ./...` sem DSNs de integração, `go vet ./...`,
`go build ./...` e `git diff --check` também passaram. Os testes provam o
contrato no handler e no repositório real; não provam UI, suítes legadas nem
emissão de claims de produção.
