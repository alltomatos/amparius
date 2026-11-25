# ADR 0034 — Persistência local da fila offline do cliente Rust

Status: aceito em 25/11/2026 — CL-03

## Contexto

O contrato `CLIENT-OFFLINE-STATE v1` já definia `tenant_id`, `node_id`, sequência
monotônica, idempotência, hash de conteúdo e revisão de conflito, mas o cliente
mantinha a fila somente em memória. Um reinício durante uma queda de rede poderia
perder operações ou reiniciar a sequência do dispositivo.

## Decisão

- `ClientQueue::open` usa JSON local versionado, schema `1`, para a fila da estação.
- O arquivo inclui identidade, conectividade, sequência final, operações, tarefas de
  revisão e métricas; o contexto de tenant e dispositivo da abertura precisa coincidir
  com o arquivo.
- Um SHA-256 cobre o estado persistido, sem incluir o próprio campo de hash. A abertura
  valida identidade, versão, hash, conteúdo, `operation_id`, sequência e unicidade de
  idempotência; qualquer divergência falha fechado.
- Escritas usam arquivo temporário e substituição com backup recuperável. Enqueue,
  replay, mudança de conectividade e reconciliação só são confirmados em memória após
  a escrita durável; falha de persistência restaura o estado anterior.
- O arquivo não contém observabilidade com payload. PII continua rejeitada antes da
  fila; o hash detecta adulteração, mas não oferece criptografia em repouso.

## Consequências

O cliente reabre a fila sem perder `seq`, replay ou estado de reconciliação. O JSON é
adequado à fatia de estação e à janela local, mas não substitui PostgreSQL/WAL, backup,
concorrência entre processos ou transporte do relay. A evolução incompatível exige
novo schema e migração explícita; versão desconhecida não é interpretada como atual.

## Verificação

```powershell
cargo fmt --manifest-path client/Cargo.toml -- --check
cargo test --locked --manifest-path client/Cargo.toml
cargo clippy --locked --manifest-path client/Cargo.toml --all-targets -- -D warnings
git diff --check
```
