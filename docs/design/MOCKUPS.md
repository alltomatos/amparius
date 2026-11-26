# Mockups — Amparius v0.1

As telas de loja estão na [galeria do cliente](../../mockups/index.html), com
prévias em JPG. As telas administrativas estão na
[galeria do painel SaaS](mockups/index.html). Todos os mockups são SVG e mostram
a posição do assistente contextual.

Gerar de novo:

```powershell
python tools/mockups/generate.py
python tools/mockups/generate_saas.py   # painel do admin SaaS (control plane)
```

O gerador usa a logomarca oficial (azul `#00274C` / `#0073CF`) e produz `index.html` com a galeria.

## 1. Telas

| Arquivo | Tela | Papel | O que demonstra |
|---|---|---|---|
| `01-painel-do-dia.svg` | Painel do dia | Atendente | agenda, pendências, estado do servidor da loja, resumo do mês |
| `02-atendimento-contrato.svg` | Novo contrato | Atendente (offline) | formulário em etapas, resumo, pagamento e operação sem internet |
| `03-cemiterio-mapa.svg` | Mapa do cemitério | Administrador | quadras e jazigos, status, concessão vencendo, detalhe do jazigo |
| `04-financeiro-titulos.svg` | Contas a receber | Financeiro | KPIs, filtros, tabela com status, total e paginação |
| `03-painel-saas-dashboard.svg` | Visão geral do control plane | Admin SaaS | saúde dos módulos, tenants ativos, licenças a vencer, quota de IA e auditoria recente |
| `05-painel-saas-cotas.svg` | Cotas do tenant | Admin SaaS | licenças por módulo (núcleo × opcional), cota/em uso/disponível, cota esgotada, acessos 12/20 |
| `06-painel-saas-licenca.svg` | Liberar / revogar licença | Admin SaaS | modal com módulo, empresa (CNPJ), vigência, resumo da cota, herança da filial e `LICENSE_POOL_EXHAUSTED` |
| `07-painel-saas-auditoria.svg` | Auditoria de entitlement | Admin SaaS | trilha append-only: quando, módulo, empresa, quem, origem (admin × self-service) e filtros |
| `08-painel-saas-ia-provedores.svg` | Provedores de IA | Admin SaaS | catálogo global, `kind`, status, chave mascarada do cofre, modelos com preço por 1M (estimativa), fallback reordenável, banner de assistente desligado e estado offline |
| `09-painel-saas-ia-modelos-cota.svg` | Modelos e cota de tokens | Admin SaaS | allowlist por escopo (tenant × CNPJ) com `fallback_order` e liga/desliga, cota em R$ (teto global + sub-cota), política `overage`/`hard_stop` e alertas de 80% e 100% |
| `10-painel-saas-ia-consumo.svg` | Consumo de tokens | Admin SaaS | gráfico de custo em R$ por dia/mês, crédito restante, projeção de estouro, trilha `AI_RUN`/`AI_TOOL_CALL` com custo como número principal e tokens como referência |

As telas 05–10 (painel do admin SaaS) ficam em `docs/design/mockups/`; 01–04 e a galeria, em `mockups/`.

### Telas 08–10 · IA no control plane (ADR 0008)

- **Dois níveis de configuração.** Provedor e modelo são catálogo global; a allowlist, a ordem de fallback e a cota são configuráveis por tenant ou por CNPJ (o CNPJ herda o que não definir).
- **Chave nunca aparece.** `api_key_ref` aponta para o cofre; a tela mostra só `cofre · ai/<provedor>` e a chave mascarada `••••••••`.
- **Cota em R$, não em tokens.** O teto global e a sub-cota são `R$`; tokens entram apenas como estimativa de referência (preço de tabela por 1M).
- **Estouro é política, não código.** `overage` (excede cobrando) ou `hard_stop` (bloqueia), configurável por tenant; alertas em 80% e 100%.
- **Offline desliga o assistente.** Sem internet, só textos fixos, sem LLM e sem consumo de cota; o snapshot de enforcement tem `version` + `valid_until` e falha fechado ao expirar.

## 2. Decisões de interface

- **Barra lateral escura** com a marca e a unidade; o conteúdo é claro para leitura prolongada.
- **Uma cor de destaque** (azul) para ação e seleção; verde/vermelho/âmbar só para estado.
- **Densidade de balcão**: 13–14 px de texto, linhas de 44–48 px na agenda e 48 px na tabela.
- **Estado sempre visível**: online, offline com fila, servidor da loja na LAN.
- **Nada de estimativa fiscal na tela**: "tributos calculados na emissão".
- **Assistente no mesmo lugar em todas as telas**, com o contexto e o papel no topo.

## 3. Assistente contextual

O rail mostra três coisas: onde a pessoa está, o que o papel dela permite e o que fazer a seguir. As respostas de exemplo são curtas e citam registro (nota 000412, jazigo C-019, 12 títulos).

Detalhes de contrato: `docs/design/AI-ASSISTANT.md`.

## 4. Próximas telas

1. **Fiscal** — emissão, rejeições, contingência e DANFSe.
2. **Flores** — pedido, composição de arranjo, lote e rota.
3. **Contrato em detalhe** — linha do tempo, documentos, parcelas.
4. **Caixa e fechamento** — abertura, sangria, conferência.
5. **Versão compacta** para tablet e celular do balcão.

## 5. O que ainda não é decisão

- Marca final de tipografia (o mockup usa a fonte do sistema; a logo usa Montserrat).
- Ilustrações do estado vazio e ícones definitivos.
- Tema escuro para uso noturno em cemitério/plantão.

## Correspondência com o MVP

Referências de comportamento: [especificação frontend](frontend-spec.md),
[aceite frontend](../qa/frontend-acceptance.md) e
[ADR da prévia sintética](../adr/0039-preview-sintetico-homologacao.md).

| Referência | Cobertura desta fatia | Evolução pendente |
| --- | --- | --- |
| SaaS 03 · Visão geral | Navegação lateral, logo oficial e indicadores do cenário. Licenças ativas e a vencer acompanham a vigência e o estado da licença opcional. | Agregações administrativas reais e auditoria recente do tenant. |
| SaaS 05–07 · Cotas, licença e auditoria | Lista por empresa, detalhe e trilha do módulo selecionado; alterações sintéticas na aba. | Visão consolidada de cotas do tenant, filtros e exportação de auditoria e paridade visual do fluxo de confirmação. |
| SaaS 08–10 · IA | Visão geral sinaliza que a cota de IA está fora do cenário. | Catálogo e fallback, allowlist e cota em reais por escopo, consumo e trilha de execuções, conforme ADR 0008. |
| Loja 01–02 · Painel do dia e atendimento | Web mostra agenda sintética, pendências, estado da conexão e revisão de um atendimento antes de enfileirar. Desktop tem marca, estado, fila persistida e atendimento por referência de contrato ativo. | Agenda real no desktop, cadastro autorizado de partes e fluxo completo de contrato, pagamento e contingência. |
| Loja 03–04 · Cemitério e financeiro | Mockups de referência para o mapa e as contas a receber. | Navegação e dados operacionais de jazigos, concessões, títulos e filtros, respeitando entitlement e disponibilidade offline. |

O cliente web demonstra a reconexão em memória. A estação Rust usa sua fila
persistida e sinaliza a indisponibilidade de sincronização quando faltam as
dependências. O roteiro web está no
[guia de homologação](../userguide/operacao-staging.md).
