# ADR 0026 — Reconexão do relay e revisão fiscal

Status: aceito em 25/11/2026 — RELAY-01C

## Contexto

O RELAY-01B já gravava contrato e caixa offline, mas ainda não havia uma fronteira
testável para reapresentar a fila após a rede voltar. Uma falha no meio do envio não
pode apagar a operação nem gerar uma nova sequência. Rejeições fiscais não podem ser
tratadas como sucesso nem sobrescritas silenciosamente.

## Decisão

1. `SyncEngine` consome um `SyncTransport` injetado, em ordem de `seq`, filtrado por
   tenant e dispositivo.
2. Falha de transporte deixa a operação `pending`, registra um código técnico sem
   PII e permite nova execução com o mesmo `operation_id`.
3. Sucesso persiste `server_cursor` e muda o estado para `synced` de forma idempotente.
4. Rejeição fiscal muda o estado para `failed` e cria uma `ReviewTask` aberta, com
   código sanitizado e vínculo tenant-scoped. A tarefa é deduplicada por operação.
5. O adaptador de rede é responsável por timeout, retry limitado e jitter. O slice
   implementado não cria sockets nem declara integração SaaS real.

## Consequências

- Queda e reconexão são reproduzíveis sem depender de internet ou serviço externo.
- O replay é observável e não duplica a operação local.
- Falhas fiscais ficam bloqueadas para revisão explícita.
- Ainda falta implementar o adaptador HTTP autenticado do SaaS, com cursor remoto,
  timeout, retry e jitter concretos; isso permanece fora deste slice.

## Verificação

```powershell
cargo fmt --manifest-path relay/Cargo.toml -- --check
cargo test --manifest-path relay/Cargo.toml
cargo clippy --manifest-path relay/Cargo.toml --all-targets --all-features -- -D warnings
```

Contrato: `docs/contracts/relay-sync-v1.md`.
