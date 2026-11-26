# Contrato CLIENT-SYNC-REVIEW v1

Escopo: tela nativa **CL-05 — Sincronização e revisão** do cliente Rust
(`client/`). A tela consome a fila persistida e o último `SyncReport`; não cria
contrato HTTP novo, não recebe payload e não resolve conflito.

Implementação: `client/src/ui/render.rs` (`render_sync` e `render_review`) e
`client/src/ui/app.rs` (navegação e ponte com `ClientQueue`/`sync_pending`).

## Dados visíveis

O backlog pode mostrar somente:

- `seq`, tipo, `operation_id` e `entity_id` como referências opacas validadas;
- status (`pending`, `synced`, `failed`, `conflict`), tentativas e código técnico
  sanitizado;
- contagens do `SyncReport`: sincronizadas, duplicadas, rejeitadas, conflitos,
  retentativas e pendências restantes;
- tarefas abertas por operação, tipo e código sanitizado.

Nunca são exibidos ou registrados pela tela: `tenant_id`, `node_id`, payload,
XML fiscal, resposta crua do relay, token, nome, documento, contato ou outro
dado pessoal. O tenant continua implícito no `ClientQueue`; o relay mantém a
validação de tenant e RLS no seu próprio contexto.

## Estados

| Estado | Apresentação |
| --- | --- |
| sem execução | resultado ainda não executado |
| pendências | backlog ordenado por prioridade e `seq` |
| sincronizando | ação desabilitada e indicador de execução |
| concluído | contagens de aplicadas e duplicadas, além de falhas/conflitos |
| interrompido | código técnico e nova tentativa habilitada se restarem pendências |
| revisão aberta | tipo, operação, código e orientação; somente leitura |

Conflito financeiro e fiscal mantém a operação bloqueada em `conflict` e cria
tarefa de revisão. A tela não aplica `last-write-wins`, não reaplica snapshot e
não altera o valor local.

## Ações

### Tentar novamente

O botão reapresenta, por `sync_pending`, somente operações que continuam
`pending`. A mesma `operation_id`, `seq`, `idempotency_key` e `content_hash` são
reutilizados; nenhuma nova operação ou sequência é criada. Falha terminal,
conflito e rejeição fiscal não são reabertos pela UI.

### Abrir revisão

Navega para o detalhe somente leitura da `ReviewTask`. A ação não muda status,
não fecha tarefa, não altera payload e não escolhe vencedor. A resolução exige
fluxo autorizado posterior e registro próprio.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
git diff --check
```

Testes headless com `egui_kittest` cobrem backlog, contagens de duplicadas,
retentativa após interrupção, abertura de revisão e ausência de payload/PII.

Decisão: `docs/adr/0037-cliente-sync-review.md`. Uso operacional:
`docs/userguide/cliente-sincronizacao-revisao.md`.
