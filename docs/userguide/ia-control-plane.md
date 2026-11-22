# Guia do usuário — Configuração de IA no painel do admin SaaS

Versão 0.1 — 21/11/2026 · Amparius

Este guia explica, em linguagem simples, como o admin SaaS configura a inteligência
artificial (IA) do Amparius: quais provedores estão ligados, quais modelos cada
empresa pode usar, quanto custa e o que acontece quando a cota acaba ou quando
não há internet. Serve tanto para o administrador SaaS quanto para o cliente que
acompanha o consumo.

> A IA do Amparius é o **assistente contextual** que aparece nas telas (rail à
> direita). Ele responde sobre o que está na tela, sugere o próximo passo e prepara
> ações — mas **nunca age sozinho** e sempre respeita o perfil de quem está usando.

---

## 1. Provedores de IA (tela 08 — "Provedores de IA")

### O que é um provedor

Um **provedor** é a empresa que fornece o modelo de IA que o assistente usa
(exemplos fictícios: `openai`, `anthropic`, `deepseek`, `gemini`). Cada provedor
tem um **tipo** (`kind`): *chat* (conversa), *embedding* (busca em documentos) ou
*rerank* (reordenação de resultados). Só o admin SaaS cadastra e mantém provedores.

### Como cadastrar, ativar e desativar

Na tela 08 o admin SaaS vê um catálogo global de provedores com: nome, tipo,
status (ativo/desativado) e a chave mascarada do cofre. Ele pode:

- **Cadastrar** um provedor novo (nome, tipo, chave).
- **Ativar/desativar** um provedor sem apagá-lo (o histórico continua).
- **Reordenar a fila de fallback** arrastando os provedores (ver abaixo).

### Por que a chave fica no cofre

A **chave de API** (a senha do provedor) **nunca é digitada nem exibida** na tela.
Ela vive em um **cofre** (cofre de segredos), e a tela mostra apenas a referência
`cofre → ai/<provedor>` e a chave mascarada `********`. Isso protege a chave de
vazamentos e garante que ninguém a copie. Se precisar trocar a chave, o admin SaaS
atualiza no cofre; a tela não muda de aparência.

### Ordem de fallback

Se um provedor ficar indisponível (fora do ar, limite atingido no lado dele), o
sistema **desce para o próximo provedor da lista** na ordem configurada. Ele nunca
usa um provedor fora da allowlist. A ordem é configurável por tenant ou por CNPJ.

---

## 2. Modelos e cota (tela 09 — "Modelos e cota")

### Allowlist (quais modelos o tenant/CNPJ pode usar)

A **allowlist** é a lista de modelos que um tenant ou um CNPJ específico tem
permissão de usar (ex.: `gpt-4o`, `claude-sonnet-4`). Ela pode ser definida:

- **Por tenant** — vale para todos os CNPJs do grupo.
- **Por CNPJ** — vale só para aquela empresa; o que não for definido herda do tenant.

Cada modelo na allowlist tem um **`fallback_order`** (ordem de tentativa). Se o
primeiro não responder, o sistema tenta o próximo da lista. Modelos podem ser
ligados/desligados sem serem apagados.

### Cota em R$ — teto global + sub-cota por CNPJ

A cota é um **teto de gasto em reais (R$)**, não em tokens. Ela tem dois níveis:

1. **Teto global do tenant** — compartilhado entre todos os CNPJs do grupo. Dá
   previsibilidade à fatura do grupo.
2. **Sub-cota por CNPJ** (opcional) — dentro do teto global. Impede que um CNPJ
   sozinho consuma o orçamento dos demais.

> A sub-cota **desconta do mesmo bolo**: o gasto do CNPJ conta no teto global.
> Se o CNPJ não tiver sub-cota definida, usa o global.

### Política de estouro

Quando a cota é atingida, o comportamento é **configurável** por tenant:

- **`overage`** (exceder com cobrança extra): o assistente continua funcionando e
  o excedente é cobrado à parte.
- **`hard_stop`** (bloquear): o assistente é bloqueado até a cota ser renovada.

### Alertas

O painel avisa em **80%** e **100%** do teto. No caso de `hard_stop`, ao
atingir 100% o assistente é desligado e aparece um aviso na tela.

---

## 3. Consumo (tela 10 — "Consumo de IA")

### Como ler o gráfico de custo em R$

A tela 10 mostra um gráfico de **custo em R$ por dia (ou por mês)**. Cada barra
representa o gasto daquele período. Use-o para ver tendências e picos.

### Crédito restante

Mostra quanto ainda pode ser gasto antes de atingir o teto (global ou da sub-cota,
conforme o escopo selecionado).

### Projeção

A tela calcula, com base no ritmo atual: **"neste ritmo, a cota estoura em X
dias"**. Útil para agir antes do bloqueio.

### Trilha de execuções

Uma tabela lista cada execução do assistente: quem usou, o que pediu, qual modelo
foi usado, quanto custou (em R$) e os tokens como referência. Isso permite
auditar o uso e identificar abusos.

### Tokens são só uma estimativa

Os **tokens** (unidade de texto que o modelo processa) aparecem apenas como
**estimativa de referência**. O número que importa para a fatura é o **valor em
R$**. O preço por token varia por modelo e por provedor, por isso o teto é em R$.

---

## 4. Comportamento offline

Sem internet (relay offline), o **assistente fica indisponível**: apenas dicas
fixas pré-computadas são exibidas, **sem LLM e sem consumo de cota**. Isso porque
sem conexão não é possível aferir o custo nem renovar o teto; consumir cota sem
aferição violaria a contabilidade. Os fluxos críticos do ERP (venda, atendimento,
caixa) **não dependem da IA** e continuam funcionando normalmente.

O assistente informa claramente que está em modo offline e o que ficará pendente
de sincronização.

---

## 5. Glossário

- **Token**: unidade de texto processada pelo modelo (aproximadamente ¾ de uma
  palavra). Aparece apenas como estimativa de referência.
- **Provedor**: empresa que fornece o modelo de IA (ex.: openai, anthropic).
- **Modelo**: versão específica de IA oferecida por um provedor (ex.: gpt-4o).
- **Fallback**: tentativa automática do próximo provedor/modelo da lista quando
  o atual falha.
- **Allowlist**: lista de modelos que um tenant ou CNPJ tem permissão de usar.
- **Cota**: teto de gasto em R$ para um período.
- **Sub-cota**: cota de um CNPJ dentro do teto global do tenant.
- **Overage**: política que permite exceder a cota cobrando o excedente.
- **Hard stop**: política que bloqueia o assistente ao atingir a cota.
- **Cofre**: repositório seguro de segredos (chaves de API) — a chave nunca
  aparece na tela.
- **Tenant**: grupo econômico com vários CNPJs.
- **CNPJ**: empresa individual dentro do tenant.

---

*Este guia reflete as decisões do ADR 0008 e do DER v0.3. Termos técnicos
internos (tabelas, campos) não são apresentados aqui; consulte a documentação
técnica para detalhes de implementação.*
