# ADR 0029 — Status fiscal, retry limitado e observabilidade do relay

Status: aceito em 25/11/2026 — RELAY-02C (#52)

## Contexto

As fatias RELAY-02A e RELAY-02B garantiram o envelope Sync v1, a ordem, a
idempotência e a revisão explícita de conflitos. Ainda faltava uma fronteira
operacional clara para falhas transitórias: o relay podia reapresentar uma
operação indefinidamente, enquanto o status mostrava apenas a fila genérica e
não separava a emissão fiscal.

## Decisão

1. Cada submissão do `SyncEngine` conta em `QueuedOperation.attempts`. O limite
   padrão é três tentativas por operação, incluindo a inicial; o limite pode ser
   reduzido ou aumentado pelo construtor de teste/política, mas zero é elevado
   para um.
2. Timeout ou indisponibilidade antes do limite mantém a operação `pending` e
   grava somente um código técnico sanitizado. Ao esgotar o limite, a operação
   vira `failed`, recebe `sync_retry_exhausted:*` e cria uma tarefa de revisão
   deduplicada por `(tenant_id, operation_id)`. Para emissão fiscal, a tarefa é
   `FiscalFailure`; para os demais domínios, `SyncFailure`.
3. `RelayStatus` expõe `queue.retrying`, o agregado `fiscal`,
   `review_tasks_open` e o modo `degraded` quando há falha ou conflito. O
   agregado fiscal deriva apenas das operações locais e nunca autoriza emissão.
4. `GET /relay/v1/review-tasks` permite leitura tenant-scoped das tarefas. Não há
   resolução por HTTP nesta fatia.
5. `SyncReport` informa o ciclo e `SyncEngine::metrics()` mantém contadores
   cumulativos em memória. Métricas não carregam payload, resposta fiscal, PII,
   segredo ou valor de tributação.

## Consequências

- O backlog transitório permanece visível e a falha terminal não desaparece nem
  gera uma nova operação ou sequência.
- O cliente pode distinguir fila offline de relay degradado e mostrar o estado
  fiscal sem inferir regra tributária.
- O retry de rede concreto, timeout, jitter, TLS, adaptador HTTP autenticado e
  autoridade remota continuam responsabilidade do `SyncTransport` futuro; os
  testes desta fatia usam transportes roteirizados.
- As métricas são locais ao processo e não substituem exportação Prometheus ou
  trace distribuído.

## Segurança e LGPD

Tenant continua vindo da credencial autenticada. Códigos de erro são filtrados;
 tarefas e status não incluem payload, resposta fiscal crua, certificado,
 segredo ou PII. Nenhuma alíquota, regra fiscal, documento real ou transmissão é
 inventada por esta decisão.

## Verificação

```powershell
cargo fmt --manifest-path relay/Cargo.toml --check
cargo test --manifest-path relay/Cargo.toml
cargo clippy --manifest-path relay/Cargo.toml --all-targets --all-features -- -D warnings
git diff --check
```
