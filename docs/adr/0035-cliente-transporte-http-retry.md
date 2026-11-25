# ADR 0035 — Transporte HTTP do cliente com retry classificado e jitter determinístico

Status: aceito em 25/11/2026 — CL-04

## Contexto

O ADR 0034 entregou a fila offline persistida do cliente, mas o cliente ainda não
abria sockets. A operação ficava limitada ao arquivo local: uma reconexão não
podia esvaziar a fila, e a política de rede exigida pelo contrato
`CLIENT-OFFLINE-STATE v1` (timeout, retry limitado, jitter) não existia. O relay
já expõe `POST /relay/v1/operations` com idempotência, sequência por dispositivo e
rejeição explícita (contrato `RELAY-OPERATIONS v1`).

## Decisão

1. `client::transport` implementa o adaptador HTTP sobre a fila persistida. O
   contexto `(tenant_id, node_id)` vem do `ClientQueue`, nunca do corpo; as
   credenciais vão nos cabeçalhos `x-amparius-*` do contrato do relay. O corpo
   reenvia `kind`, `entity_id`, `idempotency_key`, `payload`, `seq`,
   `operation_id` e `content_hash`, e a chave também vai em `Idempotency-Key`.
2. Toda submissão revalida o payload pelas mesmas regras do enfileiramento
   (dinheiro em centavos, sem PII) e recalcula hash e identificador. Divergência é
   erro fatal antes de qualquer socket.
3. A classificação de resposta é explícita: `Accepted` (2xx), `Conflict` (409),
   `Rejected` (demais 4xx e 2xx ilegível) e `Transient` (timeout, conexão/leitura,
   5xx). Redirecionamentos são desabilitados para o token não vazar de host.
4. Retry é limitado a `max_attempts` (inclui a inicial, nunca zero) e ocorre
   somente para timeout/indisponibilidade/5xx. 4xx e 409 nunca são repetidos.
   401/403 interrompem sem reconciliar (credencial a renovar); o restante de 4xx é
   rejeição explícita, revisada por tipo. O
   backoff é exponencial com full jitter determinístico derivado de
   `(jitter_seed, operation_id, seq, attempt)`, limitado por `backoff_cap`; o mesmo
   estado gera o mesmo atraso, o que permite testar retry sem relógio global.
5. `sync_pending` aplica cada resposta via `ClientQueue::reconcile`, que persiste
   antes de confirmar em memória. `replayed` vira `Duplicate` (sucesso
   idempotente), sem criar operação nem consumir sequência. Em falha transitória a
   execução para na primeira operação, para não esgotar o orçamento das demais
   durante uma queda; a operação corrente continua `pending` com `attempts`
   incrementado e apenas `sync_retry:*` em `last_error`.
6. `master_upsert` aceito sem `authoritative_version` não é marcado sincronizado:
   vira `Conflict` e revisão, porque o relay não é a autoridade do dado mestre.
7. O transporte é HTTP sem TLS embutido. O relay escuta em loopback ou atrás de
   TLS local, como já exige o contrato de operações; HTTPS direto, proxy
   corporativo e renovação de credencial ficam para fatia posterior. Não há UI
   gráfica nesta fatia.

Alternativas descartadas: retry em qualquer erro (repetiria rejeição de negócio e
mascararia 4xx); jitter com `rand` global (não reproduzível em teste); tratar
`replayed` como nova operação (duplicaria sequência); marcar `master_upsert` como
sincronizado sem versão autoritativa (last-write-wins disfarçado).

## Consequências

- A fila persistida pode ser drenada para o relay com idempotência e ordem
  monotônica; replay do servidor não duplica e reabrir o cliente não reenvia o que
  já foi sincronizado.
- Timeout, retry, 4xx, 409 e duplicidade são verificáveis localmente com um
  servidor HTTP de teste, sem SaaS e sem rede externa.
- O transporte não substitui o sincronizador relay→SaaS, não envia XML fiscal e
  não oferece TLS próprio. `attempts` do `ClientQueue` conta execuções de sync;
  `max_attempts` conta tentativas HTTP dentro de uma execução — os dois limites
  são independentes e documentados.
- A dependência `reqwest` entra sem TLS (`default-features = false`, `json`) para
  manter o build enxuto; adicionar TLS exige decisão e ADR próprios.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
git diff --check
```

Contrato: `docs/contracts/client-offline-state-v1.md`. Código:
`client/src/transport.rs`, integrado a `client/src/lib.rs`.
