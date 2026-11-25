# Contrato CLIENT-OFFLINE-STATE v1

Escopo: modelo local do cliente Rust para CL-01/CL-03. A UI deve consumir o
estado abaixo sem transformar indisponibilidade de rede em perda de operação.

## Estado e fila

`ConnectivityState` possui três valores distintos: `online`, `offline` e
`contingency`. A fila local pode receber operações permitidas em qualquer estado;
o estado de contingência não significa que uma emissão fiscal foi autorizada.

Cada operação nova recebe `seq` monotônica por `(tenant_id, node_id)`, iniciando em
`1`, além de `operation_id`, `content_hash` e `idempotency_key`. Replay com a mesma
chave e conteúdo retorna a operação existente. A mesma chave com conteúdo diferente
é rejeitada e não altera a operação original nem consome nova sequência.

Payloads usam inteiros em centavos, referências opacas e não podem conter PII. O
cliente não registra payload, XML, resposta fiscal ou segredo em observabilidade.

## Persistência local

`ClientQueue::open(path, tenant_id, node_id)` usa um arquivo JSON local versionado
(schema `1`) como armazenamento da fila do cliente. A abertura exige o mesmo
`tenant_id` e `node_id` do arquivo; uma divergência falha fechado. Se o arquivo não
existe, ele é criado antes de a fila ser usada.

O arquivo guarda a conectividade, a sequência final, as operações, tarefas de revisão
e contadores de métrica. Um SHA-256 cobre todo o estado persistido, exceto o próprio
hash. Cada escrita usa arquivo temporário e substituição com backup recuperável. A
abertura rejeita JSON inválido, versão desconhecida, hash divergente, operação
adulterada, duplicidade de `seq`/`idempotency_key` e payload que não passe as mesmas
validações de entrada. Nenhuma reconstrução silenciosa é feita.

O formato é adequado à fila local da estação nesta fatia, mas não é banco de produção:
não fornece criptografia em repouso, WAL, concorrência entre processos ou transporte.
O caminho deve ser privado pelo instalador; a migração para o armazenamento durável
do relay/servidor da loja permanece posterior.

## Reconciliação

O transporte entrega um `ReconciliationOutcome` explícito:

- `Applied` sincroniza operações de contrato/caixa; `MasterApplied` exige versão
  autoritativa para `master_upsert`.
- `Retry` mantém a mesma operação pendente e incrementa `attempts`. O limite local
  padrão é três; ao esgotar, a operação fica `failed` e recebe revisão.
- `Conflict` marca `conflict` e cria `MoneyConflict`, `FiscalConflict` ou
  `MasterConflict`. Nenhum valor local é sobrescrito.
- `FiscalRejected` marca `failed` e cria `FiscalFailure`.
- `Duplicate` é sucesso idempotente; `OutOfOrder` permanece pendente e sinaliza a
  rejeição classificada.

As tarefas de revisão carregam somente referência opaca, tipo e código filtrado.
Não existe resolução automática nesta versão.

## Transporte HTTP

`client::transport::HttpTransport` envia operações `pending` da fila persistida ao
relay local, no contrato RELAY-OPERATIONS v1 (`POST /relay/v1/operations`).

- **Contexto:** `tenant_id` e `node_id` vêm da fila, nunca do corpo; seguem em
  `x-amparius-tenant` e `x-amparius-node`, com `x-amparius-subject` e
  `x-amparius-token`. O token existe apenas em trânsito, não é logado nem
  persistido; credencial vazia falha antes de qualquer socket.
- **Corpo:** `kind`, `entity_id`, `idempotency_key`, `payload`, `seq`,
  `operation_id` e `content_hash`. `Idempotency-Key` também vai em cabeçalho. O
  relay atual ignora campos extras; o cliente os preserva para auditoria e
  evolução do contrato. O payload é revalidado (mesmas regras do enfileiramento,
  sem PII) e hash/identificador são recalculados antes do envio; divergência é
  erro fatal e nada é transmitido.
- **Resultado:** `Accepted` (200/201), `Conflict` (409), `Rejected` (demais 4xx
  ou 2xx ilegível) ou `Transient` (timeout, conexão/leitura, 5xx).
- **Integração:** `sync_pending` percorre `pending` em ordem crescente de `seq`,
  aplica cada resultado em `ClientQueue::reconcile` (que persiste antes de
  confirmar em memória) e para na primeira falha transitória, para não consumir
  o orçamento das demais operações durante uma queda.

### Rede e retry

Cada chamada usa timeout configurável de cliente e conexão. `max_attempts` inclui
a tentativa inicial e nunca é zero. Apenas indisponibilidade/timeout é repetida:

| Situação | Classificação | Retry |
|---|---|---|
| timeout | `transport_timeout` | sim, com backoff |
| conexão/leitura | `transport_unavailable` | sim, com backoff |
| HTTP 5xx | `transport_unavailable` | sim, com backoff |
| HTTP 401/403 | `Unauthorized` | não |
| HTTP 409 | `Conflict` | não |
| demais 4xx | `Rejected` | não |
| 2xx ilegível | `Rejected` `invalid_response` | não |

401/403 interrompem a execução sem reconciliar: a operação permanece `pending`, sem
revisão de negócio, para uma execução após renovação da credencial. 4xx fiscal vira
`FiscalRejected`; 4xx de dinheiro ou mestre vira `Conflict`, com tarefa de revisão
explícita e sem sobrescrever valor local. Redirecionamentos são desabilitados para
o token não vazar de host.

O backoff é exponencial com full jitter determinístico derivado de
`(jitter_seed, operation_id, seq, attempt)` e limitado por `backoff_cap`. O mesmo
estado produz o mesmo atraso, o que torna timeout e retry testáveis sem relógio
global. Esgotadas as tentativas HTTP, a operação continua `pending`, `attempts` é
incrementado e `last_error` recebe somente `sync_retry:*`; o limite de execuções
do `ClientQueue` continua sendo aplicado por `reconcile`.

### Reconciliação do transporte

- `Accepted` sem `replayed` → `Applied`; `replayed` → `Duplicate` (sucesso
  idempotente, sem criar operação nem consumir nova sequência).
- `Accepted` de `master_upsert` sem `authoritative_version` → `Conflict`
  (`master_requires_authoritative_version`); o relay não é a autoridade do mestre
  e a operação não é marcada como sincronizada.
- `server_cursor` é o `seq` do registro aceito pelo relay.

## Limites

O transporte é HTTP sem TLS embutido: o relay deve escutar em loopback ou atrás de
TLS local, como no contrato de operações de relay. HTTPS direto, proxy corporativo,
credencial renovável e envio de XML fiscal continuam fora desta fatia. A UI gráfica
permanece posterior. O arquivo local não substitui o PostgreSQL do relay, não é
backup e não autoriza emissão fiscal: contingência continua sendo apenas um estado
explícito do cliente.
