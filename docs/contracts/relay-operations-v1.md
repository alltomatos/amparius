# Contrato RELAY-OPERATIONS v1

Escopo: fila offline de contrato e caixa do relay (RELAY-01B), log monotônico por
dispositivo e status visível. Este contrato não define transmissão, sincronização,
reconciliação, PostgreSQL local nem emissão fiscal.

## Autenticação

Toda rota de fila ou status exige as três cabeçalhos abaixo. A credencial é a mesma
cacheada pelo RELAY-01A; a autenticação usa tenant, sujeito, token e a janela
`issued_at <= now < valid_until`. Ausência, token incorreto ou validade encerrada
retorna `401`.

| Cabeçalho | Conteúdo |
|---|---|
| `x-amparius-tenant` | `tenant_id` opaco |
| `x-amparius-subject` | `subject_id` do operador |
| `x-amparius-token` | token bruto apenas em trânsito; nunca persistido |
| `x-amparius-node` | `node_id` do dispositivo (obrigatório no `POST`) |

O `tenant_id` efetivo vem da credencial autenticada, nunca do corpo. Um tenant não
enxerga operações, sequência ou status de outro.

## Operação

```json
{
  "kind": "contract_create",
  "entity_id": "contract-123",
  "idempotency_key": "device-op-0001",
  "payload": {
    "total_cents": 150000,
    "currency": "BRL",
    "item_count": 2
  }
}
```

`entity_id` e `idempotency_key` são opacos, não vazios, com até 128 caracteres
`[A-Za-z0-9._:-]`. O payload é um objeto JSON de até 64 KiB.

### Tipos e validação

| `kind` | Payload obrigatório | Regra |
|---|---|---|
| `contract_create` | `total_cents` inteiro; `currency` opcional | total `>= 0`; moeda, se presente, `BRL`; `item_count`, se presente, inteiro `> 0` |
| `cash_open` | `opening_amount_cents` inteiro | valor `>= 0` |
| `cash_movement` | `direction` (`in`/`out`), `amount_cents` inteiro, `reason` texto | valor `> 0`; motivo não vazio e até 200 caracteres |
| `cash_close` | `counted_amount_cents` inteiro; `expected_amount_cents` opcional | valores `>= 0` |

Todo dinheiro é inteiro em centavos. Float, valor negativo fora da regra, moeda
desconhecida e payload não-objeto são recusados com `400`.

Chaves de dado pessoal (CPF, CNPJ, RG, nome, e-mail, telefone, celular, endereço,
data de nascimento) são recusadas em qualquer nível do payload com `400`. O log de
sincronização carrega referências e valores operacionais, não o cadastro completo.

### Sequência e idempotência

- `seq` é monotônica por `(tenant_id, node_id)`, começa em `1` e nunca é reutilizada.
  Reiniciar o relay não reduz a sequência.
- A chave de replay local é `(tenant_id, node_id, idempotency_key)`.
- Mesmo conteúdo com a mesma chave devolve `200` e o registro existente, sem duplicar.
- Conteúdo diferente com a mesma chave devolve `409 idempotency_conflict` e preserva o
  registro original. Não há last-write-wins.
- `operation_id` é um hash opaco de `(tenant_id, node_id, seq, content_hash)`.
- `content_hash` é SHA-256 de `(kind, entity_id, payload)`. Na abertura do store, hash,
  identificador, unicidade de sequência, unicidade de idempotência e unicidade de
  dispositivo são verificados; adulteração faz o store falhar fechado.

### Resposta de criação

`201` na primeira gravação e `200` em replay:

```json
{
  "operation": {
    "operation_id": "op_...",
    "tenant_id": "tenant-a",
    "node_id": "node-1",
    "seq": 1,
    "kind": "contract_create",
    "entity_id": "contract-123",
    "idempotency_key": "device-op-0001",
    "actor_subject_id": "operator-1",
    "payload": { "total_cents": 150000, "currency": "BRL", "item_count": 2 },
    "content_hash": "...",
    "status": "pending",
    "created_at": 1764036000,
    "updated_at": 1764036000,
    "attempts": 0,
    "last_error": null,
    "synced_at": null,
    "server_cursor": null
  },
  "replayed": false
}
```

`status` nesta fatia é `pending`. `synced`, `failed` e `conflict` existem para a
sincronização futura; nenhuma rota pública os altera.

## Listagem

`GET /relay/v1/operations?node_id=node-1&after_seq=0&limit=50`

- `node_id` opcional filtra um dispositivo; ausente lista todos do tenant.
- `after_seq` é exclusivo; default `0`.
- `limit` é limitado a `1..=200`; default `50`.

```json
{
  "operations": [ ... ],
  "next_after_seq": 2
}
```

## Status visível

`GET /relay/v1/status?node_id=node-1`

```json
{
  "mode": "offline_queued",
  "queue": {
    "tenant_id": "tenant-a",
    "node_id": "node-1",
    "pending": 2,
    "synced": 0,
    "failed": 0,
    "conflict": 0,
    "total": 2,
    "oldest_pending_at": 1764036000,
    "oldest_pending_age_secs": 86400,
    "last_seq": 2,
    "last_error": null
  }
}
```

- `mode` é `offline_queued` quando `pending > 0` e `online` quando `pending == 0`.
- `oldest_pending_age_secs` é `now - oldest_pending_at`, saturado em zero. A UI usa
  contagem e idade para mostrar a fila; contingência fiscal entra em fatia futura.
- `last_error` traz a mensagem do sincronizador mais recente, truncada em 500 caracteres e
  sem payload de negócio, quando houver.

## Códigos de erro

| HTTP | `code` | Quando |
|---|---|---|
| `400` | `invalid_request` | campo ausente, tipo errado, valor fora da regra ou PII |
| `401` | `missing_credential` / `invalid_credential` | cabeçalho ausente, token incorreto, credencial expirada ou de outro tenant |
| `404` | `operation_not_found` | transição interna para operação inexistente |
| `409` | `idempotency_conflict` | mesma chave com conteúdo diferente |
| `409` | `invalid_transition` | regressão de status bloqueada |
| `413` | `payload_too_large` | payload acima de 64 KiB |
| `503` | `store_unavailable` | falha de I/O ou corrupção detectada |

Mensagens de erro não incluem token, payload de negócio nem PII.

## Persistência e limites

O store JSON mantém `records`, `credentials`, `snapshot`, `queue` e `sequencers` no
schema `2`. Arquivos do schema `1` são migrados na abertura sem perda. A escrita usa
arquivo temporário e backup `.bak`; o registro só é confirmado depois de durável.

Limites desta versão:

- não há transmissão nem alteração de status pela rede;
- não há PostgreSQL local, WAL, criptografia de volume ou retenção automática;
- o volume esperado (contrato e caixa de uma loja por 24 h) é adequado ao JSON, mas
  operação contínua de longo prazo exige a fatia PostgreSQL;
- o relay deve escutar em loopback ou atrás de TLS local; o token não pode trafegar em
  HTTP aberto na LAN.
