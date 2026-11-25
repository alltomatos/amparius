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

O arquivo local cobre a fila da estação e o reinício do cliente, mas não substitui o armazenamento durável do relay. Transporte HTTP, timeout/retry/jitter de rede e UI gráfica permanecem nas fatias correspondentes.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
git diff --check
```

