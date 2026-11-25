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
  `pending`; ao esgotar `max_attempts` vira `failed`.
- Lease vencido que já esgotou tentativas é fechado por `ReapExpired`.
- A outbox é a fonte da verdade; um broker futuro será apenas consumidor dela.

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

## 8. Compatibilidade

- Campo **novo e opcional** entra na mesma `event_version`.
- Campo removido, renomeado, ou com semântica alterada exige
  `event_version` novo; o `event_type` permanece e o consumidor aceita a
  versão que conhece, ignorando versões maiores.
- Nunca reutilizar uma `idempotency_key` com conteúdo diferente; a correção é
  um evento novo com chave nova.

## 9. Verificação

```powershell
go test ./internal/modules/outbox/... -run 'PayloadPrivacy|Atomicity|Database' -count=1 -v
```

Com PostgreSQL real (`amparius_app` `NOSUPERUSER NOBYPASSRLS`):

```powershell
$env:AMPA_TEST_POSTGRES_DSN = 'postgres://amparius_app:...@127.0.0.1:5432/amparius?sslmode=disable'
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = 'postgres://amparius:...@127.0.0.1:5432/amparius?sslmode=disable'
go test ./internal/modules/outbox/... ./internal/modules/funeral/... -count=1 -run 'Atomicity|PersonalData|FuneralContractEvent' -v
```

## 10. Limites conhecidos

- Retenção/expurgo de eventos `processed` continua fora do contrato (AGENTS §6,
  decisão de auditoria/LGPD pendente).
- Não há garantia de ordem entre aggregates; consumidores não podem assumir
  sequência global.
- O `Dispatcher` ainda não está ligado ao `cmd/worker` porque não existe handler
  de negócio real; ligar sem efeito marcaria eventos como processados à toa.
