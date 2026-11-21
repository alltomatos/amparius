# Mockups — Amparius v0.1

Telas em SVG, com o assistente contextual em todas. Arquivos em `mockups/`; prévias em JPG em `mockups/previews/`.

Gerar de novo:

```powershell
python tools/mockups/generate.py
```

O gerador usa a logomarca oficial (azul `#00274C` / `#0073CF`) e produz `index.html` com a galeria.

## 1. Telas

| Arquivo | Tela | Papel | O que demonstra |
|---|---|---|---|
| `01-painel-do-dia.svg` | Painel do dia | Atendente | agenda, pendências, estado do servidor da loja, resumo do mês |
| `02-atendimento-contrato.svg` | Novo contrato | Atendente (offline) | formulário em etapas, resumo, pagamento e operação sem internet |
| `03-cemiterio-mapa.svg` | Mapa do cemitério | Administrador | quadras e jazigos, status, concessão vencendo, detalhe do jazigo |
| `04-financeiro-titulos.svg` | Contas a receber | Financeiro | KPIs, filtros, tabela com status, total e paginação |

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
