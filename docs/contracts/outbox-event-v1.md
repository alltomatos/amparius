# Contrato de evento da outbox — v1

Status: vigente desde 2026-11-25 (fatia OUT-01A, issue #28).
Escopo: `outbox.event` e `outbox.event_attempt` no PostgreSQL da célula.
Referências: ADR 0003 (multitenancy/RLS), ADR 0009 (outbox/dispatch),
`migrations/0007_outbox.sql`, `migrations/0025_outbox_payload_guard.sql`.

## 1. Invariante transacional

Todo evento de domínio é gravado **na mesma transação** do agregado que o
produz, via `outbox.Repository.AppendTx(ctx, tx, event)`.

- Não existe evento sem o dado de negócio que ele descreve.
- Não existe dado de negócio publicado sem o evento correspondente: se o
  `AppendTx` falhar, a transação inteira é desfeita.
- Um replay da mesma operação não duplica nem o agregado nem o evento.

O `Append` (sem transação externa) existe para operações administrativas e
testes; módulos de negócio usam `AppendTx`.

## 2. Envelope

| Campo | Regra |
| --- | --- |
| `id` | `uuidv7`, gerado no banco. Não é referência de negócio. |
| `tenant_id` | Obrigatório; sempre o tenant do contexto (`app.tenant_id`). Divergência falha com `ErrTenantMismatch`. |
| `aggregate_type` | `<schema>.<entidade>` (ex.: `funeral.contract`), minúsculo com `_`. |
| `aggregate_id` | UUID do agregado; é a referência opaca usada pelo consumidor. |
| `event_type` | `<módulo>.<entidade>.<ação>`, regex `^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`. |
| `event_version` | Inteiro positivo; versão do **payload daquele `event_type`**. |
| `payload` | Objeto JSON, até 64 KiB, conforme §4. |
| `idempotency_key` | Até 200 caracteres, única por tenant. |
| `status` | `pending` → `processing` → `processed` \| `pending` (retry) \| `failed`. |
| `attempts` / `max_attempts` | `attempts` avança uma unidade por claim; `max_attempts` padrão 10, teto 1000. |
| `next_attempt_at` | Lease do claim ou agenda do retry. |
| `last_error` | Texto truncado em 1000 runes, sem PII. |
| `created_at` / `processed_at` | `timestamptz` UTC; `processed_at` só existe em `processed`. |

A trilha `outbox.event_attempt` é append-only: `claimed`, `succeeded`, `retry`,
`failed`, única por `(tenant_id, event_id, attempt, outcome)`. UPDATE e DELETE
são bloqueados por trigger.

## 3. Idempotência

- Chave única: `UNIQUE (tenant_id, idempotency_key)`.
- Mesmo conteúdo (aggregate, tipo, versão e payload) → devolve o evento
  original, sem nova linha.
- Chave reutilizada com conteúdo diferente → `outbox.ErrIdempotencyConflict`
  (nunca sobrescreve).
- A chave é derivada da operação de negócio, de forma determinística, para que
  relay/retry reproduzam o mesmo valor. Exemplos em produção:
  - `funeral.contract.create:<Idempotency-Key do cliente>`
  - `funeral.contract.status:<contract_id>:<novo_status>`

## 4. Payload: sem PII

O evento carrega **referências e fatos**, não o cadastro. Permitido:

- UUIDs de agregados e entidades (`*_id`, `*_ids`, `*_ref`);
- enums/estados (`status`, `from_status`, `to_status`);
- dinheiro em centavos (`*_amount_cents`, inteiro);
- datas e booleanos.

Proibido (bloqueado no Go e no banco):

- chaves de PII direta: `name`, `full_name`, `social_name`, `cpf`, `cnpj`,
  `rg`, `email`, `phone`, `address`, `birth_date`, `notes`, `password`,
  `token`, entre outras da lista de `0025`;
- valores que sejam e-mail, sequência de 10 a 14 dígitos sem letras
  (CPF/CNPJ/telefone) ou texto livre acima de 512 caracteres;
- payload fora de objeto JSON, com mais de 8 níveis, ou acima de 64 KiB.

`outbox.ValidatePayloadPrivacy` valida em Go antes do INSERT; o trigger
`outbox.validate_event` chama `outbox.assert_payload_safe` no banco, então a
regra vale para qualquer caminho de escrita.

Quem precisa do dado pessoal (nome do falecido, contato da família) lê o
agregado sob RLS e pela API do módulo, nunca pela outbox.

## 5. RLS e grants

- `outbox.event` e `outbox.event_attempt` têm RLS por `app.tenant_id` com
  `USING` e `WITH CHECK` iguais.
- `amparius_app`: `USAGE` no schema; `SELECT, INSERT, UPDATE` em `event`;
  `SELECT, INSERT` em `event_attempt`; `EXECUTE` em `pending_tenants` e em
  `assert_payload_safe` (revogada de `PUBLIC` na `0025`).
  Sem `DELETE`, sem `SUPERUSER`, sem `BYPASSRLS`.
- `outbox.pending_tenants(integer)` é a única leitura cross-tenant: função
  `SECURITY DEFINER` que devolve apenas `tenant_id` de quem tem evento
  elegível, executada por `db.WithSystemTx`.
- Claims, sucesso, retry e falha rodam sempre por tenant, sob RLS.

## 6. Entrega

- Semântica **at-least-once** por tenant; o handler é idempotente por
  `(event_id, attempt)`.
- Ordem por `next_attempt_at, created_at, id` dentro do tenant; não há ordem
  global entre tenants nem entre aggregates distintos.
- Falha grava `last_error`, aplica backoff exponencial de 2s a 1h e volta para
  `pending`; ao esgotar `max_attempts` vira `failed`. `last_error` é redigido
  antes de persistir (e-mail, CPF/CNPJ, telefone e segredos viram `[redigido]`),
  porque a trilha de auditoria não pode virar cadastro nem cofre.
- Lease vencido que já esgotou tentativas é fechado por `ReapExpired`.
- A outbox é a fonte da verdade; um broker futuro será apenas consumidor dela.

### 6.1 Registro de entrega (OUT-01B, #29)

A migration forward-only `0026_outbox_delivery.sql` cria `outbox.delivery`:
confirmação de entrega idempotente por `(tenant_id, event_id)`, com RLS por
`app.tenant_id`, trigger append-only e grants mínimos (`SELECT, INSERT` apenas
para `amparius_app`). A tabela guarda somente `tenant_id`, `event_id`,
`event_type`, `attempt` e `delivered_at`: **nenhum payload, PII ou `last_error`**.

`outbox.Repository.Succeed` grava, na **mesma transação**, o `status='processed'`,
a linha de entrega e a trilha `succeeded` (`INSERT ... ON CONFLICT (tenant_id,
event_id) DO NOTHING`). Não existe `processed` sem efeito: se o INSERT da entrega
falhar, a transação inteira sofre rollback e o evento permanece `processing`,
elegível para novo claim. A chave é mais forte que a do §6 (por `event_id`,
não por `attempt`): deduplica também entre tentativas de retry, que é o que
"replay sem duplicar efeito" exige.

O handler `DeliveryHandler` apenas valida o envelope v1 antes de autorizar a
conclusão — **essa validação não é o efeito**; o efeito é a linha de entrega.
O handler `RefusingHandler` (`OUTBOX_DELIVERY_HANDLER=refuse`) recusa toda
entrega de forma explícita, para que ligar o binário sem efeito nunca esvazie a
outbox marcando eventos como processados.

**Escopo single-consumer.** A chave `(tenant_id, event_id)` pressupõe um único
destino por evento. Quando existir o primeiro consumidor real (broker,
read-model, fiscal), o mesmo `event_id` terá vários destinos e essa chave deixa
de bastar: multi-consumer exige outro desenho (chave por destino) e nova
decisão de contrato. Nenhum handler de negócio está ligado nesta fatia.

## 7. Catálogo v1

### `funeral.contract.created` — v1

Agregado: `funeral.contract`.

```json
{
  "contract_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "status": "draft",
  "customer_party_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "deceased_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "total_amount_cents": 15000
}
```

Chave: `funeral.contract.create:<Idempotency-Key da criação>`.

### `funeral.contract.status_changed` — v1

```json
{
  "contract_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "from_status": "draft",
  "to_status": "active"
}
```

Chave: `funeral.contract.status:<contract_id>:<to_status>`.

Notas de consumo: `status` usa o domínio `funeral_contract_status`
(`draft`, `active`, `completed`, `cancelled`); `reason` do histórico não é
publicado para não carregar texto livre.

### `finance.title.requested` — v1

Agregado: `funeral.contract`. O módulo funerário publica a solicitação para o
financeiro criar um título e suas parcelas. O consumidor deve usar a chave do
evento como idempotência do título e `(title_id, number)` para não duplicar a
cobrança.

```json
{
  "contract_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "service_order_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "company_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "customer_party_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "total_amount_cents": 10000,
  "installments": [
    {"number": 1, "amount_cents": 3334, "due_date": "2026-12-15"},
    {"number": 2, "amount_cents": 3333, "due_date": "2027-01-15"}
  ]
}
```

Chave: `finance.title.requested:<contract_id>`.

### `fiscal.document.requested` — v1

Agregado: `funeral.contract`. O evento solicita a criação do documento fiscal,
mas não calcula nem transporta tributos. O consumidor consulta as regras
oficiais vigentes para a competência e falha fechado quando não houver regra.

```json
{
  "contract_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "company_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "customer_party_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "competence": "2026-11-25",
  "total_amount_cents": 10000
}
```

Chave: `fiscal.document.requested:<contract_id>`.

### `funeral.service_order.*` — v1 (FUN-01C)

Agregado: `funeral.service_order`. Os três eventos usam payload sem PII:

```json
{
  "service_order_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "contract_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "company_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "status": "open",
  "from_status": "open",
  "to_status": "completed"
}
```

- `funeral.service_order.created`: `service_order_id`, `contract_id`,
  `company_id`, `status` e `scheduled_for`.
- `funeral.service_order.updated`: `service_order_id`, `contract_id`,
  `company_id`, `from_status` e `to_status` (iguais quando a alteração é
  somente operacional).
- `funeral.service_order.status_changed`: os IDs e `from_status`/`to_status`
  de uma transição explícita de estado.
- `funeral.service_order.completed`: os mesmos IDs e `from_status`/`to_status`.

Descrição, nomes e motivo textual não entram no payload. A criação usa
`funeral.service_order.create:<sha256 do request>`; update e conclusão usam
`funeral.service_order.<op>:<service_order_id>:<sha256 do request>`. O hash
mantém as chaves estáveis e dentro do limite de 200 caracteres da outbox.

### `funeral.schedule.*` — v1 (FUN-02A)

Agregado: `funeral.schedule`. Os eventos não carregam PII e usam referências
opacas:

```json
{
  "schedule_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "contract_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "service_order_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "company_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "kind": "wake",
  "location_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "resource_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "starts_at": "2026-11-25T15:00:00Z",
  "ends_at": "2026-11-25T17:00:00Z",
  "time_zone": "America/Fortaleza",
  "status": "scheduled"
}
```

- `funeral.schedule.created`: todos os campos do exemplo; `service_order_id` é
  opcional para preservar criações de agenda sem OS;
- `funeral.schedule.updated`: `schedule_id`, `contract_id`, `company_id`,
  `kind`, `location_id`, `resource_id`, `starts_at`, `ends_at`, `time_zone`,
  `from_status` e `to_status`;
- `funeral.schedule.confirmed`: `schedule_id`, `contract_id`, `company_id`,
  `from_status` e `to_status`, com `to_status = confirmed`;
- `funeral.schedule.cancelled`: `schedule_id`, `contract_id`, `company_id`,
  `from_status` e `to_status`, com `to_status = cancelled`.

O banco usa intervalo semiaberto `[starts_at, ends_at)` e rejeita sobreposição
do mesmo local ou recurso enquanto o status é `scheduled` ou `confirmed`,
inclusive sob concorrência. A chave de criação é
`funeral.schedule.create:<sha256 do request>`; alterações e cancelamento usam
`funeral.schedule.<op>:<schedule_id>:<sha256 do request>`; confirmação usa o
mesmo formato. Nome, kind de local, descrição, party e texto livre não entram
no payload.

### `funeral.journey.created` — v1 (FUN-02C)

Agregado: `funeral.journey`. O evento confirma a criação atômica da jornada e
carrega somente referências opacas:

```json
{
  "journey_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "contract_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "service_order_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "schedule_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d",
  "company_id": "0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d"
}
```

`funeral.journey.create:<sha256 do request>` é idempotente. O evento é escrito
na mesma transação da OS, da agenda e de seus históricos; não há descrição,
nome, documento, motivo ou outro texto livre.

## 8. Compatibilidade

- Campo **novo e opcional** entra na mesma `event_version`.
- Campo removido, renomeado, ou com semântica alterada exige
  `event_version` novo; o `event_type` permanece e o consumidor aceita a
  versão que conhece, ignorando versões maiores.
- Nunca reutilizar uma `idempotency_key` com conteúdo diferente; a correção é
  um evento novo com chave nova.

## 9. Verificação

```powershell
go test ./internal/modules/outbox/... -run 'PayloadPrivacy|Atomicity|Database|ErrorText' -count=1 -v
```

Com PostgreSQL real (`amparius_app` `NOSUPERUSER NOBYPASSRLS`), comando
reproduzível da fatia OUT-01B:

```powershell
$env:AMPA_TEST_POSTGRES_DSN = 'postgres://amparius_app:amparius@127.0.0.1:5432/amparius?sslmode=disable'
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = 'postgres://amparius:amparius@127.0.0.1:5432/amparius?sslmode=disable'
go test ./... -count=1
go vet ./...
go build ./...
git diff --check
goose -dir migrations postgres 'postgres://amparius:amparius@127.0.0.1:5432/amparius?sslmode=disable' up
```

Testes de integração da entrega: concorrência de dois workers sem duplicar,
crash/lease expirado reobtido, replay sem reaplicar efeito, falha terminal
visível, entrega atômica (`TestSucceedRollsBackWhenDeliveryInsertFails`) e
isolamento RLS de `outbox.delivery`.

## 10. Limites conhecidos

- Retenção/expurgo de eventos `processed` continua fora do contrato (AGENTS §6,
  decisão de auditoria/LGPD pendente).
- Não há garantia de ordem entre aggregates; consumidores não podem assumir
  sequência global.
- O `Dispatcher` **está** ligado ao `cmd/worker` desde a OUT-01B, mas apenas ao
  registro de entrega idempotente (§6.1), que é efeito de infraestrutura, não de
  negócio. Ainda **não** existe handler de negócio real: ligar um consumidor de
  domínio (fiscal, read-model, broker) exige contrato próprio e, se houver mais
  de um destino por evento, redesenho da chave de idempotência do sink.
- `outbox.delivery` fica fora do escopo original deste contrato v1 (que cobre
  `event` e `event_attempt`): é uma extensão da OUT-01B, documentada no §6.1,
  com migration, RLS e grants próprios.

## 11. Observabilidade (OUT-01C, #30)

O contrato de observação é composto por logs JSON e pela exposição Prometheus
do worker. Nenhum dos dois carrega payload, `last_error` bruto, DSN ou segredo.

- **Log de ciclo** (`outbox ciclo concluido`): `correlation_id` do ciclo,
  `worker_id`, contagens (`tenants`, `reaped`, `claimed`, `succeeded`,
  `retried`, `failed`), backlog amostrado por soma e `duration_ms`. Erro de
  ciclo é logado em Warn com `erro` redigido.
- **Log de evento** (entrega em Debug, retry em Warn, falha terminal em
  Error): `tenant_id`, `event_id`, `trace_id`, `event_type`, `attempt`,
  `max_attempts`, `correlation_id`, `worker_id` e `duration_ms`.
- `trace_id` é o `event_id` (UUIDv7). É a correlação estável entre produtor,
  linha de `outbox.event` e linha de `outbox.delivery`, sem incluir dado de
  negócio. O `correlation_id` do ciclo é opaco e, quando o append nasce de uma
  requisição HTTP, o `X-Request-Id` é propagado pelo contexto.
- Texto de erro passa por `outbox.RedactError`, a mesma redação aplicada ao
  `last_error`; padrões de e-mail, CPF/CNPJ, telefone e token viram
  `[redigido]`.
- **Métricas** (prefixo `amparius_outbox_`): ciclos, erros de ciclo, eventos
  reivindicados/entregues/reagendados/falhos/recolhidos, histogramas de
  duração de ciclo e de entrega, gauge de backlog por tenant/status e gauge de
  idade do evento mais antigo. O gauge é a fotografia do último ciclo e é
  substituído inteiro a cada ciclo (`ReplaceGauges`), sem série obsoleta.
- **Endpoint**: `GET /metrics` no worker, por padrão em `127.0.0.1:9091`;
  `OUTBOX_METRICS_ADDR` aceita `host:porta` ou `off`/`-`/`disabled`/`none`.
  Falha ao abrir o listener derruba o bootstrap.
- Amostrar backlog nunca interrompe entrega: erro de amostragem vira log Warn,
  incrementa `amparius_outbox_backlog_sample_errors_total` e o ciclo segue.

Alertas, limitações do gauge (amostra por ciclo e limite de
`OUTBOX_MAX_TENANTS`), regras Prometheus e comandos reproduzíveis estão em
`docs/observability/outbox-alerts.md`.

Evidência local sem banco (contrato de log/métrica):

```powershell
go test ./internal/platform/observability/... ./cmd/worker/... -count=1 -v
go test ./internal/modules/outbox/... -count=1 -run 'DispatcherLogs|DispatcherRetry|BacklogSampleError|Observability' -v
```
