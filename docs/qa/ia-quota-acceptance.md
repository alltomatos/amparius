# QA — Aceite e contratos de teste: cota de IA no control plane

Versão 1.0 · 21/11/2026 · QA de Contrato · repo em fase de documentação (casos descritivos, não código)

Referências: ADR 0008 (`docs/adr/0008-ia-providers-quota.md`), DER v0.3 (`docs/data/DER.md` §5.1 e regras 15–18), ADR 0005 (`docs/adr/0005-celulas-vps.md`), ADR 0002 (`docs/adr/0002-relay-offline.md`), ADR 0003 (`docs/adr/0003-multitenancy-rls.md`), contrato do assistente (`docs/design/AI-ASSISTANT.md` §8), padrão de formatação `docs/qa/entitlement-acceptance.md`.

## 1. Escopo e convenções

Este documento fixa os **critérios de aceite observáveis** e os **contratos de teste** da configuração e da contabilidade de IA aprovadas no ADR 0008: catálogo de provedor/modelo, allowlist por tenant/CNPJ, cota em R$ em dois níveis, política de estouro, snapshot de enforcement com `fail-closed` e contabilidade por reporte. Cada caso é Given/When/Then com critério objetivo (status HTTP, contagem, ausência de registro, versão, TTL, código de erro). Onde o contrato ainda não está implementado, o caso já define o **golden esperado** (payload, shape de evento, código de erro) para o qual a implementação deve convergir.

**Fora de escopo:** cálculo de tributo e regra fiscal. Nenhuma alíquota, CST, cClassTrib, NCM, CFOP ou código de serviço aparece neste documento (ADR 0004). Nada aqui reabre as 7 decisões do ADR 0008. Nenhum valor de preço de modelo é tratado como teto: `input_price_mtok`/`output_price_mtok` são estimativa de painel, nunca a unidade do bloqueio (DER regra 15).

**Nomenclatura usada nos casos:**

| Termo | Significado |
| --- | --- |
| **Provedor** | `AI_PROVIDER`, catálogo global registrado pelo admin SaaS; `api_key_ref` aponta para o cofre, nunca a chave. |
| **Modelo** | `AI_MODEL`, catálogo global; `code` estável (ex.: `gpt-4o`, `claude-sonnet-4`), nunca nome de exibição. |
| **Allowlist** | `AI_MODEL_ALLOWANCE` por tenant OU CNPJ; deriva a allowlist do snapshot. |
| **Cota** | `AI_TOKEN_QUOTA`; teto em R$ (`cap_amount`) + `on_limit_policy` (`overage` \| `hard_stop`). |
| **Consumo** | `AI_USAGE.consumed_amount` em R$, alimentado por reporte da célula (agregação de `AI_RUN.cost`). |
| **Sub-cota** | cota com `company_id` preenchido; prevalece sobre o teto global do tenant. |
| **Teto global** | cota com `company_id` nulo; vale para o tenant inteiro. |
| **Snapshot de IA** | pacote assinado (allowlist + teto + política) com `version` monotônica e `valid_until`; `fail-closed` ao expirar. |
| **Célula** | VPS que executa `AI_RUN`/`AI_TOOL_CALL` e reporta uso (ADR 0005). |
| **Control plane** | fonte da verdade: catálogo, allowlist, cota, consumo (schema `ai`). |
| **Excedente** | `consumed_amount` acima de `cap_amount` quando `on_limit_policy = overage`. |

**Contratos propostos neste documento** (a confirmar pela implementação — são o alvo do teste, não fato do código):

| Código | HTTP | Quando |
| --- | --- | --- |
| `AI_QUOTA_EXCEEDED` | 403 | `hard_stop` e `consumed_amount >= cap_amount` no escopo que decide (CNPJ antes de tenant). |
| `AI_QUOTA_SCOPE_INVALID` | 422 | Sub-cota excede o teto global do tenant, ou escopo de cota/allowlist inválido. |
| `AI_MODEL_NOT_ALLOWED` | 403 | `AI_AGENT.model` fora da allowlist do snapshot, ou modelo `deprecated`/`disabled`. |
| `AI_PROVIDER_UNAVAILABLE` | 503 | Nenhum provedor habilitado disponível após esgotar a ordem de fallback. |
| `AI_SNAPSHOT_EXPIRED` | 403 | `valid_until` do snapshot passou sem renovação (fail-closed). |
| `AI_ASSISTANT_OFFLINE` | 503 | Modo offline: assistente desabilitado, sem LLM e sem consumo de cota. |
| `AI_USAGE_REPORT_INVALID` | 422 | Reporte de uso com tenant/escopo divergente da célula ou payload inválido. |

O corpo de erro segue um único envelope: `{"error":{"code":"<código>","tenant_id":"<uuid>","company_id":"<uuid ou null>","scope":"tenant|company","cap_amount":"<R$ ou null>","consumed_amount":"<R$ ou null>","period":"<aaaa-mm-01 ou null>","message":"<texto não sensível>"}}`. Valores de dinheiro são **sempre string decimal com 2 casas** (nunca `float`, nunca inteiro de centavos exposto). O envelope nunca inclui dado de outro tenant, chave de API, prompt ou PII.

---

## 2. Casos

### Ponto 1 — Cota em R$ e estouro

**QA-Q-01 · Abaixo do teto permite.**
Given `AI_TOKEN_QUOTA` ativa com `cap_amount = 100.00`, `on_limit_policy = overage`, e `AI_USAGE.consumed_amount = 40.00` no período e escopo.
When `POST /api/v1/ai/runs` é submetido com payload válido.
Then HTTP 201, o run é criado e executado.
And nenhum código de erro de cota é retornado.
And a decisão usou `consumed_amount < cap_amount` comparado em R$ (`NUMERIC(18,2)`), nunca em tokens.
Critério: a comparação é monetária e no escopo mais específico; `tokens_in`/`tokens_out` não participam da decisão.

**QA-Q-02 · Atingiu o teto com `hard_stop` bloqueia antes da chamada.**
Given `cap_amount = 100.00`, `on_limit_policy = hard_stop`, `consumed_amount = 100.00`.
When `POST /api/v1/ai/runs`.
Then HTTP 403 `AI_QUOTA_EXCEEDED`.
And o bloqueio ocorre **antes** de qualquer chamada: instrumentação de saída registra zero requisição à API do provedor.
And nenhuma linha em `AI_RUN` é criada.
And nenhuma linha em `AI_TOOL_CALL` é criada.
Critério: o bloqueio é no control plane, não na borda; um teste que passa por mock alterado do provedor não conta.

**QA-Q-03 · Atingiu o teto com `overage` continua e registra o excedente.**
Given `cap_amount = 100.00`, `on_limit_policy = overage`, `consumed_amount = 100.00`.
When `POST /api/v1/ai/runs` com custo de 5.00.
Then HTTP 201 e o run executa.
And `AI_USAGE.consumed_amount` passa a `105.00` (o excedente é registrado, não descartado).
And o reporte do período carrega `overage_amount = 5.00`.
And a resposta da API indica o estado de overage (campo observável na resposta ou em `AUDIT_LOG`).
Critério: `overage` cobra o excedente; ele nunca é truncado nem escondido do consumo.

**QA-Q-04 · `hard_stop` decide pelo saldo prévio, não pelo custo estimado.**
Given `cap_amount = 100.00`, `on_limit_policy = hard_stop`, `consumed_amount = 99.99`.
When um run com custo 0.02 é submetido.
Then a chamada é permitida (o saldo no momento da decisão era 99.99 < 100.00).
And o consumo resultante `100.01` fica registrado.
And a chamada seguinte é bloqueada com `AI_QUOTA_EXCEEDED`.
Critério: a regra é `consumed_amount < cap_amount` avaliada **antes** da chamada; não existe previsão de custo que bloqueie antes. Se a implementação passar a prever, este contrato deve ser reaberto — aqui o observável está fixado.

**QA-Q-05 · Cota mensal: o período novo zera o consumo.**
Given `consumed_amount = 100.00` em `period = 2026-10-01` e `cap_amount = 100.00`.
When em `2026-11-01` é submetido um run.
Then o run é permitido (o consumo do período `2026-11-01` é `0.00`).
And o consumo de outubro permanece `100.00`, intacto.
Critério: `AI_USAGE` é por `(tenant_id, company_id, period)`; a comparação é sempre no período corrente, nunca no acumulado histórico.

**QA-Q-06 · Escopo: sub-cota de CNPJ prevalece sobre o teto global.**
Given teto global do tenant `cap_amount = 100.00` (`company_id` nulo); sub-cota do CNPJ `cap_amount = 20.00`; consumo global do tenant `50.00`; consumo do CNPJ `20.00`.
When o CNPJ submete um run.
Then HTTP 403 `AI_QUOTA_EXCEEDED` com `scope = "company"` (aplica a sub-cota, não o global).
And a resposta traz `cap_amount = "20.00"` e `consumed_amount = "20.00"`.
And um outro CNPJ do mesmo tenant, com consumo próprio `0.00`, é atendido normalmente — o global não foi consumido pelo primeiro.
Critério: o escopo mais específico decide; o global é teto do grupo, não saldo compartilhado por CNPJ.

**QA-Q-07 · Invariante: sub-cota não excede o teto global.**
Given teto global ativo `100.00` e sub-cota existente `80.00` para um CNPJ.
When se tenta criar sub-cota de `30.00` para outro CNPJ do mesmo tenant.
Then HTTP 422 `AI_QUOTA_SCOPE_INVALID`.
And a sub-cota não é persistida.
And nenhum snapshot é reemitido com soma inválida.
Critério: `sum(sub-cotas ativas) ≤ teto global ativo` é verificado na escrita da sub-cota e no cálculo do snapshot, nunca presumido.

**QA-Q-08 · `company_id` nulo é o teto do tenant.**
Given cota com `company_id` nulo e nenhuma sub-cota vigente.
When qualquer CNPJ do tenant consome.
Then o teto aplicado é o global.
And o consumo é acumulado no registro com `company_id` nulo (agregado do tenant).
And uma sub-cota criada depois passa a valer para aquele CNPJ sem alterar o registro de consumo agregado.
Critério: `NULL` é escopo do tenant inteiro, não "sem escopo" nem "ignorado".

---

### Ponto 2 — Allowlist e fallback

**QA-A-01 · Modelo fora da allowlist falha fechado.**
Given `AI_AGENT.model = "gpt-4o"` e o snapshot de IA do tenant **não** contém `gpt-4o` (allowlist vazia ou com outro modelo).
When `POST /api/v1/ai/runs`.
Then HTTP 403 `AI_MODEL_NOT_ALLOWED`.
And nenhuma requisição sai ao provedor.
And nenhuma linha em `AI_RUN` é criada.
And a UI mostra erro de modelo não permitido (não erro genérico de sync).
Critério: a validação ocorre antes da execução; `AI_AGENT.model` é referência por código ao snapshot, não string livre.

**QA-A-02 · Modelo na allowlist executa com o provedor resolvido.**
Given `AI_AGENT.model = "claude-sonnet-4"` presente na allowlist do snapshot com `fallback_order = 1` e `enabled = true`.
When `POST /api/v1/ai/runs`.
Then HTTP 201 e o run usa o provedor/modelo resolvido do snapshot.
And `AI_RUN` registra o `model_code` resolvido (não o nome de exibição).
Critério: o código do catálogo é estável; o snapshot, não o banco da célula, é a fonte da allowlist.

**QA-A-03 · Fallback só entre provedores habilitados, na ordem.**
Given allowlist com `claude-sonnet-4` (provedor `anthropic`, `fallback_order = 1`) e `gpt-4o` (provedor `openai`, `fallback_order = 2`); o provedor de ordem 1 indisponível.
When o run é submetido.
Then o run tenta o provedor de ordem 2 e conclui com sucesso.
And a falha do provedor de ordem 1 fica registrada (tentativa observável).
And nenhum provedor fora da allowlist é acionado.
Critério: a ordem vem do snapshot; o fallback jamais sai da allowlist (não há "provedor de emergência" implícito).

**QA-A-04 · Esgotada a ordem, erro estável.**
Given allowlist com todos os provedores `disabled` ou indisponíveis.
When o run é submetido.
Then HTTP 503 `AI_PROVIDER_UNAVAILABLE`.
And nenhuma chamada fora da allowlist é feita.
And nenhum `AI_RUN` conclui com sucesso.
Critério: a indisponibilidade é comunicada com código próprio, distinguível de cota e de modelo não permitido.

**QA-A-05 · Modelo `deprecated` ou `disabled` não executa.**
Given `AI_MODEL.status = deprecated` (ou `disabled`) e o modelo referenciado por `AI_AGENT.model`.
When o run é submetido.
Then HTTP 403 `AI_MODEL_NOT_ALLOWED`.
And o snapshot correspondente não inclui o modelo (a allowlist do snapshot só carrega modelos `active`).
Critério: status de catálogo é enforceable; permitir modelo deprecado quebra o contrato.

**QA-A-06 · Nome de exibição ou código solto não resolve.**
Given `AI_AGENT.model = "GPT-4o"` (nome de exibição) ou valor não cadastrado.
When o run é submetido.
Then HTTP 403 `AI_MODEL_NOT_ALLOWED`.
And nenhum provedor é acionado.
Critério: `AI_AGENT.model` é comparado ao `code` do catálogo; não há resolução fuzzy, por nome de exibição nem por provedor.

---

### Ponto 3 — Snapshot, versão e TTL

**QA-N-01 · Snapshot expirado falha fechado.**
Given snapshot de IA com `valid_until` no passado e sem renovação.
When `POST /api/v1/ai/runs`.
Then HTTP 403 `AI_SNAPSHOT_EXPIRED`.
And nenhuma requisição sai ao provedor.
And a UI sinaliza bloqueio por expiração do snapshot (não por "sem internet").
Critério: `fail-closed`; não existe caminho local que desbloqueie o assistente sem snapshot novo.

**QA-N-02 · Snapshot válido dentro do TTL executa.**
Given snapshot com `valid_until` futuro em relação ao relógio da célula.
When o run é submetido.
Then o run executa normalmente.
And a comparação de TTL é feita em UTC, com relógio controlado no teste de caos.
Critério: o caso de expiração (QA-N-01) e este são os dois lados da mesma checagem.

**QA-N-03 · Versão monotônica: maior substitui, menor é descartado.**
Given snapshot local com `version = N`.
When chega um snapshot com `version = N+1`.
Then a célula/relay substitui o snapshot local por `N+1`.
When chega um snapshot com `version <= N` (entrega fora de ordem).
Then ele é ignorado e o snapshot local permanece `N`.
Critério: `version` é inteiro estritamente monotônico por tenant/escopo; nunca regressivo.

**QA-N-04 · Renovação eleva a versão e estende o TTL.**
Given snapshot `N` com `valid_until = T1`.
When o control plane emite `N+1` com `valid_until = T2 > T1`.
Then o snapshot local passa a `N+1` e o TTL passa a `T2`.
And um assistente bloqueado por expiração volta a funcionar no ciclo seguinte.
Critério: renovação é **nova versão**, nunca edição do pacote antigo.

**QA-N-05 · Snapshot não carrega segredo nem catálogo completo.**
Given o pacote de snapshot de IA.
When inspecionado.
Then ele contém apenas allowlist (provedor/modelo/ordem), teto/sub-cotas e política — com `version` e `valid_until`.
And não contém `api_key` nem `api_key_ref`.
And não contém as tabelas `AI_PROVIDER`/`AI_MODEL` completas.
Critério: o cofre fica no control plane; a borda nunca recebe chave nem catálogo bruto.

---

### Ponto 4 — Offline

**QA-O-01 · Offline desabilita o assistente e não chama o provedor.**
Given o relay em modo `offline` (sem internet).
When o atendente tenta usar o assistente.
Then HTTP 503 `AI_ASSISTANT_OFFLINE` (ou a UI nem oferece o rail).
And a instrumentação de saída registra **zero requisição à API do provedor**.
And nenhuma linha em `AI_RUN` é criada.
Critério: offline = assistente desabilitado, sem LLM; "a operação não para" não depende de IA.

**QA-O-02 · Offline não consome cota nem reporta uso.**
Given relay offline.
When se inspecionam `AI_USAGE` e o snapshot.
Then `consumed_amount` permanece inalterado.
And nenhum `ai.usage.reported` é emitido pela célula no período.
Critério: sem conexão não há como aferir custo; não existe gasto fantasma registrado.

**QA-O-03 · Textos fixos continuam disponíveis offline.**
Given modo offline com dicas/sugestões pré-computadas.
When o atendente abre a tela.
Then as sugestões fixas aparecem e funcionam sem LLM.
And a UI rotula o conteúdo como local/fixo, não como resposta do modelo.
Critério: o modo offline degrada para conteúdo estático; nunca para "resposta vazia" nem para erro de modelo.

---

### Ponto 5 — Isolamento e RLS

**QA-R-01 · Corpo não define escopo.**
Given uma sessão autenticada do tenant A.
When uma requisição envia `{"tenant_id":"<uuid do tenant B>"}` e `{"company_id":"<uuid de empresa de B>"}` no corpo.
Then esses campos são descartados.
And o escopo efetivo é o da sessão (tenant A, empresa da sessão).
And a resposta não contém dado de B.
Critério: não existe bind de `tenant_id`/`company_id` a partir do corpo em nenhum endpoint de IA ou cota.

**QA-R-02 · Leitura cross-tenant falha.**
Given um token do tenant A.
When `GET` de cota, consumo, allowlist ou provedor de B (por id ou por filtro).
Then HTTP 404 (não revela existência) ou 403.
And nenhum campo de B (cap, consumo, modelo, provedor) aparece na resposta.

**QA-R-03 · Consumo de A não toca a cota de B.**
Given cota de B `cap_amount = 100.00`, `hard_stop`, `consumed_amount = 100.00`.
Given cota de A `cap_amount = 100.00`, `consumed_amount = 0.00`.
When A faz um run.
Then o run de A é permitido e registra consumo em A.
And o `consumed_amount` de B permanece `100.00`.
And o snapshot de A não contém cota, allowlist ou consumo de B.
Critério: a comparação de cota é por tenant; o isolamento vale no snapshot e na tabela.

**QA-R-04 · Query direta sem contexto falha.**
Given uma conexão com `app.tenant_id` definido para A.
When `SELECT` sobre `ai_token_quota`, `ai_usage` ou `ai_model_allowance` filtrando por id de B.
Then a RLS retorna zero linhas.
Critério: qualquer query de teste que tente cruzar tenants sem `BYPASSRLS` retorna vazio ou erro.

**QA-R-05 · Reporte de uso não cruza tenant.**
Given a célula de A autenticada no control plane.
When o payload de `ai.usage.reported` declara `tenant_id` de B (ou omite o tenant).
Then o control plane rejeita com HTTP 422 `AI_USAGE_REPORT_INVALID`.
And `AI_USAGE` de B não é alterado.
And o reporte válido de A é aplicado normalmente.
Critério: o reporte é validado pelo tenant da célula; a ausência de FK entre bancos não abre mão da checagem de escopo.

**QA-R-06 · Respostas não vazam identificadores vizinhos.**
Given uma sessão do tenant A.
When listagens e autocomplete de provedores, modelos, cotas e consumo respondem.
Then as respostas contêm apenas entidades de A.
And ids/nomes de B não aparecem em sugestões, em erros nem em metadados.

---

### Ponto 6 — PII e segredo

**QA-P-01 · Prompt sai mascarado antes do provedor.**
Given um contexto com PII de falecido/familiar (nome, CPF, endereço, telefone).
When o prompt é montado para o provedor de modelo.
Then os campos de PII aparecem mascarados no prompt enviado e no prompt versionado persistido.
And `AI_RUN`/`AI_TOOL_CALL` registram a versão mascarada, nunca o PII limpo.
Critério: a máscara é aplicada **antes** da chamada e antes de persistir; o log não contém PII limpa.

**QA-P-02 · `api_key` nunca aparece em log nem em resposta.**
Given qualquer operação de IA (configuração, run, listagem de provedor, snapshot).
When se inspecionam logs, respostas de API, tabelas e o pacote de snapshot.
Then não existe a chave em nenhum desses pontos; existe apenas `api_key_ref`.
Critério: nenhum segredo no banco, em log, no snapshot ou no repositório (AGENTS.md §6).

**QA-P-03 · `AI_PROVIDER` sem chave no payload.**
Given o catálogo de provedores.
When `GET` do provedor responde.
Then o payload traz `id`, `code`, `name`, `kind`, `status` e `api_key_ref` (referência), e **não** possui campo `api_key`.
Critério: a coluna de chave não existe; só a referência ao cofre.

**QA-P-04 · Prompt e resposta versionados, com retenção.**
Given uma execução concluída.
When se consulta o histórico do run.
Then prompt e resposta ficam versionados e retidos conforme a configuração de retenção do tenant.
And a retenção é aplicável (registro expirado some/é descartado) sem quebrar a auditoria de custo.
Critério: `AI-ASSISTANT.md` §8; retenção configurável, não fixa em código.

**QA-P-05 · Chamada auditada com ator e custo.**
Given uma execução com tool call.
When se inspeciona `AI_RUN` e `AI_TOOL_CALL`.
Then cada registro tem `tenant_id`, ator (`user_id`), entrada, saída, `status` e custo.
And o custo alimenta o reporte de consumo (QA-U-01).
And a entrada/saída está mascarada (QA-P-01).
Critério: "quem, o quê, entrada, saída, custo" auditado (ADR 0008, governança do AGENTS.md §7).

---

### Ponto 7 — Contabilidade por reporte

**QA-U-01 · Reporte agrega `AI_RUN.cost` e atualiza `AI_USAGE`.**
Given 3 `AI_RUN` com `cost` 1.00, 2.50 e 0.50 no mesmo período/escopo.
When o reporte da célula é recebido.
Then `AI_USAGE.consumed_amount` passa a `4.00` (soma exata).
And `tokens_in`/`tokens_out` são somados como referência.
And o mesmo reporte reenviado com o mesmo `report_id` não duplica o consumo.
Critério: agregação no servidor, idempotente por `report_id`; `consumed_amount` nunca é digitado.

**QA-U-02 · Reporte lê a célula, não a escreve.**
Given um reporte de uso.
When aplicado.
Then nenhum `AI_RUN` é alterado ou apagado.
And `AI_USAGE` sofre upsert por `(tenant_id, company_id, period)`.
Critério: o reporte é o único canal de atualização do control plane; a célula não escreve direto.

**QA-U-03 · Contabilidade compara escopo mais específico.**
Given `consumed_amount` do CNPJ e do tenant atualizados por reporte.
When o control plane avalia o limite.
Then compara o consumo do escopo mais específico contra a cota do mesmo escopo (CNPJ antes de tenant).
And o snapshot reemitido reflete o teto e a política vigentes.
Critério: DER regra 18; sem FK entre bancos, com checagem de escopo no reporte.

---

## 3. Rastreabilidade

| Critério de aceite ADR 0008 | Casos |
| --- | --- |
| Decisão 1 — chave centralizada no cofre; config por tenant OU CNPJ | QA-N-05, QA-P-02, QA-P-03, QA-Q-06, QA-Q-08 |
| Decisão 2 — teto global + sub-cota opcional por CNPJ | QA-Q-06, QA-Q-07, QA-Q-08 |
| Decisão 3 — cota em R$ (não tokens) | QA-Q-01, QA-Q-05 |
| Decisão 4 — estouro `overage` ou `hard_stop` configurável | QA-Q-02, QA-Q-03, QA-Q-04 |
| Decisão 5 — offline desabilita o assistente | QA-O-01, QA-O-02, QA-O-03 |
| Decisão 6 — fatia somente documentação (gancho DER v0.3) | todos os casos (contratos propostos) |
| Decisão 7 — `AI_AGENT.model` código estável, validado contra snapshot, fail-closed | QA-A-01 … QA-A-06, QA-N-01 … QA-N-05 |
| DER regras 15–18 — cota é dinheiro; código é referência; escopo com `NULL`; contabilidade por reporte | QA-Q-01, QA-Q-05, QA-Q-06, QA-Q-07, QA-U-01, QA-U-02, QA-U-03 |
| `AI-ASSISTANT.md` §8 — PII mascarada; prompt/resposta versionados; auditoria | QA-P-01, QA-P-04, QA-P-05 |
| ADR 0003 — RLS e recusa de escopo pelo corpo | QA-R-01 … QA-R-06 |

---

## Anexo A — Golden: snapshot de enforcement de IA

Shape esperado do pacote distribuído pelo control plane para células e relays (mesmo mecanismo do pacote de entitlement e das tabelas fiscais em `SCALING.md` §7: pacote assinado e versionado). **Contrato não implementado** — este é o golden para o qual a implementação deve convergir.

```json
{
  "version": 17,
  "tenant_id": "<uuid>",
  "valid_until": "2026-11-21T17:00:00Z",
  "quota": {
    "scope": "tenant",
    "company_id": null,
    "cap_amount": "100.00",
    "period": "monthly",
    "on_limit_policy": "overage"
  },
  "sub_quotas": [
    {
      "company_id": "<uuid>",
      "cap_amount": "20.00",
      "period": "monthly",
      "on_limit_policy": "hard_stop"
    }
  ],
  "allowlist": [
    { "provider_code": "anthropic", "model_code": "claude-sonnet-4", "fallback_order": 1, "enabled": true },
    { "provider_code": "openai", "model_code": "gpt-4o", "fallback_order": 2, "enabled": true }
  ],
  "signature": "<assinatura>"
}
```

Critérios de contrato: `version` inteiro estritamente monotônico por tenant/escopo; `valid_until` sempre futuro na emissão; `cap_amount` e `overage` em string decimal de 2 casas; `scope = "tenant"` representa `company_id` nulo; a allowlist só contém modelos `active`; `sum(sub_quotas.cap_amount) ≤ quota.cap_amount`; o pacote **não** contém `api_key`, `api_key_ref` nem catálogo completo; a assinatura é verificável contra a chave do control plane; pacote com `version` menor que o local corrente é descartado; pacote com `valid_until` expirado dispara `AI_SNAPSHOT_EXPIRED` (fail-closed).

## Anexo B — Golden: evento `ai.usage.reported` (v1)

Shape esperado do reporte de consumo que a célula envia ao control plane. **Contrato não implementado** — golden de convergência.

```json
{
  "topic": "ai.usage.reported",
  "payload": {
    "version": "v1",
    "report_id": "<uuidv7>",
    "tenant_id": "<uuid>",
    "company_id": "<uuid ou null>",
    "period": "2026-11-01",
    "consumed_amount": "123.45",
    "overage_amount": "0.00",
    "tokens_in": 12000,
    "tokens_out": 3400,
    "source_node": "<relay_node_id>",
    "reported_at": "2026-11-21T14:00:00Z"
  }
}
```

Critérios de contrato: `version` é a string `"v1"` (campo de versão do contrato, distinto da `version` do snapshot); `report_id` é a chave de idempotência (reenvio não duplica); `period` é sempre o primeiro dia do mês (`date`); `consumed_amount`/`overage_amount` em string decimal de 2 casas; `company_id` nulo = agregado do tenant; `tenant_id` precisa ser o da célula autenticada, sob pena de `AI_USAGE_REPORT_INVALID`; o evento é a única via de atualização de `AI_USAGE` (QA-U-02).

## Anexo C — Golden: `AI_USAGE` (contabilidade)

Colunas não nulas: `tenant_id`, `period`, `consumed_amount`, `tokens_in`, `tokens_out`, `updated_at`; `company_id` nulo = agregado do tenant. Upsert por `(tenant_id, company_id, period)` com índice parcial por escopo (DER regra 17). Append/upsert **somente por reporte** — nunca digitado e nunca por FK entre bancos (DER regra 18). `consumed_amount` é o único valor comparado a `cap_amount` (QA-Q-01 … QA-Q-04); `tokens_in`/`tokens_out` são referência de painel e não governam bloqueio.

## Anexo D — Notas de execução

- **Caos/offline:** QA-N-01 e QA-N-03 usam relógio controlado para avançar `valid_until` e para simular entrega fora de ordem; a afirmação é sobre o estado após a expiração/after-ordem, não sobre esperar tempo real.
- **Instrumentação de saída:** QA-Q-02, QA-A-01, QA-A-04, QA-N-01 e QA-O-01 instrumentam a fronteira real do provedor (client HTTP) e exigem zero requisições. Um teste que passa por mock alterado do client não conta como evidência.
- **Isolamento:** todos os casos de cota/allowlist/snapshot têm teste espelho — o mesmo pedido com escopo de outro tenant deve falhar (QA-R-02, QA-R-03, QA-R-04).
- **Dinheiro:** nenhum caso usa `float`; teto, consumo e custo são `NUMERIC(18,2)` e trafegam como string decimal. Calcular em centavos inteiros é aceitável apenas se o contrato de payload estiver reaberto.
- **PII:** os casos de mascaramento inspecionam o prompt versionado persistido e o payload enviado ao provedor; a simples ausência de PII no log não satisface QA-P-01 se o prompt persistido estiver limpo.
- **Contratos não implementados:** os códigos de erro, o envelope, o snapshot (Anexo A), o evento (Anexo B) e o upsert (Anexo C) são o alvo ao qual a implementação deve convergir; divergência de código sem atualização deste documento é falha de contrato.
