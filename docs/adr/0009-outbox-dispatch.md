# ADR 0009 — Outbox transacional e dispatch cross-tenant

Status: proposto · 23/11/2026 · referencia os ADRs 0002, 0003 e 0005

## Contexto

A Fase 1 do roadmap exige `Outbox + worker + idempotência`. Ainda não existe broker
(RabbitMQ está no compose, mas nenhum evento de negócio é publicado) e todas as
tabelas de negócio são isoladas por `tenant_id` com RLS pelo `app.tenant_id`
(ADR 0003). O worker precisa drenar eventos de todos os tenants, o que conflita
diretamente com uma política de RLS que só enxerga o tenant da sessão.

Três perguntas precisam de resposta antes do código:

1. Onde mora o evento e como ele guarda o histórico de tentativas?
2. Como o worker processa vários tenants sem abrir uma exceção global de RLS?
3. Como garantir claim único, retry persistido e nenhuma duplicação?

## Decisão

### 1. Schema `outbox`, evento versionado

`outbox.event` guarda o evento de domínio com `tenant_id`, `aggregate_type`,
`aggregate_id`, `event_type` (`<modulo>.<entidade>.<acao>`), `event_version`
inteiro positivo, `payload jsonb` obrigatoriamente objeto, `idempotency_key`,
`status`, `attempts`, `max_attempts`, `next_attempt_at`, `last_error`,
`created_at` e `processed_at`. IDs são `uuidv7`; tempos são `timestamptz` (UTC no
banco). Não há `enum` PostgreSQL: os estados são `identity.domain_value`
(`outbox_event_status`, `outbox_attempt_outcome`) validados por trigger, como nos
demais módulos.

A chave de idempotência é única por tenant: `UNIQUE (tenant_id, idempotency_key)`.
Reenviar a mesma chave devolve o evento original; reutilizar a chave com outro
conteúdo falha com erro tipado, em vez de sobrescrever silenciosamente.

### 2. Máquina de estados e auditoria append-only

`outbox.event` é mutável apenas nas colunas de progresso. Um trigger rejeita
`DELETE`, rejeita alteração de identidade (`tenant_id`, aggregate, tipo, versão,
payload, chave, `max_attempts`, `created_at`) e só aceita as transições
`pending → processing`, `processing → processing|processed|pending|failed`.
`attempts` só avança de um em um, no claim.

`outbox.event_attempt` é a trilha append-only de cada tentativa (`claimed`,
`succeeded`, `retry`, `failed`), com `UNIQUE (tenant_id, event_id, attempt,
outcome)` para que um replay do worker não duplique a auditoria. UPDATE e DELETE
são bloqueados por trigger.

### 3. Claim por tenant, uma função cross-tenant mínima

O worker não recebe `SUPERUSER` nem `BYPASSRLS`. O claim é feito **por tenant**,
com `app.tenant_id` definido pela `WithTenantTx`, usando um único `UPDATE ... FROM
(SELECT ... FOR UPDATE SKIP LOCKED)` que incrementa `attempts`, marca
`processing` e grava o lease em `next_attempt_at`. Assim todo acesso a payload
continua sob RLS.

A única superfície cross-tenant é `outbox.pending_tenants(p_limit)`, função
`SECURITY DEFINER` que devolve **somente `tenant_id`** dos tenants com evento
elegível. Ela é `EXECUTE` apenas para `amparius_app` e existe para o dispatcher
descobrir quem drenar. Nenhum dado de negócio atravessa essa função. O acesso ao
banco por ela passa por `db.WithSystemTx`, a única transação sem tenant do
projeto, documentada para esse fim.

### 4. Retry persistido, lease e falha terminal

Claim é lease: um worker que morre no meio deixa o evento `processing` com
`next_attempt_at` no passado; o próximo ciclo o reivindica. `Fail` grava
`last_error` (truncado), volta para `pending` com backoff exponencial calculado
em Go (2s até 1h) ou, ao atingir `max_attempts`, vira `failed` terminal.
`ReapExpired` varre leases vencidos que já esgotaram as tentativas e os fecha em
`failed`. Nada disso depende de broker: a outbox é a fonte da verdade e o
RabbitMQ, quando entrar, será apenas um consumidor dela.

Retenção/expurgo de eventos processados fica fora deste slice (exige política de
retenção e decisão de auditoria própria). O teste usa uma conexão administrativa
apenas no `t.Cleanup`, como os demais módulos.

## Consequências

- Todo módulo que publica evento escreve na mesma transação do dado de negócio.
- O dispatcher é biblioteca (`outbox.Dispatcher`) com `Handler` injetado; o
  binário `cmd/worker` só deve ser ligado quando existir um **traço durável de
  entrega**, para não marcar eventos como processados sem efeito algum. A
  OUT-01B (#29) liga o worker a um sink de entrega idempotente
  (`outbox.delivery`, migration `0026`), gravado na mesma transação do `Succeed`:
  é efeito de infraestrutura observável, **não** handler de negócio. A validação
  de envelope do `DeliveryHandler` não conta como efeito; o efeito é a linha de
  entrega. O primeiro consumidor de domínio (fiscal, read-model, broker) continua
  sem contrato e exigirá decisão própria — inclusive outra chave de
  idempotência, pois `(tenant_id, event_id)` só vale para sink single-consumer.
- O desenho adiciona uma função `SECURITY DEFINER`. Ela é auditável, devolve só
  IDs de tenant e é a única leitura cross-tenant do projeto.
- `max_attempts` por evento permite tratar eventos críticos (fiscal, dinheiro)
  com mais tentativas que eventos cosméticos.
- Ordem de entrega é por `next_attempt_at, created_at, id`; não há garantia de
  ordenação global entre tenants nem entre eventos de aggregates diferentes.

## Alternativas descartadas

- **Policy de RLS com flag de worker (`app.worker = on`)**: o papel de aplicação
  pode setar qualquer GUC; a flag viraria um bypass de RLS acionável por qualquer
  request, violando o ADR 0003.
- **Worker com papel `BYPASSRLS`**: o teste deixaria de provar isolamento com o
  papel real da aplicação.
- **Claim global em uma função `SECURITY DEFINER` que devolve payload**: mais
  simples, mas tiraria o payload do caminho de RLS e concentraria toda a leitura
  de negócio em uma função privilegiada.
- **Tabela de histórico separada por UPDATE (trigger copiando linha inteira)**:
  duplicaria payload a cada tentativa e cresceria sem necessidade; a trilha de
  tentativas guarda o que muda (tentativa, resultado, erro).
- **`SELECT ... FOR UPDATE` sem `SKIP LOCKED`**: dois workers disputariam o mesmo
  evento e serializariam o lote.

## Critérios de aceite

1. `append` repetido com a mesma `idempotency_key` no mesmo tenant devolve o
   evento original; chave com conteúdo diferente falha com erro tipado.
2. Isolamento: tenant B não lê nem reivindica evento de tenant A com o papel
   `amparius_app` sem `SUPERUSER` e sem `BYPASSRLS`.
3. Claim concorrente não entrega o mesmo evento duas vezes dentro do lease; lease
   vencido é reivindicado de novo e incrementa `attempts`.
4. Falha grava `last_error` (redigido, sem PII/segredo), agenda `next_attempt_at`
   com backoff e, ao esgotar `max_attempts`, marca `failed`; `ReapExpired` fecha
   leases vencidos.
5. `event_attempt` é append-only e registra claim, sucesso e retry/falha.
6. Migration sobe, desce e sobe de novo sem tocar nas migrations anteriores, e
   `go build`, `go vet` e `go test` passam.
7. (OUT-01B) A entrega e o `status='processed'` são atômicos: se o INSERT em
   `outbox.delivery` falha, o evento permanece `processing` e nada de efeito
   parcial é gravado. Replay do mesmo `event_id` não duplica a entrega.
