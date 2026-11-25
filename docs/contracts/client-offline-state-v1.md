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

## Limites

O cliente ainda não abre sockets; o adaptador de rede deverá aplicar timeout, retry
limitado e jitter. A UI gráfica e a integração HTTP com o relay ficam para fatias
posteriores. O arquivo local não substitui o PostgreSQL do relay, não é backup e não
autoriza emissão fiscal: contingência continua sendo apenas um estado explícito do
cliente.
