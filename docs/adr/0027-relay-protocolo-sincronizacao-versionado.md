# ADR 0027 — Protocolo de sincronização versionado

Status: aceito em 25/11/2026 — RELAY-02A

## Contexto

A RELAY-01C entregou a reconexão local com `SyncTransport`, mas o limite de
transporte recebia apenas a operação da fila. Sem um envelope explícito, a versão
do evento, a origem e a sequência ficavam implícitas no formato do payload, e o
lado que aplica não tinha regra declarada para duplicidade, intervalo de sequência
ou replay do lote após queda.

A issue #48 exige eventos versionados, ordem preservada e reaplicação segura. O
protocolo não pode depender de last-write-wins em dinheiro ou fiscal, e uma versão
desconhecida não pode ser interpretada como se fosse a atual.

## Decisão

1. O limite de transporte ganha `SyncEnvelope`, com `protocol_version`,
   `event_version`, `event_id`, `tenant_id`, `origin_node`, `seq`, `kind`,
   `entity_id`, `idempotency_key`, `actor_subject_id`, `content_hash`,
   `occurred_at` e `payload`.
2. `SyncTransport` mantém `submit(&QueuedOperation)` para adaptadores existentes e
   adiciona `submit_versioned(&QueuedOperation, &SyncEnvelope)`, cuja
   implementação padrão delega para `submit`. O adaptador novo sobrescreve o método
   versionado e envia o envelope; nenhum socket é criado nesta fatia.
3. `SyncEnvelope::from_operation` recalcula `content_hash` e `event_id` antes de
   liberar o envelope. Operação ou envelope adulterado falha fechado.
4. A ordem é monotônica por `(tenant_id, origin_node)`. O receptor de referência,
   `SyncReceiver`, aceita somente a próxima sequência, recusa intervalos com
   `out_of_order` e informa `expected_seq`. Não há preenchimento silencioso.
5. Duplicidade é reconhecida por `event_id` e por `(origin_node,
   idempotency_key)`. Replay do mesmo evento devolve `duplicate` com o cursor
   original; mesma posição ou chave com conteúdo diferente devolve `conflict` sem
   sobrescrever.
6. O cursor do receptor é monotônico e só avança em `applied`. Reaplicar o lote
   inteiro após reconexão não duplica nem regride o cursor.
7. Versão de protocolo ou de evento diferente de `1` é recusada com código estável;
   campos novos dentro da mesma versão precisam ser opcionais.

Alternativas descartadas: inferir versão pelo formato do payload (ambíguo e não
autodescritivo); aceitar sequência fora de ordem e aplicar depois (esconde perda de
evento e cria ambiguidade em dinheiro/fiscal); tratar conflito como sucesso
(last-write-wins proibido pelo AGENTS.md).

## Consequências

- O protocolo é testável sem rede, com transporte roteirizado e receptor em
  memória.
- Adaptadores legados continuam compilando; adaptadores novos têm um ponto único
  para enviar o envelope versionado.
- O relay não inventa evento faltante nem aplica evento fora de ordem: a operação
  não é marcada como sincronizada e a reconciliação explícita fica para a
  RELAY-02B.
- O receptor desta fatia é uma referência em memória, vinculada a um tenant. Ele
  não persiste estado, não faz buffer de intervalo e não é o armazenamento do SaaS.
- O adaptador HTTP autenticado, TLS, retry com jitter concreto, persistência do
  cursor no servidor e observabilidade de sync continuam fora deste slice.

## Verificação

```powershell
cargo fmt --manifest-path relay/Cargo.toml -- --check
cargo test --manifest-path relay/Cargo.toml
cargo clippy --manifest-path relay/Cargo.toml --all-targets --all-features -- -D warnings
```

Contrato: `docs/contracts/relay-sync-v1.md`. Código: `relay/src/protocol.rs` e
`relay/src/sync.rs`.
