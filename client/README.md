# Amparius Client

Cliente desktop shell do Amparius no papel de estação.

## Requisitos

- Rust MSRV: 1.85 (edition 2024)

## Executar

```powershell
cargo run
```

O binário aguarda `Ctrl+C` para encerramento gracioso. A interface gráfica será adicionada em fase posterior.

## Modelo CL-01/CL-03

`src/lib.rs` contém o modelo de estado usado pela futura UI:

- `ConnectivityState` separa `online`, `offline` e `contingency`; a operação permitida continua enfileirável nos três estados.
- `ClientQueue` atribui `seq` monotônica por `(tenant_id, node_id)` e mantém a mesma operação em replay.
- A mesma `idempotency_key` com conteúdo diferente falha com `IdempotencyConflict`; dinheiro e fiscal nunca aplicam last-write-wins.
- `ReconciliationOutcome::Conflict` e `FiscalRejected` criam tarefas explícitas de revisão sem carregar payload, resposta remota ou PII.
- `ClientStatus` e `ClientMetrics` expõem apenas contagens, estado e códigos técnicos seguros para observabilidade.
- `ClientQueue::open(path, tenant_id, node_id)` persiste a fila em JSON versionado com SHA-256, escrita temporária/backup e rejeição fail-closed de corrupção ou versão desconhecida.

O arquivo local cobre a fila da estação e o reinício do cliente, mas não substitui o armazenamento durável do relay. A UI gráfica permanece em fatia posterior.

## Transporte HTTP

`src/transport.rs` envia a fila persistida ao relay local no contrato RELAY-OPERATIONS v1:

```rust
use amparius_client::transport::{Credentials, HttpTransport, TransportConfig, sync_pending};

let mut config = TransportConfig::new("http://127.0.0.1:8787")?;
config.timeout = std::time::Duration::from_secs(5);
config.max_attempts = 3;
let transport = HttpTransport::new(config)?;
let credentials = Credentials::new("operator-1", token)?;

let report = sync_pending(&mut queue, &transport, &credentials, amparius_client::unix_now()).await?;
```

- Preserva `(tenant_id, node_id)`, `Idempotency-Key`, `seq` monotônica, `operation_id` e `content_hash`; revalida payload e recusa PII.
- Timeout configurável; retry limitado apenas para timeout/indisponibilidade/5xx, com backoff exponencial e jitter determinístico.
- 409 vira conflito explícito; demais 4xx são rejeição sem retry (fiscal vira `FiscalRejected`, os outros viram `Conflict`).
- Replay do relay (`replayed: true`) é `Duplicate`, sucesso idempotente sem criar operação.
- `master_upsert` só é marcado sincronizado com `authoritative_version`; sem ela, vira revisão.
- Em queda, a execução para após a primeira falha transitória e a operação continua `pending`.
- O transporte é HTTP (loopback ou atrás de TLS local): o token nunca é logado nem persistido, e redirecionamentos são desabilitados.

Nenhuma tela gráfica é implementada nesta fatia.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
git diff --check
```

