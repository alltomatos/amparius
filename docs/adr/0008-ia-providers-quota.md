# ADR 0008 — Provedores de IA, modelos e cota de tokens

Status: proposto · 21/11/2026 · referencia os ADRs 0002, 0005 e 0007, o DER v0.2 e o contrato `docs/design/AI-ASSISTANT.md`

## Contexto

O DER v0.2 registra a execução de IA na célula (`AI_AGENT`, `AI_RUN`, `AI_TOOL_CALL`, `MCP_SERVER`, `KNOWLEDGE_DOC`) e `AI-ASSISTANT.md` fixa o contrato de produto do assistente contextual. O que falta é a **configuração e a contabilidade** por trás disso: de onde vêm os provedores, qual modelo é usado, quanto custa e quem paga. Hoje isso se resume a `AI_AGENT.model` — uma `text` solta — e a `AI_RUN.cost` — um número que registra o gasto mas não o governa.

Três tensões de fundo:

1. **`AI_AGENT.model` como string solta não sustenta provedor múltiplo.** Sem um catálogo de provedor/modelo, não há como expressar fallback entre provedores, versão de modelo, preço por token ou desligamento de um provedor. Qualquer mudança de modelo vira edição manual de string em cada agente, sem auditoria nem validação.
2. **Cota exige tabela, não só campo.** Um teto de gasto é uma política com dono, período, unidade e comportamento no estouro — e vale em dois níveis (tenant e CNPJ). `AI_RUN.cost` é o histórico; não é o limite nem o saldo. Sem tabela de cota, não há como bloquear antes de gastar nem como cobrar o excedente.
3. **A fronteira já está desenhada** (ADRs 0005 e 0007): catálogo e billing vivem no control plane; a célula e o relay só guardam snapshot assinado de enforcement. Provedores, modelos e cota são mais um caso da mesma regra — config no control plane, enforcement na borda, sem tabela duplicada.

## Decisão

As decisões abaixo foram fechadas com o dono do produto e são formalizadas aqui; não são reabertas nesta fatia.

### 1. Chave centralizada; configuração em dois níveis

1. A **chave de API é centralizada no admin SaaS** (um segredo por provedor, no cofre; ver §Consequências). O tenant nunca guarda chave.
2. A **configuração** — quais provedores/modelos estão habilitados, a ordem de fallback e a cota — pode ser definida **por tenant OU por CNPJ** (dois níveis). Justificativa: o tenant é o grupo econômico que contrata o plano (ADR 0007), mas a operação que consome IA é o CNPJ; o dono precisa tanto de um teto compartilhado quanto de rédeas por empresa, exatamente como no entitlement por módulo.

### 2. Cota em dois níveis

3. **Teto global por tenant** (compartilhado entre os CNPJs do grupo) **+ sub-cota opcional por CNPJ** dentro do teto global. Sem sub-cota definida, o CNPJ usa o global. Justificativa: o global dá previsibilidade de fatura ao grupo; a sub-cota impede que um CNPJ sozinho consuma o orçamento dos demais.

### 3. Unidade da cota

4. O teto é em **R$ (valor real)**, não em tokens. Tokens entram **apenas como estimativa de referência** no painel (conversão por preço de tabela do modelo). Justificativa: token não é unidade contábil — preço por token varia por provedor/modelo e por moeda; R$ casa com `NUMERIC(18,2)` do DER e com a fatura.

### 4. Política no estouro

5. Ao estourar o teto, o comportamento é **configurável por tenant**: **exceder com cobrança extra** (overage) **ou bloquear** (hard stop), por campo de política da cota. Justificativa: um só comportamento fixo obrigaria clientes a escolher entre "operar às cegas" e "ficar sem assistente"; a política vira configuração, não código.

### 5. Offline

6. Offline (relay sem internet), o assistente fica **desabilitado**: só dicas/textos fixos pré-computados, **sem LLM e sem consumo de cota**. Justificativa: sem conexão não há como aferir custo nem renovar o teto; consumir cota sem aferição viola a contabilidade, e a regra "a operação não para" não exige IA (os fluxos críticos não dependem do assistente).

### 6. Escopo desta fatia

7. Esta fatia é **somente documentação** (ADR + gancho para o DER v0.3). Nenhum código é escrito agora. Justificativa: a decisão de modelo precede a implementação, e o Engenheiro de Dados precisa do contrato antes de formalizar o DER.

## Consequências

- **Fronteira control plane (explícita).** Provedor, modelo e cota vivem no Postgres do control plane (ADR 0005), no mesmo banco de `module` e `entitlement` (ADR 0007). A célula e o relay recebem um **snapshot assinado de enforcement** — allowlist de provedor/modelo + teto + política — com `version` monotônica e `valid_until` (TTL). Ao expirar sem renovação, **fail-closed**: o assistente bloqueia em vez de continuar sem teto. É o mesmo mecanismo do pacote de entitlement e das tabelas fiscais em `SCALING.md` §7.
- **`AI_AGENT.model` deixa de ser string solta.** Passa a carregar o **código estável do modelo** (do catálogo), validado contra o snapshot de enforcement. Não é FK entre bancos: é referência por código, resolvida no control plane e revalidada a cada `AI_RUN`.
- **Segredo `api_key` em cofre.** A chave nunca entra no banco nem em log: `AI_PROVIDER.api_key_ref` é referência para o cofre. Nenhum segredo no repositório (regra 6 do AGENTS.md); resposta do provedor e prompt são tratados como dados sensíveis (LGPD, `AI-ASSISTANT.md` §8).
- **Fallback entre provedores.** A ordem de fallback é configurável por tenant/CNPJ (tabela de vínculo, abaixo). Falha de um provedor desce para o próximo habilitado na ordem; nunca para um provedor fora da allowlist.
- **Governança conecta ao AGENTS.md §7.** Todo agente declara tenant, papel, tools permitidas, escopo de dados e limite de custo; PII mascarada antes do provedor; MCP por allowlist; chamadas auditadas (quem, o quê, entrada, saída, custo). A cota é o **limite de custo** daquela seção, agora com tabela e enforcement próprios.
- **Contabilidade é por reporte, não por FK entre bancos.** O consumo é agregado de `AI_RUN.cost` na célula e reportado ao control plane (mesmo padrão do `USER_QUOTA.used` do ADR 0007). O control plane é quem decide estourar/continuar; a célula consome contra o teto do snapshot.

## Modelo de dados proposto

Todas as tabelas novas vivem no **Postgres do control plane** (schema a definir pelo Engenheiro de Dados — sugestão `ai`, ao lado de `entitlement`), seguindo as regras de modelagem do DER v0.2 §6: `uuidv7`, `tenant_id NOT NULL` + RLS, dinheiro `NUMERIC(18,2)`, tempo em UTC, enums como código em tabela de domínio, `(tenant_id, ...)` nos índices. **Desenho final no DER v0.3 com o Engenheiro de Dados.**

### `AI_PROVIDER` — catálogo de provedores (global, registrado pelo admin SaaS)

| campo | tipo | nota |
| --- | --- | --- |
| `id` | `uuid PK` | uuidv7 |
| `code` | `text UNIQUE` | código estável (ex.: `openai`, `anthropic`, `deepseek`); nunca reciclado |
| `name` | `text` | nome de exibição |
| `kind` | `text` | `llm` / `embedding` / `rerank` (tabela de domínio) |
| `api_key_ref` | `text` | referência para o cofre; **nunca** a chave |
| `status` | `text` | `active` / `disabled` |
| `created_at`, `updated_at` | `timestamptz` | UTC |

### `AI_MODEL` — catálogo de modelos (global)

| campo | tipo | nota |
| --- | --- | --- |
| `id` | `uuid PK` | uuidv7 |
| `provider_id` | `uuid FK → AI_PROVIDER.id` | dono do modelo |
| `code` | `text` | código estável do modelo (ex.: `gpt-4o`); chave alternativa `(provider_id, code)` |
| `name` | `text` | nome de exibição |
| `kind` | `text` | `chat` / `embedding` / `rerank` (tabela de domínio) |
| `context_window` | `int` | referência |
| `input_price_mtok`, `output_price_mtok` | `NUMERIC(18,6)` | preço por 1M tokens, **só estimativa de painel** — não é o teto |
| `status` | `text` | `active` / `deprecated` / `disabled` |
| `created_at`, `updated_at` | `timestamptz` | UTC |

### `AI_MODEL_ALLOWANCE` — vínculo provedor/modelo por tenant OU CNPJ (configuração, decisão 1)

| campo | tipo | nota |
| --- | --- | --- |
| `id` | `uuid PK` | uuidv7 |
| `tenant_id` | `uuid FK → TENANT.id` | sempre presente; RLS |
| `company_id` | `uuid FK → COMPANY.id` | **nulo = vale para o tenant todo**; preenchido = só aquele CNPJ |
| `model_id` | `uuid FK → AI_MODEL.id` | modelo habilitado |
| `fallback_order` | `int` | ordem de fallback dentro do escopo (decisão: fallback entre provedores) |
| `enabled` | `bool` | liga/desliga sem apagar |
| `created_at`, `updated_at` | `timestamptz` | UTC |

> A allowlist de modelos derivada desta tabela entra no snapshot de enforcement que a célula/relay consomem.

### `AI_TOKEN_QUOTA` — teto e política (decisões 2, 3 e 4)

| campo | tipo | nota |
| --- | --- | --- |
| `id` | `uuid PK` | uuidv7 |
| `tenant_id` | `uuid FK → TENANT.id` | sempre presente; RLS |
| `company_id` | `uuid FK → COMPANY.id` | **nulo = teto global do tenant**; preenchido = sub-cota daquele CNPJ |
| `cap_amount` | `NUMERIC(18,2)` | teto real em **R$** (decisão 3); dinheiro, nunca `float` |
| `period` | `text` | período da cota (ex.: `monthly`); tabela de domínio |
| `on_limit_policy` | `text` | `overage` (excede com cobrança extra) \| `hard_stop` (bloqueia) — decisão 4 |
| `status` | `text` | `active` / `disabled` |
| `created_at`, `updated_at` | `timestamptz` | UTC |

> Invariante: sub-cota (`company_id` preenchido) só existe **dentro** de um teto global vigente do mesmo tenant; não pode excedê-lo.

### `AI_USAGE` — consumo (uso/consumo, alimentado por reporte da célula)

| campo | tipo | nota |
| --- | --- | --- |
| `tenant_id` | `uuid FK → TENANT.id` | RLS |
| `company_id` | `uuid FK → COMPANY.id` | nulo = agregado do tenant |
| `period` | `date` | mês de competência (primeiro dia) |
| `consumed_amount` | `NUMERIC(18,2)` | gasto acumulado no período, em **R$** |
| `tokens_in`, `tokens_out` | `bigint` | **estimativa de referência** apenas (decisão 3) |
| `updated_at` | `timestamptz` | UTC |

> `AI_USAGE` é **append/upsert por período**, alimentado pelo reporte de ocupação da célula (agregação de `AI_RUN.cost`), não por FK entre bancos — mesmo padrão do `USER_QUOTA.used`. O control plane compara `consumed_amount` contra `AI_TOKEN_QUOTA.cap_amount` e aplica a `on_limit_policy`.

### Snapshot de enforcement (célula/relay) — não é tabela de negócio

Pacote assinado com `version` monotônica e `valid_until` (TTL), contendo: allowlist de provedores/modelos (de `AI_MODEL_ALLOWANCE`), teto global + sub-cotas (`AI_TOKEN_QUOTA`) e a política de estouro. Offline, o assistente desabilita (decisão 5); online, `fail-closed` ao expirar o TTL sem renovação.

## Alternativas rejeitadas

- **BYOK (o tenant traz a própria chave de API)**: flexível, mas dispersa segredo em cada tenant, fere a regra "nenhum segredo no repositório" e complica a contabilidade centralizada. Chave centralizada no admin SaaS (decisão 1).
- **Teto em tokens em vez de custo (R$)**: token não é unidade contábil; preço por token varia por provedor/modelo e por moeda, o que tornaria o teto enganoso na fatura. R$ casa com `NUMERIC(18,2)` e com a cobrança (decisão 3).
- **Degradar automaticamente de modelo ao estourar a cota**: troca silenciosa de modelo muda comportamento e custo sem o cliente saber, e esconde o limite. Substituído por política explícita de `overage` ou `hard_stop` (decisão 4).
- **Provedor/modelo/cota como campo na célula (sem control plane)**: duplicaria config entre células e violaria ADR 0005 (billing/config no control plane); rejeitado em favor do snapshot de enforcement.

## Critérios de aceite

1. O ADR fixa as 6 decisões fechadas com o produto, cada uma com justificativa; não há valor fiscal inventado (fora do escopo de fiscal, conforme ADR 0004).
2. `AI_PROVIDER`, `AI_MODEL`, `AI_TOKEN_QUOTA` e o consumo (`AI_USAGE`) estão propostos com campos e FKs, em `uuidv7`, `tenant_id` + RLS, dinheiro `NUMERIC(18,2)` e `company_id` no nível empresa.
3. A fronteira control plane está explícita: catálogo e cota no control plane; célula/relay só com snapshot de enforcement assinado, `fail-closed` ao expirar.
4. `api_key` aparece apenas como `api_key_ref` para cofre; nenhum segredo no banco, em log ou no repositório.
5. As alternativas rejeitadas (BYOK, teto em token, degrade automático) estão registradas com motivo.
6. Fica o gancho explícito para o Engenheiro de Dados formalizar as tabelas no DER v0.3.

## Mudanças decorrentes

### DER (v0.3 — com o Engenheiro de Dados)

- Adicionar, no control plane (junto a `entitlement`), as tabelas `AI_PROVIDER`, `AI_MODEL`, `AI_MODEL_ALLOWANCE`, `AI_TOKEN_QUOTA` e `AI_USAGE` (campos acima).
- Atualizar `AI_AGENT.model` de `text` solta para **código estável de modelo** do catálogo (referência por código, validada contra o snapshot; sem FK entre bancos).
- Confirmar precisão de preço (`NUMERIC(18,6)` por 1M tokens, só estimativa) e `NUMERIC(18,2)` para teto/consumo.

### ARCHITECTURE.md

- §3 (control plane): incluir **provedores/modelos de IA + cota** como responsabilidade do control plane, junto de billing e entitlement.
- §4 (módulos): registrar a cota de IA como concern transversal de enforcement, par do entitlement.
- `SCALING.md` §7: distribuição do snapshot de IA (allowlist + teto + política) às células, par do pacote de tabelas fiscais e do entitlement.

### PRD (RFs novos)

- RF: cadastro de provedor/modelo pelo admin SaaS com chave em cofre.
- RF: configuração de provedor/modelo/cota por tenant e por CNPJ (dois níveis).
- RF: teto global + sub-cota por CNPJ; política de estouro `overage` ou `hard_stop` configurável.
- RF: painel de consumo em R$ com estimativa de tokens apenas como referência.
- RF: offline desabilita o assistente (só textos fixos), sem consumo de cota.

### Relay (cache de enforcement de IA)

- Snapshot de IA com TTL e versão; `fail-closed` ao expirar; offline = assistente desabilitado; sinalização na UI quando o assistente bloqueou por cota ou por expiração.
