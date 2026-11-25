# Observabilidade da outbox — métricas, logs e alertas

Status: vigente desde 2026-11-25 (OUT-01C, issue #30).
Escopo: worker da outbox (`cmd/worker`) e `outbox.Dispatcher`.
Referências: ADR 0009, `docs/contracts/outbox-event-v1.md` §11,
`internal/modules/outbox/observability.go`.

## 1. O que é medido

| Métrica | Tipo | Significado |
| --- | --- | --- |
| `amparius_outbox_cycles_total` | contador | ciclos de dispatch concluídos |
| `amparius_outbox_cycle_errors_total` | contador | ciclos que terminaram com erro agregado |
| `amparius_outbox_cycle_duration_seconds` | histograma | duração do ciclo completo |
| `amparius_outbox_events_claimed_total` | contador | eventos reivindicados (`Claim`) |
| `amparius_outbox_events_succeeded_total` | contador | eventos concluídos e com entrega gravada |
| `amparius_outbox_events_retried_total` | contador | eventos devolvidos à fila com backoff |
| `amparius_outbox_events_failed_total` | contador | eventos em falha terminal (`failed`) |
| `amparius_outbox_events_reaped_total` | contador | leases vencidos fechados por `ReapExpired` |
| `amparius_outbox_backlog_events` | gauge | eventos por `tenant_id` e `status` (`pending`, `processing`, `failed`) |
| `amparius_outbox_backlog_oldest_age_seconds` | gauge | idade do evento mais antigo pendente/em processamento, por `tenant_id` |
| `amparius_outbox_backlog_sample_errors_total` | contador | falhas ao amostrar o backlog (não interrompem entrega) |
| `amparius_outbox_delivery_duration_seconds` | histograma | tempo do handler por evento |

### Limites do gauge de backlog

- Os gauges por tenant são uma **fotografia do último ciclo**; `ReplaceGauges`
  remove tenants que deixaram de ser visitados. Uma série que desapareceu é
  tratada como stale pelo Prometheus, não como zero permanente.
- O ciclo visita no máximo `OUTBOX_MAX_TENANTS` tenants. Com mais tenants
  ativos que o limite, a soma dos gauges **subestima** o backlog. Para alerta
  global, dimensione `OUTBOX_MAX_TENANTS` acima do número de tenants com fila
  ou alerte por `max by (tenant_id)`.
- O gauge não é fonte de verdade: a outbox é. Para auditoria, consulte
  `outbox.event` com papel administrativo (a RLS limita o papel de aplicação).

## 2. Logs estruturados

Todos os logs são JSON (`log/slog`) e nunca incluem payload, `last_error`
bruto, DSN ou segredo.

Campos obrigatórios:

- Linha `outbox ciclo concluido` (Info, ou Warn quando há erro):
  `correlation_id`, `worker_id`, `tenants`, `reaped`, `claimed`, `succeeded`,
  `retried`, `failed`, `backlog_pending`, `backlog_processing`,
  `backlog_failed`, `backlog_oldest_age_seconds`, `duration_ms` e, em erro,
  `erro` redigido.
- Linha de evento (`outbox evento entregue` em Debug, `outbox evento
  reagendado` em Warn, `outbox evento esgotou tentativas` em Error):
  `tenant_id`, `event_id`, `trace_id`, `event_type`, `attempt`,
  `max_attempts`, `correlation_id`, `worker_id`, `duration_ms`.

Convenções:

- `correlation_id` identifica o ciclo de dispatch; se a chamada chegou por HTTP
  e trouxe `X-Request-Id`, o middleware propaga o mesmo valor.
- `trace_id` é o `event_id` (UUIDv7). Ele liga o log de entrega à linha em
  `outbox.event` e à linha em `outbox.delivery` sem carregar dado de negócio.
- Texto de erro passa por `outbox.RedactError` (mesma redação de e-mail,
  CPF/CNPJ, telefone e token aplicada ao `last_error`).
- Sucesso por evento é Debug para não gerar 10 mil linhas por ciclo em Info.
  Para rastrear cada evento: `LOG_LEVEL=debug`.

## 3. Exposição

O worker sobe um endpoint separado, por padrão em `127.0.0.1:9091`:

- `GET /metrics` — exposição Prometheus.
- `GET /healthz` — liveness do próprio servidor de métricas.

Variável de ambiente:

- `OUTBOX_METRICS_ADDR` — `host:porta` (padrão `127.0.0.1:9091`). Valores
  `off`, `disabled`, `none` ou `-` desabilitam. Endereço inválido derruba o
  bootstrap, em vez de subir um worker sem métricas.

O endpoint não deve ser exposto publicamente: mantenha loopback ou uma rede de
observabilidade.

## 4. Regras de alerta (Prometheus)

```yaml
groups:
  - name: amparius-outbox
    rules:
      - alert: OutboxBacklogAlto
        expr: sum(amparius_outbox_backlog_events{status="pending"}) > 100
        for: 10m
        labels: { severity: warning }
        annotations:
          summary: "Backlog da outbox acima de 100 eventos pendentes"
          description: "Soma dos gauges do último ciclo; confira OutboxIdadeAlta por tenant."

      - alert: OutboxIdadeAlta
        expr: max(amparius_outbox_backlog_oldest_age_seconds) > 300
        for: 5m
        labels: { severity: warning }
        annotations:
          summary: "Evento mais antigo pendente há mais de 5 minutos"

      - alert: OutboxIdadeCritica
        expr: max(amparius_outbox_backlog_oldest_age_seconds) > 1800
        for: 5m
        labels: { severity: critical }
        annotations:
          summary: "Evento mais antigo pendente há mais de 30 minutos"

      - alert: OutboxRetryAlto
        expr: sum(rate(amparius_outbox_events_retried_total[5m])) > 0.5
        for: 10m
        labels: { severity: warning }
        annotations:
          summary: "Mais de 0,5 retry/s na outbox"

      - alert: OutboxFalhaTerminal
        expr: increase(amparius_outbox_events_failed_total[5m]) > 0
        labels: { severity: critical }
        annotations:
          summary: "Evento atingiu falha terminal; exige revisão manual"

      - alert: OutboxCicloComErro
        expr: increase(amparius_outbox_cycle_errors_total[5m]) > 0
        for: 5m
        labels: { severity: warning }
        annotations:
          summary: "Ciclos da outbox terminando com erro"

      - alert: OutboxWorkerParado
        expr: increase(amparius_outbox_cycles_total[5m]) == 0 or absent(amparius_outbox_cycles_total)
        for: 5m
        labels: { severity: critical }
        annotations:
          summary: "Nenhum ciclo da outbox em 5 minutos (worker parado ou nunca iniciado)"

      - alert: OutboxEntregaLenta
        expr: histogram_quantile(0.95, sum(rate(amparius_outbox_delivery_duration_seconds_bucket[5m])) by (le)) > 2
        for: 10m
        labels: { severity: warning }
        annotations:
          summary: "p95 do handler da outbox acima de 2s"
```

`OutboxFalhaTerminal` é crítico porque a outbox é a espinha de eventos: um
evento `failed` não é reenviado sozinho. A ação é inspecionar `last_error`
(redigido), corrigir a causa e reenviar com `idempotency_key` nova.

## 5. Comandos reproduzíveis

Subir o worker com métricas e log de evento:

```powershell
$env:LOG_LEVEL = 'debug'
$env:OUTBOX_METRICS_ADDR = '127.0.0.1:9091'
$env:OUTBOX_INTERVAL = '1s'
$env:POSTGRES_DSN = 'postgres://amparius_app:amparius@127.0.0.1:5432/amparius?sslmode=disable'
go run ./cmd/worker
```

Em outro terminal:

```powershell
curl.exe -s http://127.0.0.1:9091/metrics
curl.exe -s http://127.0.0.1:9091/healthz
```

Testes de contrato de log/métrica (sem banco) e de configuração:

```powershell
go test ./internal/platform/observability/... ./cmd/worker/... -count=1 -v
go test ./internal/modules/outbox/... -count=1 -run 'DispatcherLogs|DispatcherRetry|BacklogSampleError|Observability' -v
```

Integração com PostgreSQL real (`amparius_app` `NOSUPERUSER/NOBYPASSRLS`):

```powershell
$env:AMPA_TEST_POSTGRES_DSN = 'postgres://amparius_app:amparius@127.0.0.1:5432/amparius?sslmode=disable'
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = 'postgres://amparius:amparius@127.0.0.1:5432/amparius?sslmode=disable'
go test ./internal/modules/outbox/... -count=1 -v
go test ./... -count=1
go vet ./...
go build ./...
git diff --check
```

Conferência manual do backlog (papel administrativo, fora da RLS de tenant):

```sql
SELECT tenant_id, status, count(*)
FROM outbox.event
WHERE status IN ('pending', 'processing', 'failed')
GROUP BY tenant_id, status
ORDER BY tenant_id, status;
```
