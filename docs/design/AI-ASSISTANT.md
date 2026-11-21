# Assistente contextual — contrato de produto

Versão 0.1 · 21/11/2026

## 1. Princípio

O assistente **não é um chat genérico**. Ele é parte da tela: sabe onde a pessoa está, qual registro está aberto e qual é o papel dela. Responde sobre o que está à vista, sugere o próximo passo e prepara ações — nunca age sozinho.

Frase de projeto: *"Ajuda no contexto, com o limite do seu perfil."*

## 2. Contexto enviado em cada mensagem

```json
{
  "screen": "atendimento.contrato.novo",
  "screenTitle": "Atendimento · Contrato novo",
  "route": "/atendimento/contratos/novo",
  "entity": { "type": "contract", "id": null },
  "selection": { "field": "servico", "value": "plano-essencial" },
  "role": "atendente",
  "permissions": ["party.read", "contract.write", "finance.title.read"],
  "tenant": "tnt_...",
  "unit": "loja-centro",
  "mode": "offline",
  "locale": "pt-BR",
  "at": "2026-11-21T15:42:00-03:00"
}
```

Regras do envelope:

- `screen` é um id estável do registro de telas; a UI nunca manda texto solto de contexto.
- `entity` é o registro realmente aberto, não o último visitado.
- `permissions` vem da sessão; o servidor revalida em cada tool call.
- `mode` informa `online`, `offline` ou `contingency`; muda o que pode ser prometido.

## 3. Regras de comportamento

1. **Responder primeiro sobre a tela atual.** Se a pergunta for de outro módulo, dizer onde fica e oferecer o atalho — não inventar contexto.
2. **Nunca expor dado fora do perfil.** Se o papel não permite, explicar o que falta e quem pode liberar.
3. **Nunca estimar imposto, prazo legal ou valor fiscal.** Dado fiscal vem do motor; se não houver cálculo, dizer isso.
4. **Propor, não executar.** Toda escrita passa por confirmação explícita e permissão; a UI mostra exatamente o que será feito.
5. **Mostrar a origem.** Toda resposta com número aponta o registro (ex.: "título #4821") ou a tela de onde veio.
6. **Offline é explícito.** Dizer que a resposta usa a base local e o que ficará pendente de sincronização.
7. **Não pedir dado sensível no chat.** Documento, senha ou cartão não são digitados na conversa.
8. **Uma pergunta por vez quando faltar dado.** Não abrir formulário paralelo.
9. **Registrar tudo.** Pergunta, resposta, ferramenta, resultado e custo ficam auditados; PII é mascarada no log.
10. **Escalar quando não souber.** Oferecer falar com humano ou abrir chamado, em vez de improvisar.

## 4. Registro de telas

Cada tela declara, no código, um bloco de metadados:

```ts
assistant: {
  id: "atendimento.contrato.novo",
  title: "Atendimento · Contrato novo",
  intents: ["explicar_campos", "checklist_documentos", "calcular_taxa", "proxima_etapa"],
  suggestions: ["Ver checklist", "Anexar documentos", "Calcular taxa"],
  entities: ["party", "deceased", "contract", "catalog_item"]
}
```

Sem esse bloco, o rail não aparece. Isso evita assistente genérico em tela sem contrato definido.

## 5. Papel muda a resposta

| Pergunta | Atendente | Financeiro | Administrador |
|---|---|---|---|
| "quanto entrou hoje?" | vê o próprio caixa | vê o consolidado | vê consolidado e por unidade |
| "posso dar desconto?" | vê a regra e o limite | vê impacto no título | vê política e exceções |
| "emitir nota?" | prepara, mas depende de permissão | idem | idem |

O assistente nunca "eleva" permissão por conversa.

## 6. Ciclo de ação

```
pergunta → contexto → ferramenta de leitura → resposta
                                  ↓ (escrita)
                         proposta com diff/resumo
                                  ↓
                    confirmação explícita do usuário
                                  ↓
                     execução com idempotência + auditoria
```

- A proposta mostra o que muda (ex.: "criar título de R$ 1.333,33, vencimento 21/12").
- Cancelar não deixa efeito.
- Repetir a confirmação não duplica (idempotency key).

## 7. Offline

- Só consulta base local; responde com o que existe.
- Ações que dependem do servidor entram na fila e o assistente diz o prazo estimado.
- Se a base local estiver desatualizada, a resposta traz "atualizado em …" e a diferença de tempo.

## 8. Privacidade e limites

- PII de familiares e falecidos é mascarada antes do provedor de modelo.
- Nada de dados de outra unidade ou tenant, mesmo que o modelo "saiba".
- Prompt e resposta ficam versionados; retenção configurável por tenant.
- Toda ferramenta tem allowlist por papel e por tela.

## 9. UI

- Rail fixo à direita, 340 px, recolhível; em telas menores vira gaveta.
- Cabeçalho com o contexto ("Cemitério · Mapa") e o papel.
- Sugestões em chips mudam com a tela; nunca mais de três.
- Estado vazio explica o que ele pode fazer ali.
- Atalho de teclado para abrir/fechar; foco preservado no formulário.

## 10. Critérios de aceite

1. Pergunta fora do escopo do papel recebe recusa clara e caminho alternativo.
2. Nenhuma escrita acontece sem confirmação e sem permissão.
3. Resposta com valor sempre cita o registro de origem.
4. Em modo offline, a resposta informa que é local e o que está na fila.
5. Nenhum dado de outro tenant aparece em teste de isolamento.
6. Log de ferramenta contém entrada, saída, custo e ator, com PII mascarada.
