# Especificação Frontend — Control Plane e Cliente

- **Status:** rascunho v0.1
- **Data:** 2026-11-25
- **Idioma:** Português (PT-BR)
- **Escopo:** contrato de produto e UI para o control plane administrativo (`CP-*`) e o cliente de loja/offline (`CL-*`).
- **Não escopo:** implementação de código, criação de ADR, migrações, contratos de fiscalização, alteração em documentos pré-existentes.

Esta especificação consolida as decisões já tomadas em [AGENTS.md](../../AGENTS.md), [docs/ENGINEERING.md](../ENGINEERING.md), [ADR 0001](../adr/0001-stack.md), [ADR 0007](../adr/0007-entitlement-licenca.md), [ADR 0024](../adr/0024-autenticacao-oidc-mfa-rbac-escopo.md), [PRD](../prd/PRD.md), [DER](../data/DER.md), [entitlement-ux.md](entitlement-ux.md), [AI-ASSISTANT.md](AI-ASSISTANT.md), [MOCKUPS.md](MOCKUPS.md), [ia-control-plane.md](../userguide/ia-control-plane.md), [funeral-atendimento.md](../userguide/funeral-atendimento.md) e [client/README.md](../../client/README.md). A validação correspondente está em [frontend-acceptance.md](../qa/frontend-acceptance.md).

## 1. Objetivo

Definir, antes de qualquer implementação, um contrato estável de telas, estados, vocabulário, acessibilidade, segurança e observabilidade para duas superfícies: o control plane usado por administradores do SaaS e o cliente usado pela operação da loja, inclusive com internet instável. O documento deve permitir que design, frontend, backend, QA e segurança trabalhem com a mesma lista de telas, os mesmos estados e as mesmas regras de falha, sem reabrir decisões arquiteturais já registradas.

## 2. Premissas e fontes de verdade

1. **Amparius é a fonte de verdade do produto.** Documentos e specs próprios prevalecem sobre qualquer referência externa.
2. **Clean-room Amparius.** Não copiar componentes, CSS, telas, nomes internos ou estrutura de diretórios do aplicativo TypeScript citado em [ADR 0001](../adr/0001-stack.md). É permitido usar apenas mockups e specs gerados dentro de `D:\dev\amparius`.
3. **Clean-room allcode-go.** O projeto em `D:\dev\allcode-go` serve apenas como referência observável de contrato HTTP, comportamento e golden files. Não copiar arquivos, snippets, nomes internos, estrutura `packages/*` ou dependências `Effect`, `Drizzle`, `Solid`, `Bun`.
4. **Contrato executável da referência.** A verdade final do allcode é `GET /openapi.json` em execução mais capturas SSE; documentos estáticos são pistas, não autoridade absoluta.
5. **Nenhuma mudança estrutural nesta rodada.** Este documento não cria ADR. Se uma decisão mudar fronteira de módulo, contrato, modelo de dados ou topologia, um ADR deve ser aberto antes do código.
6. **Fiscal não se estima.** Alíquota, CST, cClassTrib, NCM, CFOP, código de serviço e vigência vêm de tabela oficial datada. Se a regra não estiver na tabela, a operação falha. A UI nunca sugere “valor aproximado”.
7. **Licença é administrativa.** Ela controla quota, módulo, empresa, validade e auditoria. Ela não calcula nem autoriza regra tributária.

## 3. Princípios de produto

| Princípio | Aplicação |
| --- | --- |
| Tenant é contexto, não parâmetro de confiança | `tenant_id` vem do token autenticado. Header `X-Tenant-Id`, se existir, é apenas dica de UI e nunca autoridade. |
| Empresa é âncora de licença | `COMPANY` concentra CNPJ/entitlement; `ESTABLISHMENT` herda o entitlement e não possui licença própria. |
| Falha fechada | Role desconhecida, empresa fora do tenant, módulo sem entitlement ou token inválido negam acesso. |
| Offline não é exceção | Toda tela `CL-*` declara o que funciona offline e o que entra em contingência. |
| Auditoria é produto | Toda concessão, revogação, mudança de quota, uso de IA e conflito de sincronização gera evidência navegável. |
|Estado explícito | Nenhuma tela fica “em branco”. Sempre há `idle`, `loading`, `empty`, `error` ou `success`. |
| Cor nunca é único sinal | Status usa cor + texto + ícone/formato. |
| Mobile-first | Layout deve ser utilizável em 360 px sem scroll horizontal antes de ganhar densidade desktop. |
| Revalidação no servidor | O envelope de IA e o front enviam contexto; o servidor revalida permissões e dados. |

## 4. Arquitetura de informação

Rotas abaixo são rotas do produto, não endpoints da API.

### 4.1 Control plane (`CP`)

| ID | Rota | Nome | Objetivo | Papéis |
| --- | --- | --- | --- | --- |
| CP-01 | `/cp/login` | Entrar | Autenticar com OIDC e MFA quando aplicável | todos |
| CP-02 | `/cp/tenants` | Selecionar contexto | Escolher tenant e empresa ativos | `owner`, `admin`, `manager` |
| CP-03 | `/cp/dashboard` | Visão geral | Mostrar saúde de módulos, licenças por vencer, quota de IA e auditoria recente | `owner`, `admin`, `manager`, `auditor` |
| CP-04 | `/cp/companies` | Empresas e CNPJ | Listar e criar `COMPANY` | `owner`, `admin` |
| CP-05 | `/cp/companies/:companyId/entitlements` | Entitlements | Listar módulos, status, quota e validade | `owner`, `admin`, `auditor` |
| CP-06 | `/cp/companies/:companyId/entitlements/:module` | Detalhe da licença | Ver histórico, conceder, revogar e ajustar quota | `owner`, `admin` |
| CP-07 | `/cp/ai` | IA e quota | Gerenciar providers, modelos, fallback, quota e uso | `owner`, `admin`, `auditor` |
| CP-08 | `/cp/users` | Usuários e RBAC | Convidar, atribuir papéis e vínculo empresa | `owner`, `admin` |
| CP-09 | `/cp/audit` | Auditoria | Consultar eventos com filtro por ator, ação, entidade e período | `owner`, `admin`, `auditor` |

### 4.2 Cliente de loja (`CL`)

| ID | Rota | Nome | Objetivo | Modo |
| --- | --- | --- | --- | --- |
| CL-01 | `/cl/day` | Painel do dia | Mostrar atendimentos, ordens e pendências priorizadas | online/offline |
| CL-02 | `/cl/attendance` | Atendimento funeral | Registrar contrato/parte e iniciar atendimento | online/offline com contingência |
| CL-03 | `/cl/service-orders` | Ordens de serviço | Executar, pausar e concluir ordens | online/offline com contingência |
| CL-04 | `/cl/offline` | Offline e contingência | Explicar o que está disponível e o que aguarda sincronização | offline |
| CL-05 | `/cl/sync` | Sincronização e conflitos | Mostrar fila, sequência, conflitos e revisão | online |

## 5. Modelo visível ao frontend

Estes são projeções de UI, não tabelas de banco. Campos sensíveis ou secretos nunca aparecem nesta camada.

| Entidade de UI | Campos principais | Observação |
| --- | --- | --- |
| `Tenant` | `id`, `name`, `selectedCompanyId` | `id` é opaco. |
| `Company` | `id`, `tenantId`, `legalName`, `cnpj`, `status` | CNPJ mascarado na listagem; valor integral só em detalhe autorizado. |
| `Establishment` | `id`, `companyId`, `name`, `code` | Herda entitlement da empresa. |
| `Entitlement` | `id`, `tenantId`, `companyId`, `module`, `status`, `quota`, `used`, `validFrom`, `validTo`, `version`, `lastSyncedAt` | `version` alimenta concorrência otimista. |
| `LicenseAudit` | `id`, `actorId`, `action`, `before`, `after`, `at`, `traceId` | `before`/`after` redigidos. |
| `AiProvider` | `id`, `name`, `kind`, `enabled`, `secretRef`, `maskedSecret`, `status` | `kind` é `chat`, `embedding` ou `rerank`. Segredo nunca sai do cofre. |
| `AiModel` | `id`, `providerId`, `modelId`, `enabled`, `inputPriceMtok`, `outputPriceMtok` | Preços são estimativas de painel, não bloqueio. |
| `AiQuota` | `id`, `scope`, `modelId`, `limitTokens`, `usedTokens`, `limitBrl`, `usedBrl`, `resetAt` | Duas unidades: tokens e BRL. |
| `AiUsage` | `at`, `actorId`, `modelId`, `tokensIn`, `tokensOut`, `costBrl`, `screen`, `traceId` | Agregado por dia no painel. |
| `User` | `id`, `displayName`, `email`, `status`, `mfaEnabled`, `roles`, `companies` | E-mail é PII; mascarar em logs. |
| `Event` | `id`, `type`, `entity`, `entityId`, `at`, `seq`, `actorId` | Usado em SSE e auditoria. |
| `SyncConflict` | `id`, `entity`, `originNode`, `serverVersion`, `localVersion`, `detectedAt`, `resolution` | Dinheiro e fiscal nunca usam “last write wins” silencioso. |
| `OfflineOperation` | `id`, `seq`, `type`, `entity`, `status`, `createdAt`, `lastError` | Sequência monotônica por dispositivo. |

## 6. Contrato HTTP

### 6.1 Regras gerais

- Base: `/v1`.
- Autenticação obrigatória: `Authorization: Bearer <token>`.
- `Content-Type: application/json; charset=utf-8` e `Accept: application/json; charset=utf-8`.
- `camelCase` em todos os nomes de campo.
- Identificadores opacos: UUIDv7 ou ULID. Nunca expor sequência numérica.
- Horários em UTC no contrato; conversão para `America/Sao_Paulo` apenas na apresentação.
- Dinheiro em inteiro de centavos ou `NUMERIC(18,2)` serializado como string decimal; nunca `float`.
- `Idempotency-Key` obrigatório em escritas críticas: emissão/movimentação financeira, concessão/revogação de licença, alteração de quota, atribuição de papel e conclusão de ordem de serviço.
- Paginação: `{ "items": [], "nextCursor": null }`. Cursor é opaco; ordenação estável por `(createdAt, id)`.

### 6.2 Erros

Formato único:

```json
{
  "code": "LICENSE_INACTIVE",
  "message": "Módulo bloqueado por licença.",
  "details": {},
  "traceId": "trc_01J..."
}
```

| HTTP | Código | Quando usar |
| --- | --- | --- |
| 400 | `VALIDATION_ERROR` | Payload inválido; `details` lista campos. |
| 401 | `UNAUTHENTICATED` | Token ausente, inválido ou expirado. |
| 403 | `PERMISSION_DENIED` | Role ou escopo insuficiente. |
| 403 | `TENANT_MISMATCH` | Recurso fora do tenant autenticado. |
| 403 | `LICENSE_INACTIVE` | Módulo sem entitlement ativo. |
| 403 | `LICENSE_EXPIRED` | Entitlement vencido. |
| 403 | `LICENSE_BLOCKED_BY_QUOTA` | Quota esgotada para o escopo. |
| 409 | `STALE_VERSION` | `version` informado não corresponde ao estado atual. |
| 409 | `CONFLICT_UNREVIEWED` | Conflito de sincronização aguardando revisão. |
| 422 | `FISCAL_RULE_MISSING` | Regra fiscal ausente da tabela oficial datada. |
| 429 | `QUOTA_EXCEEDED` | Quota de IA excedida; usar este código, não `QUOTA_EXCEEDAD`. |
| 503 | `CACHE_STALE` | Cache obrigatório indisponível e operação não pode prosseguir. |

A UI nunca traduz um erro de licença em “sem internet”. São causas distintas e devem ter ações distintas.

### 6.3 Endpoints propostos

| Método | Path | Finalidade | Idempotência |
| --- | --- | --- | --- |
| GET | `/v1/context` | Retornar tenant e empresa fixados no token | não |
| GET | `/v1/companies` | Listar empresas vinculadas ao usuário no tenant autenticado | não |
| POST | `/v1/companies` | Criar empresa | sim |
| GET | `/v1/companies/{companyId}/entitlements` | Listar entitlements | não |
| GET | `/v1/companies/{companyId}/entitlements/{module}` | Detalhe do entitlement | não |
| PUT | `/v1/companies/{companyId}/entitlements/{module}` | Conceder/substituir entitlement | sim |
| PATCH | `/v1/companies/{companyId}/entitlements/{module}` | Ajustar quota/validade com `version` | sim |
| POST | `/v1/entitlements/check` | Consulta autoritativa de acesso | não |
| GET | `/v1/entitlements/{entitlementId}/audit` | Histórico do entitlement | não |
| GET | `/v1/ai/providers` | Listar providers | não |
| POST | `/v1/ai/providers` | Criar provider com `secretRef` | sim |
| PATCH | `/v1/ai/providers/{providerId}` | Ativar/desativar e reordenar fallback | sim |
| GET | `/v1/ai/models` | Listar modelos por provider | não |
| GET | `/v1/ai/quotas` | Listar quotas por escopo/modelo | não |
| GET | `/v1/ai/usage` | Listar uso agregado | não |
| GET | `/v1/ai/audit` | Listar auditoria de chamadas de IA | não |

### 6.4 Eventos em tempo real

- Canal proposto: `GET /v1/events?after=<seq>` com `text/event-stream`.
- Cada evento usa `event:` para o tipo, `id:` quando durável e `data:` com JSON.
- Reconexão usa `after` com backoff limitado e jitter.
- O cliente deve marcar “desatualizado” quando o stream cair, mas nunca usar o stream como autoridade de negócio: ele apenas invalida um refetch.
- A referência allcode possui G-6 (`GET /api/session/active` e `GET /api/event`) fora de escopo até decisão de contrato. Não replicar esse comportamento no Amparius sem ADR.

## 7. Design system

### 7.1 Tokens

| Categoria | Token | Valor/regra |
| --- | --- | --- |
| Cor primária | `--color-brand-900` | `#00274C` |
| Cor de ação | `--color-brand-500` | `#0073CF` |
| Superfície | `--color-surface` | branco no claro, cinza azulado profundo no escuro |
| Texto | `--color-text` | contraste mínimo `4.5:1` sobre superfície |
| Borda | `--color-border` | distingue superfície sem depender só de sombra |
| Estado | `--color-success`, `--color-warning`, `--color-danger` | sempre acompanhados de texto e ícone |
| Espaçamento | `--space-1..--space-8` | grid de 8 px; meio passo de 4 px permitido |
| Raio | `--radius-control` | 8 px |
| Foco | `--focus-ring` | 2 px sólido + offset 2 px |
| Tipografia | `--font-sans` | fonte do sistema; base 16 px; escala 1.2 |
| Largura | `--content-max` | 1200 px para tabelas administrativas |

### 7.2 Componentes base

| Componente | Responsabilidade | Estados obrigatórios |
| --- | --- | --- |
| `AppShell` | Navegação, tenant, empresa, conexão, contingência | `idle`, `loading`, `error` |
| `TenantSwitcher` | Trocar tenant/empresa ativos | `idle`, `loading`, `empty`, `error` |
| `DataTable` | Listas paginadas com filtro e ordenação | `idle`, `loading`, `empty`, `error`, `success` |
| `FilterBar` | Filtros persistidos na URL | `idle`, `error` |
| `StatePanel` | Ilustração/texto/ação para vazio e erro | `empty`, `error` |
| `LicenseStatusBadge` | Status textual + ícone + cor | `active`, `inactive`, `expired`, `blocked_by_quota`, `cache_stale`, `permission_denied` |
| `QuotaMeter` | Tokens e BRL com limites e reset | `ok`, `warning`, `exceeded`, `unknown` |
| `AuditTimeline` | Linha do tempo com ator redigido e `traceId` | `idle`, `loading`, `empty`, `error` |
| `ProviderCard` | Provider, `kind`, máscara do segredo, fallback | `enabled`, `disabled`, `error` |
| `ConfirmDialog` | Ações destrutivas ou de licença | `idle`, `loading`, `error` |
| `OfflineBanner` | Explica o que está indisponível e por quê | `online`, `offline`, `contingency` |
| `SyncConflictCard` | Compara versão local e servidor | `pending`, `resolved`, `error` |
| `AttendanceForm` | Cadastro guiado de atendimento funeral | `idle`, `saving`, `queued`, `error` |
| `ServiceOrderCard` | Execução e conclusão de ordem | `idle`, `running`, `queued`, `blocked`, `done` |

### 7.3 Responsividade

- `360–599 px`: uma coluna, tabelas viram cartões, filtros em `details/summary` ou bottom sheet acessível.
- `600–959 px`: duas colunas em formulários; tabelas permitem scroll horizontal apenas se não houver alternativa em cartão.
- `>= 960 px`: sidebar + conteúdo; tabelas densas com colunas fixas e cabeçalho sticky.
- Nenhuma ação crítica depende de hover.
- Totais e ações primárias permanecem visíveis ou facilmente retornáveis em viewport pequeno.

## 8. Especificação por tela

### CP-01 — Entrar

- **Objetivo:** autenticar com OIDC e concluir MFA quando `mfaEnabled=true`.
- **Elementos:** e-mail, senha ou botão OIDC, código MFA, link “não recebi o código”, states de erro.
- **Validações:** e-mail sintaticamente válido; código MFA com formato definido pelo provedor.
- **Erros:** `UNAUTHENTICATED`, `MFA_REQUIRED`, `MFA_INVALID`, `PERMISSION_DENIED`.
- **Segurança:** nunca registrar senha/código em log; mensagem genérica para credenciais inválidas.
- **Edge:** sessão expirada durante navegação deve preservar a rota desejada e redirecionar após login.

### CP-02 — Selecionar contexto

- **Objetivo:** escolher tenant e empresa ativos após autenticação.
- **Elementos:** lista de tenants, lista de empresas, busca, indicador de contexto atual.
- **Validações:** empresa deve pertencer ao tenant; papel deve permitir aquele escopo.
- **Erros:** `TENANT_MISMATCH`, `PERMISSION_DENIED`, `CACHE_STALE`.
- **Edge:**仅有 um tenant e uma empresa → entrar direto, mas oferecer troca no `AppShell`.

### CP-03 — Visão geral

- **Objetivo:** dar prioridade operacional sem revelar PII.
- **Elementos:** cards de módulos ativos, licenças a vencer, quota de IA, auditoria recente.
- **Estados:** `loading` esqueletos, `empty` com ação “configurar empresa”, `error` com retry.
- **Regra:** card de módulo bloqueado mostra o código do erro e a tela de destino para correção.
- **Edge:** `cache_stale` aparece como aviso distinto de “sem internet”.

### CP-04 — Empresas e CNPJ

- **Objetivo:** listar e criar `COMPANY`.
- **Elementos:** tabela/cartões com nome, CNPJ mascarado, status, quantidade de estabelecimentos.
- **Criação:** razão social, CNPJ, tenant inferido do token.
- **Erros:** `VALIDATION_ERROR`, `TENANT_MISMATCH`, `PERMISSION_DENIED`.
- **Edge:** CNPJ duplicado no mesmo tenant retorna `409` com link para a empresa existente.

### CP-05 — Entitlements

- **Objetivo:** ver rapidamente quais módulos estão liberados por empresa.
- **Elementos:** filtro por status e módulo, `LicenseStatusBadge`, validade, quota usada, `lastSyncedAt`.
- **Ordenação padrão:** módulos core primeiro, depois opcionais por nome.
- **Estados:** `empty` quando a empresa foi criada sem módulos opcionais.
- **Edge:** `lastSyncedAt` antigo não altera o status autoritativo; apenas sinaliza invalidação de cache.

### CP-06 — Detalhe da licença

- **Objetivo:** conceder, revogar e ajustar quota com auditoria.
- **Elementos:** status, validade, quota, histórico, botões “Conceder”, “Revogar”, “Ajustar quota”.
- **Validações:** `validFrom <= validTo`; quota inteira não negativa; `version` obrigatório em PATCH.
- **Erros:** `STALE_VERSION`, `LICENSE_INACTIVE`, `LICENSE_EXPIRED`, `PERMISSION_DENIED`.
- **Idempotência:** chave persistida em PUT/PATCH.
- **Edge:** revogação de módulo core é proibida; botão não aparece e endpoint falha fechado.

### CP-07 — IA e quota

- **Objetivo:** configurar providers/modelos, fallback, quota e consumo.
- **Elementos:** cards de providers com `kind`, segredo mascarado `********`, sliders de quota, tabela de uso com `screen`, `traceId`, `costBrl`.
- **Regras:** `secretRef` referencia `cofre → ai/<provedor>`; segredo nunca é exibido nem enviado de volta.
- **Quotas:** tokens e BRL em dois níveis; `QUOTA_EXCEEDED` quando excedida.
- **Fallback:** ordem reordenável; mudança é escrita com `Idempotency-Key`.
- **Estimativa:** preços por milhão de tokens são apenas informativos; não bloqueiam chamadas.
- **Edge:** provider com `kind=embedding` não aparece em lista de chat para satisfazer uma tela de conversa.

### CP-08 — Usuários e RBAC

- **Objetivo:** convidar usuários, atribuir papéis e vincular empresas.
- **Elementos:** lista, filtro por papel/empresa, convite, detalhe com `roles` e `companies`.
- **Papéis:** `owner`, `admin`, `manager`, `operator`, `auditor`; qualquer outro valor falha fechado.
- **Erros:** `VALIDATION_ERROR`, `PERMISSION_DENIED`, `STALE_VERSION`.
- **LGPD:** e-mail mascarado em listagens compartilhadas; acesso integral depende de papel.

### CP-09 — Auditoria

- **Objetivo:** responder “quem fez o quê, quando e com qual efeito”.
- **Elementos:** filtros por ator, ação, entidade, período; timeline com `traceId`; exportação autorizada.
- **Redação:** PII, segredos e payloads fiscais sensíveis são removidos antes da exibição.
- **Estados:** `empty` com explicação de que ainda não há eventos; `error` com retry.
- **Edge:** evento sem `traceId` recebe marcador “sem rastreamento” e é sinalizado em observabilidade.

### CL-01 — Painel do dia

- **Objetivo:** priorizar atendimentos e ordens do dia com estado de conexão.
- **Elementos:** contadores, lista priorizada, `OfflineBanner`, atalhos para CL-02/CL-03/CL-05.
- **Offline:** dados do cache local com `lastSyncedAt`; operações novas entram na fila com sequência.
- **Erros:** conflito não revisado bloqueia apenas a entidade afetada, não o painel inteiro.
- **Edge:** dia sem dados mostra `empty` com ação de recarregar, não tela em branco.

### CL-02 — Atendimento funeral

- **Objetivo:** registrar parte/contrato e iniciar atendimento, inclusive em contingência.
- **Elementos:** busca/criação de parte, vínculo com contrato `active`, formulário de atendimento.
- **Contrato dev-only:** `GET/POST /_dev/parties`, `POST /_dev/funeral/contracts/{contract_id}/service-orders`, `PATCH /_dev/funeral/service-orders/{id}`, `PATCH /_dev/funeral/service-orders/{id}/status`, `POST /_dev/funeral/service-orders/{id}/complete`.
- **Regras:** descrição não vai para outbox; criação exige contrato `active`; conclusão usa `Idempotency-Key`.
- **LGPD:** falecido e familiares são dados sensíveis; mascarar em logs e prompts de IA.
- **Edge:** contrato inativo desabilita criação e explica o motivo.

**Implementação Rust:** a fatia CL-02 está disponível na tela nativa do cliente.
O formulário usa somente referências opacas, confirma localmente o contrato
`active` e enfileira `funeral_journey_create`,
`funeral_service_order_advance` ou `funeral_service_order_complete` com
`Idempotency-Key`. Offline e contingência mostram `queued`; entitlement bloqueado,
relay/identidade indisponíveis e erro de validação permanecem estados explícitos.
O servidor continua revalidando tenant, RLS, entitlement, contrato e transição.

### CL-03 — Ordens de serviço

- **Objetivo:** executar, pausar e concluir ordens com rastro de sequência local.
- **Elementos:** cartão por ordem, status, responsável, prazo apresentado em `America/Sao_Paulo`, ações.
- **Estados:** `idle`, `running`, `queued`, `blocked`, `done`.
- **Offline:** transição gravada localmente com `seq`; ao reconectar, reaplicada por idempotência.
- **Conflito:** ordem com `CONFLICT_UNREVIEWED` vai para CL-05; não é sobrescrita.

### CL-04 — Offline e contingência

- **Objetivo:** tornar explícito o que funciona sem internet e o que ficou pendente.
- **Elementos:** estado da conexão, última sincronização, contagem de operações enfileiradas, explicação por funcionalidade.
- **Regras:** nunca misturar “sem internet” com “licença expirada” ou “cache expirado”.
- **Estados:** `online`, `offline`, `contingency`.
- **Edge:** operação fiscal em contingência mostra prazo de transmissão e modo de contingência; nunca estima regra tributária.

### CL-05 — Sincronização e conflitos

- **Objetivo:** acompanhar a fila e revisar conflitos.
- **Elementos:** sequência por dispositivo, status por operação, diff local/servidor, botões “manter servidor”, “reaplicar local”, “revisar”.
- **Regras:** dados mestres são autoritativos no servidor; operações locais são reaplicadas por idempotência.
- **Proibição:** “last write wins” silencioso em dinheiro ou fiscal.
- **Estados:** `pending`, `resolved`, `error`.
- **Edge:** conflito de fiscal/dinheiro nunca é resolvido por botão único sem registro de revisão.

## 9. Fluxos críticos

### 9.1 Login com MFA

1. Usuário envia credenciais em CP-01.
2. Servidor exige MFA se `mfaEnabled=true`.
3. Código inválido mantém a tela e não revela qual fator falhou.
4. Após sucesso, CP-02 usa apenas tenants/empresas do token.
5. Rota desejada é restaurada.

### 9.2 Verificação de entitlement

1. Front chama `POST /v1/entitlements/check` com `screen`, `module`, `companyId`.
2. Servidor revalida token, tenant, empresa, papel e entitlement.
3. Resposta autoriza ou devolve um dos códigos de licença.
4. UI mostra a ação correta: configurar licença, renovar, liberar quota ou aguardar cache.
5. Nunca exibe conteúdo do módulo “bloqueado” como preview funcional.

### 9.3 Concessão/revogação de licença

1. Admin abre CP-06.
2. Front envia `version` atual.
3. Servidor grava com `Idempotency-Key`.
4. Auditoria recebe `before`, `after`, ator, `traceId`.
5. `STALE_VERSION` recarrega e mostra diff; nunca sobrescreve silenciosamente.

### 9.4 Offline → reconciliação

1. Operação local recebe `seq` monotônico por dispositivo.
2. `OfflineBanner` classifica: offline, licença, cache ou conflito.
3. Ao reconectar, operações são reaplicadas por idempotência.
4. Conflito de mestre de dados usa servidor como autoridade.
5. Conflito de dinheiro/fiscal fica `pending` até revisão.

### 9.5 Jornada funeral

1. CP/CL verifica entitlement de `funeral`.
2. CL-02 cria/seleciona parte.
3. Contrato precisa estar `active`.
4. Ordem é criada sem descrição na outbox.
5. Status avança com transições permitidas; conclusão usa `Idempotency-Key`.

## 10. Acessibilidade

- WCAG 2.2 AA como mínimo.
- Navegação completa por teclado; ordem de foco segue a leitura.
- Foco visível em todos os controles interativos.
- Contraste de texto ≥ `4.5:1` e de componentes gráficos ≥ `3:1`.
- Alvos ≥ `44×44 px`.
- `aria-live` para mudanças de estado de conexão, quota e erro de salvamento.
- Tabelas com cabeçalhos associados; cartões em mobile preservam rótulos de coluna.
- Formulários com `label` programático, mensagens de erro associadas e resumo de erros no topo.
- Ícones têm texto equivalente; status nunca usa apenas cor.
- Movimento reduzido respeitado; nenhuma animação obrigatória para entender estado.
- Testes manuais com leitor de tela e navegação 100% teclado fazem parte do aceite.

## 11. Segurança e LGPD

- Token Bearer com OIDC; MFA quando habilitado; papéis desconhecidos falham fechado.
- `tenant_id` do token; filtros de empresa adicionais não substituem autorização.
- Segredos de IA ficam em cofre; UI mostra apenas `********`.
- PII de clientes, familiares e falecidos não é logada nem enviada a provedor de IA sem mascaramento.
- Prompt e resposta de IA versionados e auditados com `screen`, ator e custo.
- Exportação de auditoria respeita papel e redigiu campos sensíveis.
- Logs estruturados usam `traceId`, `actorId` opaco, `entity`, `action`; nunca payload completo de fiscal/dinheiro.
- Requisições críticas registram métrica de latência, erro e idempotência.

## 12. Observabilidade

- Cada tela relevante emite: `screen_view`, `screen_action`, `screen_error` com `screen`, `tenantId` opaco, `companyId` opaco, `role`, `traceId`.
- `screen` é id estável (`CP-06`, `CL-03`), nunca título traduzido.
- Erros de licença, quota, cache e conflito têm métrica própria para separar causa operacional de falha técnica.
- SSE/stream: medir reconexões, idade do último evento e quedas.
- Cliente offline: medir tamanho da fila, idade da operação mais antiga, taxa de reaplicação e conflitos não revisados.
- Golden files: contratos de API e SSE devem ser versionados com normalização de IDs, timestamps, durações e cursores.
- Nenhum log contém CNPJ integral, e-mail, celular, senha, código MFA ou segredo de provider.

## 13. Trade-offs

| Decisão | Alternativa considerada | Por que esta | Custo/contrapartida |
| --- | --- | --- | --- |
| Monólito modular + SPA | Microsserviços por módulo | Reduz complexidade inicial e preserva fronteiras por schema/serviço | Exige disciplina para não acoplar módulos |
| REST `/v1` + cursor | GraphQL ou offset | Contrato explícito, cacheável e alinhado ao PRD | Mais endpoints e versionamento manual |
| Bearer obrigatório | Modo tokenless do allcode `/api/v1` | ADR `0024` exige OIDC/RBAC/escopo | Divergência documentada com a referência; não fundir sem ADR |
| SSE para eventos | WebSocket | Suficiente para invalidação e mais simples no relay | Sem replay até decisão de contrato |
| Cache local no cliente | Sempre online | A operação não pode parar | Exige fila, sequência e reconciliação |
| Tokens de design próprios | Copiar tema externo | Clean-room e identidade Amparius | Mais manutenção de tokens |
| Mobile-first | Desktop-first | Loja usa dispositivos variados e contingência | Densidade desktop precisa de trabalho extra |
| Entitlement no control plane | Licença por estabelecimento | ADR `0007` define `COMPANY` como âncora | Estabelecimentos herdam e não têm exceção própria |

## 14. Sequência de entrega

| Fase | Entrega | Critério de saída |
| --- | --- | --- |
| 0 | Contratos e design tokens | Spec e aceite revisados; ADRs existentes não reabertos |
| 1 | `AppShell`, `TenantSwitcher`, CP-01/CP-02 | Login/MFA e troca de contexto passam no aceite |
| 2 | CP-03..CP-06 | Entitlements e auditoria com idempotência/versão aprovados |
| 3 | CP-07..CP-09 | IA, RBAC e auditoria com máscara/quota aprovados |
| 4 | CL-01..CL-03 | Painel, atendimento e ordens funcionam online/offline |
| 5 | CL-04/CL-05 | Contingência, fila e conflitos aprovados, inclusive caos de rede |

## 15. Gaps e decisões pendentes

Estes pontos não impedem a escrita da spec. Seguem o default indicado até decisão explícita.

- **NEEDS INPUT:** numerar ADR de frontend se ele um dia for criado. **Default:** manter `docs/adr/0026-frontend-architecture.md` como nome proposto; não criar ADR nesta rodada.
- **NEEDS INPUT:** adotar `/api/session/active` e `/api/event` do allcode. **Default:** não adotar; G-6 fica fora de escopo até contrato próprio.
- **NEEDS INPUT:** alinhar `/v1` do Amparius com `/api/v1` do allcode. **Default:** Amparius usa `/v1` e exige Bearer.
- **NEEDS INPUT:** unidade monetária de quota de IA. **Default:** BRL em centavos/string decimal, com preços por milhão apenas informativos.
- **NEEDS INPUT:** origem exata do segredo de provider. **Default:** referência `cofre → ai/<provedor>`, exibida como `********`.
- **NEEDS INPUT:** formato final do bundle desktop. **Default:** seguir [client/README.md](../../client/README.md) — Rust MSRV `1.85`, edition `2024`, `cargo run`.

## 16. Relação com QA

Todo requisito desta spec possui caso correspondente em [frontend-acceptance.md](../qa/frontend-acceptance.md). A implementação só é considerada pronta quando o caso positivo, o negativo e o edge de cada tela crítica estiverem executados, com evidência anexada, sem PII em logs ou capturas.
