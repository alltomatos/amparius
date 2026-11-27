# ADR 0040 — Escopo de plataforma e leitura cross-tenant no manager.amparius.com

- **Status:** aceito para implementação sequencial
- **Data:** 2026-11-26
- **Escopo:** control plane administrativo, API administrativa e autorização

## Contexto

Os mockups administrativos (`03-painel-saas-dashboard`, `05`–`10`) mostram um
console que agrega, em uma única tela, tenants ativos, licenças em uso e a
vencer, saúde por módulo, cota de IA, provedores/modelos, consumo e auditoria.

O ADR 0045 propõe `manager.amparius.com` como superfície da verdade para
tenants, empresas, licenças, quotas e auditoria. O que não existe é o contrato
de leitura que essa superfície precisa:

- O ADR 0038 e o código atual só têm leitura escopada por tenant, com RLS;
  `/v1` não lê cross-tenant, por decisão explícita.
- O ADR 0024 define papéis de tenant (`owner`, `admin`, `manager`, `operator`,
  `auditor`) e nenhum papel de plataforma.

Sem essa definição, a UI só consegue mostrar uma empresa por vez e não pode
exibir os indicadores do mockup sem inventar números — o que viola a regra de
não estimar.

## Decisões

1. **Papel de plataforma.** `platform_admin` é distinto dos papéis de tenant,
   autenticado por OIDC com MFA obrigatório e emitido apenas para o domínio
   `manager.amparius.com`. Não herda permissão de tenant nem concede acesso a
   `/v1`.
2. **Superfície de API separada.** O console administrativo usa `/admin/v1/*`,
   servido pelo mesmo binário `cmd/api` com um conjunto próprio de rotas.
   `/v1/*` permanece estritamente tenant-scoped.
3. **Leitura cross-tenant por views de leitura** no schema `reporting`
   (permitidas pelo `AGENTS.md` §4), nunca por `JOIN` em tabelas de módulo nem
   por desligar RLS nos caminhos de tenant.
4. **Escrita continua nos serviços públicos do módulo**, com contexto de tenant
   explícito (ex.: entitlement), preservando RLS, `Idempotency-Key`, `version`
   e auditoria. O evento registra `origin=platform` e o ator.
5. **Auditoria obrigatória.** Toda resposta de `/admin/v1` é auditada com ator,
   tenant alvo, ação e resultado. Agregações não expõem PII nem payload fiscal;
   CNPJ continua mascarado.
6. **Falha fechada.** Sem `platform_admin` válido, `/admin/v1` responde
   `401`/`403`. Se a view de `reporting` estiver ausente ou defasada, responde
   `503` com código estruturado — nunca um número estimado.
7. **Escopo explícito na UI.** O console declara qual escopo está ativo:
   plataforma (`/admin/v1`) ou tenant (`/v1`). A tela nunca mistura os dois sem
   indicar a origem do dado.

## Consequências e limites

- Nova fatia de backend: views de `reporting`, rotas `/admin/v1`, papel e
  claims de plataforma.
- O frontend pode entregar o shell e as telas já suportadas por `/v1` antes do
  agregado administrativo.
- Migração forward-only para views e grants; nenhum `BYPASSRLS`.
- O ADR 0024 ganha um papel de plataforma; claims e sessão de `platform_admin`
  dependem do contrato de OIDC/MFA ainda pendente (bloqueio já registrado no
  ADR 0038).

## Fatias

- `FE-01` — shell do control plane alinhado ao mockup, com Visão geral usando
  apenas dados disponíveis e estado explícito para o que depende de `/admin/v1`.
- `ADM-01` — views de `reporting` e `GET /admin/v1/overview`.
- `ADM-02` — tenants/empresas e saúde dos módulos.
- `ADM-03` — IA: provedores, allowlist/fallback, cota em R$ e consumo.

## Estado de implementação (ADM-02A, issue #80)

Entregue em 2026-11-27, contrato em
`docs/contracts/platform-admin-licenses-v1.md`:

- Migration control `0004_reporting_platform_views.sql`: schema `reporting` com
  `platform_license_pool`, `platform_license_assignment`,
  `platform_entitlement_event` e `platform_tenant_summary`; só ids opacos,
  contagens e estados; `GRANT SELECT` ao `amparius_control_app`; sem
  `BYPASSRLS` (as views pertencem ao dono das tabelas, que não está sujeito a
  RLS porque não há `FORCE`).
- `auth.RequirePlatform` e `PlatformService`: bearer + MFA + `platform_admin`,
  política de papéis disjunta da de tenant; gate ausente responde `503`.
- `PlatformReader` (leitura via `WithSystemTx` somente sobre as views) e
  `AdminHandler` (`/admin/v1/tenants`, `.../licenses`, `.../entitlement-events`,
  grant, revoke e quota por tenant explícito).
- Escrita reaproveita `entitlement.Service` com `db.WithTenant(tenantAlvo)`,
  `Idempotency-Key` obrigatória, `version` e o ator de plataforma.

Decisões e desvios:

- A origem gravada é `admin` (valor já existente no catálogo de
  `entitlement_event.origin`), e não `platform`; o escopo de plataforma fica
  identificado pelo ator e pela rota. Criar um terceiro valor exigiria migração
  de constraint sem ganho de auditoria.
- A auditoria de toda resposta (decisão 5) é registro estruturado
  `platform_audit` (ator, tenant alvo, ação, status, `trace_id`). Para escritas,
  o evento durável já está em `entitlement_event`/`quota_event`. Persistência
  durável das **leituras** de plataforma continua pendente.
- Um tenant só aparece nas views depois de ter cota, licença ou assentos; tenant
  sem nenhum registro responde `404 TENANT_NOT_FOUND`, mas ainda pode receber
  quota/grant (que o cria).

Pendências (a #80 fica **parcial** por causa delas):

- Diretório de tenants e empresas (nome, CNPJ mascarado, status) exige uma view
  de reporting no banco da célula (`identity`); o control plane só conhece ids.
  Próxima fatia (`ADM-02B`).
- Adaptador OIDC/MFA de plataforma (emissor, audience `manager`, nonce): sem ele
  `PlatformAuthenticator` não é montado em `cmd/api` e `/admin/v1` responde
  `503`. Continua pendência de produção (ADR 0038).
- Saúde por módulo, vencimento de licenças e IA (`ADM-01`, `ADM-03`).

## Rejeitado

- Colocar o superadmin em `app.amparius.com`, misturado ao acesso de cliente.
- Desligar RLS globalmente para o administrador.
- Agregar no navegador com N requisições por empresa.
- Preencher cota, consumo ou contagem com valor estimado quando o endpoint não
  existir.
