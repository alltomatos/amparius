# ADR 0037 — Tela nativa de sincronização e revisão do cliente

Status: aceito em 26/11/2026 — CL-05

Relaciona-se com: ADR 0034 (fila offline persistida), ADR 0035 (transporte HTTP
com retry), ADR 0036 (UI nativa), `docs/contracts/client-sync-review-v1.md` e
`docs/qa/frontend-acceptance.md`.

## Contexto

O cliente já persistia operações offline e executava `sync_pending`, mas o
operador não tinha uma superfície dedicada para conferir o backlog, distinguir
duplicidade de falha ou abrir uma tarefa de revisão. Conflitos de dinheiro e
fiscal não podem ser tratados como sucesso nem resolvidos por `last-write-wins`.

## Decisão

1. A UI nativa `egui/eframe` ganha a tela **CL-05 — Sincronização e revisão**,
   com backlog, contagens do último `SyncReport` e tarefas abertas.
2. O modelo reutiliza `ClientQueue`, `DayPanelView` e `SyncReport`; não cria um
   segundo store, não duplica payload e não cria contrato HTTP novo.
3. **Tentar novamente** chama o fluxo existente de sincronização e só pode
   reapresentar operações que continuam `pending`. A mesma operação preserva
   `operation_id`, `seq`, `idempotency_key` e `content_hash`; nenhuma sequência
   nova é criada.
4. Falha terminal, conflito e rejeição fiscal não são reabertos pela UI. O
   cliente não transforma uma rejeição de negócio em retry automático.
5. **Abrir revisão** navega para um detalhe somente leitura. A tela mostra
   referência opaca, tipo, status e código técnico sanitizado, mas não fecha a
   tarefa, não altera status e não escolhe vencedor.
6. Conflitos financeiros e fiscais permanecem bloqueados e exigem fluxo
   autorizado posterior. O cliente nunca aplica `last-write-wins`, não aplica
   snapshot autoritativo e não sobrescreve o valor local.
7. `tenant_id` e `node_id` permanecem implícitos no `ClientQueue`; a UI não os
   exibe. A validação de tenant e RLS continuam responsabilidade do relay.

## Segurança, LGPD e observabilidade

- A tela não exibe nem registra payload, XML fiscal, resposta crua do relay,
  token, nome, documento, contato ou outro dado pessoal.
- Referências e códigos passam pelas validações/sanitização existentes antes de
  chegar ao modelo visual.
- Logs permanecem limitados a contagens, estados e códigos técnicos; não há
  resolução silenciosa nem vazamento de credencial.
- O detalhe de revisão é explicitamente somente leitura até existir um contrato
  autorizado para decisão e auditoria.

## Alternativas descartadas

- Resetar uma operação falha ou conflito para `pending`: poderia mascarar uma
  rejeição fiscal ou reabrir uma divergência sem decisão autorizada.
- Recriar a operação para tentar novamente: consumiria nova sequência e
  quebraria a idempotência.
- Resolver conflito pelo valor mais recente: perde histórico em dinheiro/fiscal
  e viola o contrato de reconciliação explícita.
- Exibir payload na tela de suporte: amplia exposição de PII e dados fiscais sem
  necessidade operacional.

## Consequências e limites

A estação passa a oferecer visibilidade e encaminhamento seguro para revisão,
sem alterar a autoridade do relay ou do SaaS. A resolução efetiva, aplicação de
snapshot, renovação de credencial, TLS embutido e integração produtiva com SaaS
continuam fora desta fatia.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
git diff --check
```

O contrato e os testes headless são a fonte de comportamento: `client-sync-review-v1.md` e
`client/src/ui/render.rs`.
