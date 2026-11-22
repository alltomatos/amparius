# Entitlement por licença — fluxos e estados de UI

Versão 0.1 · 21/11/2026 · Designer de Produto · repositório em fase de documentação

Referências: ADR 0007 (`docs/adr/0007-entitlement-licenca.md`), DER §5 (`docs/data/DER.md`), PRD RF-100 a RF-105 (`docs/prd/PRD.md`), QA `docs/qa/entitlement-acceptance.md`, `docs/design/MOCKUPS.md`, `docs/design/AI-ASSISTENT.md`, ADR 0002/0005/0006.

Este documento desenha **fluxos, telas e estados** do entitlement por licença. Não define regra fiscal, não mostra valor de tributo e não descreve implementação. Placeholders usam "Funerária Exemplo" e "CNPJ 00.000.000/0001-00"; nenhum dado real.

---

## 1. Princípios

- **Reaproveita as convenções de `MOCKUPS.md`:** barra lateral escura, uma cor de destaque (azul), densidade de balcão, estado de conexão sempre visível e "nada de estimativa fiscal na tela".
- **Licença é conceito administrativo, não jurídico.** A tela fala de cota, módulo, empresa e vigência. Nunca de alíquota, CST, NCM, CFOP ou código de serviço.
- **Momento emocional do setor.** Poucas telas, poucos cliques; linguagem humana; o atendimento não para por causa de licença.
- **Offline é explícito** (`AI-ASSISTENT.md` §7). A UI distingue "sem internet" de "módulo bloqueado por licença" e de "bloqueio por expiração de cache".
- **Cor nunca é o único sinal** (WCAG 2.2 AA). Todo estado tem ícone + texto + ação.
- **Acesso por papel.** O que o admin SaaS vê não é o que o dono do tenant vê nem o que o atendente vê.
- **Auditoria é produto, não log.** Quem ligou, quando e por qual origem aparece em tela.

---

## 2. Vocabulário e modelo mental

| Termo | O que é | Na tela |
|---|---|---|
| **Tenant** | grupo econômico; pode ter várias empresas | seletor no topo do painel e do self-service |
| **Empresa (CNPJ)** | entidade legal operadora; onde o entitlement amarra | cartão com nome e CNPJ |
| **Filial** | estabelecimento da mesma empresa | linha "herda da empresa", sem licença própria |
| **Licença** | 1 licença = 1 módulo em 1 empresa (CNPJ) | chip "1 de 2 usadas" |
| **Cota por módulo** | total de licenças que o admin SaaS liberou por módulo | coluna "Cota" |
| **Cota de usuários** | acessos do tenant | barra "12 / 20" |
| **Módulo núcleo** | `identity`, `party`, `catalog`, `finance`, `fiscal`, `sync`, `reporting` | selo "Núcleo", sem ação de licença |
| **Módulo opcional** | `funeral`, `cemetery`, `cremation`, `floriculture`, `inventory`, `documents`, `ai` | linha com ação de ativar/desativar |
| **Vigência** | `valid_from` / `valid_to` da licença | "vigência até 31/12/2026" |

Herança (ADR 0007 §7):

```
EMPRESA (CNPJ raiz) ── tem ──> LICENÇA por módulo
        │
        └── FILIAL (mesmo CNPJ raiz) ── herda ──> o entitlement da empresa
```

A filial **não recebe licença própria**; a tela mostra isso em texto, não só omitindo o botão.

---

## 3. Convenções de tela e estados

### 3.1 Cabeçalho presente em todas as telas

```
┌────────────────────────────────────────────────────────────────────────┐
│ AMPARIUS   [Empresa: Funerária Exemplo · CNPJ 00.000.000/0001-00]     │
│ Papel: dono do tenant                         [● online / ○ offline]   │
└────────────────────────────────────────────────────────────────────────┘
```

- O seletor de empresa é o contexto de escopo (QA-R-01: `tenant_id`/`company_id` vêm da sessão, nunca do corpo).
- O estado de conexão é sempre visível: **online**, **offline**, **servidor da loja na LAN**.

### 3.2 Estados base (toda tela tem)

| Estado | Gatilho | O que aparece | Ação oferecida |
|---|---|---|---|
| **Carregando** | requisição em curso | skeleton com a forma da tela | nenhuma |
| **Vazio** | lista sem itens | ilustração neutra + explicação + CTA | "Criar/ver" o primeiro item |
| **Erro** | falha não esperada | mensagem simples + causa não sensível | "Tentar de novo" |
| **Bloqueado por licença** | módulo sem licença ativa | aviso com motivo e caminho | "Falar com o admin" / "Ver planos" |
| **Expiração de cache** | relay com snapshot vencido | aviso distinto de "sem internet" | "Reconectar" / "Verificar licença" |
| **Offline** | sem rede | estado na barra + fila visível | "Operar local" |
| **Sucesso** | mudança aplicada | confirmação com o que mudou | próximo passo |

### 3.3 Regras de linguagem

- Fala em português claro: "Este módulo está bloqueado porque a licença acabou. Fale com o admin da sua conta ou veja um plano com mais licenças."
- Códigos de erro aparecem como **apelido** (`LICENSE_NOT_AVAILABLE` → "sem licença disponível"), nunca crus na mensagem principal (o código fica em "detalhes" ou no log).
- Nenhum valor fiscal: a tela de licença fala de cota e vigência.

---

## 4. Fluxo 1 — Painel do admin SaaS (control plane)

**Ator:** admin SaaS. **Entrada:** painel do control plane → tenant. **Objetivo:** ver cotas, distribuir/revogar licenças e auditar.

### 4.1 Passos

1. O admin abre o **Painel de cotas** e escolhe o tenant (grupo econômico).
2. Vê **licenças por módulo** (cota, em uso, disponível) e a **cota de usuários**.
3. Para liberar, clica em **+ liberar** num módulo opcional; escolhe a **empresa (CNPJ)** e a **vigência**.
4. Confirma. O sistema valida a cota; se faltar, mostra o estado de erro (§4.3).
5. Para revogar, clica em **revogar** na licença; a mudança é aplicada e auditada.
6. Abre a **Auditoria** para ver quem ligou/desligou, quando e por qual origem.

### 4.2 Telas

**T1 — Painel de cotas do tenant**

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ AMPARIUS · Control plane            admin.saas   [SaaS online]        ┃ ┈┈  │
├───────────────┬──────────────────────────────────────────────────────────────┤
│ Painel        │  Cotas — Funerária Exemplo (grupo)              [trocar ▾]   │
│ ▸ Tenants     │                                                              │
│ ▸ Empresas    │  ┌─ Licenças por módulo ─────────────────────────────────┐  │
│ ▸ Módulos     │  │ Módulo      Tipo    Cota  Em uso  Disponível  Ação     │  │
│ ▸ Auditoria   │  │ cemetery    opc.     2      1        1       [+ liberar]│ │
│               │  │ funeral     opc.     2      2        0       [esgotada] │ │
│               │  │ cremation   opc.     1      0        1       [+ liberar]│ │
│               │  │ floricult.  opc.     3      2        1       [+ liberar]│ │
│               │  │ inventory   opc.     1      0        1       [+ liberar]│ │
│               │  │ documents   opc.     1      1        0       [esgotada] │ │
│               │  │ ai          opc.     2      0        2       [+ liberar]│ │
│               │  │ identity    núcleo  —      —        —       [sempre ligado]│
│               │  │ ...                                                      │ │
│               │  └──────────────────────────────────────────────────────────┘  │
│               │  ┌─ Acessos ──────────────────────────────────────────────┐  │
│               │  │ Usuários  12 / 20   [adjustar cota]                    │  │
│               │  └──────────────────────────────────────────────────────────┘  │
└───────────────┴──────────────────────────────────────────────────────────────┘
```

**T2 — Liberar / revogar licença (modal)**

```
┌──────────────────────── Liberar licença ────────────────────────┐
│ Módulo:  [cemetery ▾]                                          │
│ Empresa: [Funerária Exemplo · CNPJ 00.000.000/0001-00 ▾]       │
│ Vigência: de [01/12/2026] até [31/12/2026]                     │
│                                                            │
│ Cota do módulo: 2   Em uso: 1   Disponível: 1               │
│ Esta licença habilita o módulo nesta empresa. Filiais        │
│ dela herdam automaticamente.                                 │
│                                                            │
│                              [Cancelar]  [Confirmar licença] │
└────────────────────────────────────────────────────────────────┘
```

**T3 — Auditoria de entitlement**

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ Auditoria — Funerária Exemplo (grupo)                          [exportar]     │
├──────────────────────────────────────────────────────────────────────────────┤
│ Quando            Módulo      Empresa                Quem        Origem      │
│ 21/11 14:02       cemetery    Funerária Exemplo      admin.saas  admin       │
│ 21/11 11:20       funeral     Funerária Exemplo      ronaldo     self-service│
│ 20/11 18:44       floricult.  Floricultura Exemplo   ronaldo     self-service│
│ ...                                                                       │
│                                                                            │
│ Filtro: [módulo ▾] [origem ▾] [período ▾]                                  │
└──────────────────────────────────────────────────────────────────────────────┘
```

### 4.3 Estados e erros

| Situação | Comportamento | Referência |
|---|---|---|
| Carregando | skeleton das tabelas | — |
| **Vazio** (tenant sem nenhuma licença atribuída) | "Este grupo ainda não distribuiu licenças. Libere a primeira abaixo." + CTA | RF-102 |
| **Cota esgotada** | ao confirmar licença sem cota disponível: `LICENSE_POOL_EXHAUSTED` → "As 2 licenças de `cemetery` já estão em uso. Encerre ou reatribua uma licença antes de liberar outra." | QA-S-04 |
| Revogação | confirmação mostra o efeito: "A empresa perde o acesso ao módulo; os dados permanecem." | ADR 0007 §2 |
| Admin sobrepõe | o estado do control plane é a fonte da verdade; self-service não reativa sem nova licença | QA-S-02 |
| Auditoria | toda mudança grava `ENTITLEMENT_EVENT` (quem, quando, origem) | QA-S-03, QA-M-01 |
| Control plane indisponível | estado de erro com "Tentar de novo"; o painel não faz escrita em modo degradado | — |

---

## 5. Fluxo 2 — Self-service do cliente (dono do tenant)

**Ator:** dono do tenant. **Entrada:** portal → "Módulos da minha empresa". **Objetivo:** ver e distribuir os módulos por empresa (CNPJ).

### 5.1 Passos

1. O dono abre **Empresas do tenant** e escolhe uma empresa (CNPJ).
2. Vê três grupos: **Ativos**, **Bloqueados** e **Disponíveis** (da cota do tenant).
3. Para ativar, clica em **Ativar** num módulo disponível; o modal mostra que **1 licença será consumida**.
4. Confirma. Se houver licença, o módulo fica ativo e a cota é atualizada.
5. Se **não houver licença**, a UI explica o motivo em linguagem simples e aponta o caminho (falar com o admin SaaS / upgrade de plano).
6. Para desativar, clica em **Desativar**; o módulo sai do menu e das ações, mas os dados permanecem.
7. Empresas com **filiais** mostram que as filiais herdam, sem licença própria.

### 5.2 Telas

**T1 — Empresas do tenant**

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ AMPARIUS · Portal do cliente      Funerária Exemplo · CNPJ 00.000.000/0001-00│
│ Papel: dono do tenant                                    [● online]          │
├──────────────────────────────────────────────────────────────────────────────┤
│ Empresas do grupo                                                             │
│                                                                              │
│ ┌────────────────────────────┐  ┌────────────────────────────┐              │
│ │ Funerária Exemplo          │  │ Floricultura Exemplo        │              │
│ │ CNPJ 00.000.000/0001-00    │  │ CNPJ 00.000.000/0002-00    │              │
│ │ 5 módulos ativos           │  │ 2 módulos ativos           │              │
│ │ 2 filiais (herdam)         │  │ 1 filial (herda)           │              │
│ │              [Ver módulos] │  │              [Ver módulos] │              │
│ └────────────────────────────┘  └────────────────────────────┘              │
└──────────────────────────────────────────────────────────────────────────────┘
```

**T2 — Módulos da empresa**

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ Módulos — Funerária Exemplo · CNPJ 00.000.000/0001-00        [trocar empresa] │
├──────────────────────────────────────────────────────────────────────────────┤
│ Licenças do grupo: 6 de 10 em uso                                             │
│                                                                              │
│ Ativos                                                          6             │
│   funeral        contrato, agenda, ordem de serviço        [Desativar]      │
│   cemetery       mapa, jazigos, concessões                 [Desativar]      │
│   finance        títulos, caixa, conciliação               [núcleo]         │
│   ...                                                                         │
│                                                                              │
│ Bloqueados por licença                                        1             │
│   documents      licença vigente acabou                       [Ver plano]    │
│                                                                              │
│ Disponíveis (da cota do grupo)                                2             │
│   floriculture   estoque, arranjos, pedidos                  [Ativar]        │
│   ai             copiloto por módulo                         [Ativar]        │
│                                                                              │
│ Filiais                                                                       │
│   Filial Centro · CNPJ 00.000.000/0001-01 — herda estes módulos, sem        │
│   licença própria.                                                            │
└──────────────────────────────────────────────────────────────────────────────┘
```

**T3 — Confirmar ativação**

```
┌────────────── Ativar módulo ──────────────┐
│ Módulo:  floriculture                      │
│ Empresa: Funerária Exemplo                 │
│                                            │
│ Será consumida 1 licença do grupo.         │
│ Licenças de floricultura: 3 em uso,        │
│ 1 disponível.                              │
│                                            │
│ A Filial Centro passa a usar este módulo   │
│ automaticamente.                           │
│                                            │
│              [Cancelar]  [Ativar módulo]   │
└────────────────────────────────────────────┘
```

**T4 — Falta de licença (`LICENSE_NOT_AVAILABLE`)**

```
┌────────────────────────────┐
│ ⚠  Sem licença disponível  │
│                            │
│ Este grupo já usou todas   │
│ as licenças de "cemetery". │
│ Para ativar, você pode:    │
│                            │
│  1. Falar com o admin da   │
│     sua conta e pedir      │
│     mais uma licença.      │
│  2. Ver um plano com mais  │
│     licenças.              │
│                            │
│  [Falar com o admin]       │
│  [Ver planos]   [Fechar]   │
└────────────────────────────┘
```

### 5.3 Estados e erros

| Situação | Comportamento | Referência |
|---|---|---|
| Carregando | skeleton dos grupos | — |
| **Vazio** (grupo sem módulos disponíveis) | "Não há módulos disponíveis para ativar. Fale com o admin da sua conta." | RF-101/102 |
| **Falta de licença** | `LICENSE_NOT_AVAILABLE` → T4: motivo simples + caminho; o módulo **permanece desativado** | QA-S-01 |
| Admin sobrepõe | se o admin revogou, o dono não reativa sem nova licença | QA-S-02 |
| Módulo bloqueado | módulo com licença vencida/inativa aparece em "Bloqueados", não em "Ativos" | ADR 0007 §2 |
| Filial | filial herda; não há ação de licença na filial | QA-M-02 |
| Desativar | confirmação: "O módulo sai do menu. Os dados continuam lá." | ADR 0007 §2 |
| Reativar | com licença nova, o uso volta | QA-D-02 |

---

## 6. Fluxo 3 — Bloqueio nas três camadas (operador/relay)

**Ator:** atendente/operador. **Objetivo:** entender por que o módulo não aparece e o que fazer. O bloqueio é aplicado em **UI, backend e relay**, sempre derivando da mesma fonte (ADR 0007 §3).

### 6.1 Mapa das camadas

| Camada | O que bloqueia | O que a UI mostra |
|---|---|---|
| **UI** | módulo sem licença some do menu e das ações | item ausente + estado na barra (QA-E-01) |
| **Backend** | API nega endpoint/evento/IA antes de tocar o dado | erro traduzido (`ENTITLEMENT_MODULE_NOT_LICENSED`) |
| **Relay (cache válido)** | bloqueia a operação local antes de gravar | "módulo bloqueado por licença" (QA-E-05) |
| **Relay (cache expirado, offline)** | fail-closed por TTL | "bloqueio por expiração de cache" (QA-P-03) |
| **Reativação** | novo snapshot/versão restaura | menu e uso voltam (QA-D-02) |

### 6.2 Telas

**T1 — Menu sem o módulo (QA-E-01)**

```
┌──────────────────────────────────────────────────────────────┐
│ AMPARIUS                              [● online / ○ offline] │
├──────────────┬───────────────────────────────────────────────┤
│ Balcão       │  Painel do dia                                 │
│ Atendimento  │                                               │
│ Financeiro   │  (o item "Cemitério" não renderiza no menu)   │
│ Flores       │                                               │
│ Relatórios   │                                               │
│              │                                               │
│ ──           │                                               │
│ Módulos      │  Catálogo de módulos do CNPJ atual não lista  │
│ com licença  │  "cemetery".                                  │
└──────────────┴───────────────────────────────────────────────┘
```

**T2 — Módulo bloqueado por licença (cache válido)**

```
┌──────────────────────────────────────────────────────────────┐
│ ⚠  Módulo bloqueado por licença                              │
│                                                              │
│ "Cemitério" está desativado para esta empresa. O menu e as   │
│ ações foram ocultados.                                       │
│                                                              │
│ Para reativar, fale com o admin da sua conta.                │
│                                                              │
│ [Ver módulos da empresa]                        [Fechar]     │
│                                                              │
│ Detalhes: licença não ativa no CNPJ desta empresa.           │
└──────────────────────────────────────────────────────────────┘
```

**T3 — Offline: bloqueado por expiração de cache (não é "sem internet")**

```
┌──────────────────────────────────────────────────────────────┐
│ Estado da conexão:  ● offline · servidor da loja na LAN       │
│                                                              │
│ ⚠  Módulo bloqueado                                             │
│ A licença de "funeral" venceu no cache local.                │
│ Reconecte para atualizar. Enquanto isso, o módulo segue     │
│ bloqueado.                                                   │
│                                                              │
│ Isto NÃO é falta de internet: é a validade do cache de       │
│ licenças. Nenhuma ação local desbloqueia.                    │
│                                                              │
│ [Tentar reconectar]                                         │
│ Detalhes: ENTITLEMENT_CACHE_EXPIRED                          │
└──────────────────────────────────────────────────────────────┘
```

**T4 — Reativação restaura**

```
┌──────────────────────────────────────────────────────────────┐
│ ✓  Módulo reativado                                          │
│ "Cemitério" voltou para o menu e para as ações.              │
│ Os dados anteriores continuam intactos.                      │
└──────────────────────────────────────────────────────────────┘
```

### 6.3 Estados e erros

| Situação | Comportamento | Referência |
|---|---|---|
| Menu sem módulo | item não renderiza; catálogo do CNPJ não lista o módulo | QA-E-01 |
| API nega | 403 `ENTITLEMENT_MODULE_NOT_LICENSED` / `ENTITLEMENT_LICENSE_NOT_ACTIVE`, traduzido na UI | QA-E-02, QA-E-03, QA-E-04 |
| Relay com cache válido | bloqueia local antes de gravar; sem `SYNC_OPERATION` de escrita; UI sinaliza "bloqueado por licença" | QA-E-05 |
| **Expiração de cache** | fail-closed com `ENTITLEMENT_CACHE_EXPIRED`; UI **distingue** de "sem internet"; não há caminho que desbloqueie local | QA-P-03 |
| Propagação | invalidação por versão monotônica + `entitlement.revoked` prioritária | QA-P-01, QA-P-02 |
| Sem emissão fiscal pós-revogação | barrada antes do transporte, sem XML e sem chamada à SEFAZ | QA-P-04 |
| Reconciliação | operações pendentes do módulo rejeitadas/revisadas; módulos não revogados seguem | QA-P-05 |
| Desativar não apaga | contagem de contratos permanece; nada de `deleted_at` | QA-D-01 |
| Reativar restaura | `POST` volta a 201; dados intactos | QA-D-02 |
| Leitura congelada | `fiscal`/`reporting` leem histórico com módulo desligado; escrita nova é barrada | QA-D-03, QA-D-04 |

---

## 7. Tabela consolidada de estados (UX)

| Estado | Onde aparece | Mensagem-resumo | Ação | QA |
|---|---|---|---|---|
| Carregando | todas | skeleton | — | — |
| Vazio | painel admin, self-service | "Nada por aqui ainda" + CTA | criar/ver | RF-102 |
| Erro genérico | todas | "Algo deu errado" + tentar de novo | retry | — |
| Cota esgotada | admin | "Licenças do módulo esgotadas" | reatribuir | QA-S-04 |
| Sem licença disponível | self-service | "Sem licença disponível" | falar com admin / ver plano | QA-S-01 |
| Bloqueado por licença | menu, API, relay | "Módulo bloqueado por licença" | ver módulos | QA-E-01, QA-E-05 |
| Expiração de cache | relay offline | "Licença venceu no cache local" | reconectar | QA-P-03 |
| Sem internet | barra/relay | "Sem internet — operando local" | operar local | RF-070 |
| Reativado | menu/portal | "Módulo reativado" | usar | QA-D-02 |

---

## 8. Acessibilidade e LGPD

- **WCAG 2.2 AA** nas telas críticas (painel admin, self-service, estados de bloqueio).
- Contraste de texto ≥ 4,5:1; ícones com rótulo; foco visível e ordem de tabulação lógica.
- Cor nunca é o único indicador: bloqueado tem ícone + texto; erro tem texto + ação.
- Navegação por teclado nos modais e seletores; foco preservado ao voltar.
- **PII:** nenhum dado real em placeholder; nome de falecido/familiar não aparece em tela de licença.
- Código de erro aparece em "detalhes", nunca como mensagem principal.
- Nenhuma regra fiscal é exibida; a tela de licença fala de cota e vigência.

---

## 9. Critérios de aceite (UX)

| # | Critério | Evidência |
|---|---|---|
| 1 | Menu e ações do módulo sem licença não renderizam | QA-E-01 |
| 2 | API nega endpoint/evento/IA com erro traduzido | QA-E-02, QA-E-03, QA-E-04 |
| 3 | Self-service sem licença falha com explicação e caminho | QA-S-01 |
| 4 | Admin SaaS sobrepõe o estado | QA-S-02 |
| 5 | Auditoria visível com quem/quando/origem | QA-S-03 |
| 6 | Relay bloqueia com cache válido e sinaliza "bloqueado por licença" | QA-E-05 |
| 7 | Offline com TTL expirado bloqueia e **distingue** de "sem internet" | QA-P-03 |
| 8 | Reativação restaura menu e uso | QA-D-02 |
| 9 | Filial herda e aparece como herança na tela | QA-M-02 |
| 10 | Toda tela tem estado de erro, vazio e (quando aplicável) offline | este documento, §3 |

---

## 10. Fora de escopo

- Cálculo de tributo, alíquota, CST, NCM, CFOP ou código de serviço — vêm de tabela oficial datada (ADR 0004) e **não aparecem nas telas de licença**.
- Cobrança e gestão de planos — a licença grava vigência como gancho, mas cobrar é outra fase.
- Implementação de código: este documento é fluxos e estados; a tela nasce daqui, não o contrário.
