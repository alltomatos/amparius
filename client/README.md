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

A fila ainda é somente em memória nesta fatia. Persistência durável, transporte HTTP, timeout/retry/jitter de rede e UI gráfica permanecem nas fatias do relay/cliente correspondentes.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --manifest-path client/Cargo.toml
cargo clippy --manifest-path client/Cargo.toml --all-targets -- -D warnings
```

