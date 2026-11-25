# Contrato RELAY-SYNC v1

Escopo: processamento local da fila do relay após a reconexão (RELAY-01C) e o
protocolo de sincronização versionado entre relay e SaaS (RELAY-02A). Este
contrato define a fronteira do sincronizador; não afirma que exista um cliente
HTTP do SaaS nesta fatia.

## Fluxo

1. O sincronizador lê, por `tenant_id` e `node_id`, somente operações `pending`, em
   ordem crescente de `seq`.
2. O transporte recebe a operação inteira, incluindo `operation_id`,
   `idempotency_key` e `content_hash`, e o envelope versionado definido abaixo.
3. O transporte responde `applied` com `server_cursor`, ou uma rejeição classificada.
   Para `master_upsert`, o aceite precisa ser `MasterApplied` e trazer a versão
   autoritativa do servidor.
4. `applied` marca a operação como `synced` e persiste o cursor; `MasterApplied`
   também persiste `authoritative_version` como evidência do aceite.
5. Indisponibilidade ou timeout mantém a operação `pending`, incrementa `attempts` e
   registra apenas um código técnico seguro (`sync_retry:*`). A próxima execução
   reapresenta a mesma operação; não é criada uma nova sequência.

O adaptador de rede que implementar `SyncTransport` deve aplicar timeout, retry
limitado e jitter. O engine desta versão não abre sockets nem simula integração
com o SaaS.

## Rejeições críticas

Uma rejeição fiscal marca a operação como `failed` e cria, na mesma persistência,
uma `ReviewTask` aberta, vinculada a `(tenant_id, operation_id)`. O código da
rejeição é filtrado para identificador seguro; resposta fiscal crua e PII não entram
no store de fila.

Conflito também bloqueia a operação e não aplica last-write-wins. A resolução fica
para uma etapa explícita de revisão.

## Protocolo versionado (RELAY-02A)

O limite de transporte ganhou um envelope explícito, `SyncEnvelope`. `SyncTransport`
mantém `submit` para adaptadores legados e oferece `submit_versioned`, que recebe a
operação e o envelope. Um adaptador novo deve sobrescrever `submit_versioned` e
enviar o envelope; o relay desta fatia continua sem abrir sockets.

```json
{
  "protocol_version": 1,
  "event_version": 1,
  "event_id": "op_...",
  "tenant_id": "tenant-a",
  "origin_node": "node-1",
  "seq": 1,
  "kind": "contract_create",
  "entity_id": "contract-123",
  "idempotency_key": "device-op-0001",
  "actor_subject_id": "operator-1",
  "content_hash": "...",
  "occurred_at": 1764036000,
  "payload": { "total_cents": 150000, "currency": "BRL" }
}
```

- `protocol_version` versiona o envelope; `event_version` versiona o evento. Esta
  fatia aceita somente `1`/`1`. Versão desconhecida é recusada, nunca interpretada
  como a versão 1.
- `event_id` é o `operation_id` opaco. O receptor recalcula `event_id` e
  `content_hash` a partir de `(tenant_id, origin_node, seq)` e de
  `(kind, entity_id, payload)`; envelope adulterado é recusado.
- `origin_node` é a origem. A ordem é monotônica por `(tenant_id, origin_node)` e
  `seq` começa em `1`.
- Campos novos só podem ser opcionais dentro da mesma versão. Remover ou mudar o
  significado de um campo exige nova `event_version`.

### Ordem, duplicidade e reaplicação

O receptor de referência, `SyncReceiver`, é vinculado a um tenant e mantém um
cursor monotônico do servidor:

| Situação | Resultado | Estado |
|---|---|---|
| `seq` esperado para a origem | `applied` com novo cursor | avança |
| mesmo `event_id` e conteúdo já aplicado | `duplicate` com o cursor original | inalterado |
| `seq` acima do esperado | `out_of_order` com `expected_seq` | inalterado |
| mesmo `seq` ou mesma `idempotency_key` com conteúdo diferente | `conflict` | inalterado |
| `tenant_id` diferente do receptor | `rejected` (`tenant_mismatch`) | inalterado |
| versão desconhecida ou envelope adulterado | `rejected` | inalterado |

Reaplicar o lote inteiro após reconexão produz `duplicate` para cada evento já
aplicado; nenhum evento é aplicado duas vezes e o cursor não regride. Um intervalo
de sequência não é preenchido silenciosamente: o receptor recusa e informa a
sequência esperada. O adaptador do relay deve tratar `out_of_order` como rejeição
classificada, nunca como sucesso, e não inventa o evento faltante; o bloqueio e a
reconciliação explícita ficam para a RELAY-02B.

## Reconciliação (RELAY-02B)

As operações são classificadas pela natureza do efeito:

| Domínio | Operações | Regra de autoridade |
|---|---|---|
| `master` | `master_upsert` | servidor autoritativo; só uma confirmação do transporte sincroniza |
| `money` | contrato e caixa | conflito bloqueia e exige revisão; nenhum valor é sobrescrito |
| `fiscal` | `fiscal_emission` | rejeição vira `failed`/`FiscalFailure`; conflito vira revisão |

`master_upsert` exige `base_version` inteiro no payload. O relay não aplica uma
versão local concorrente nem realiza merge implícito. O transporte pode devolver
`MasterApplied { server_cursor, authoritative_version }` quando o servidor
aceitar a mutação; a versão autoritativa serve como evidência do aceite e não
como dado local inventado.

Para conflito, o relay grava `status=conflict`, incrementa a tentativa e cria
uma `ReviewTask` aberta com `MasterConflict`, `MoneyConflict` ou
`FiscalConflict`. A tarefa carrega `tenant_id`, `operation_id`, tipo e código
sanitizado; não carrega payload, resposta remota ou PII. Reaplicar o mesmo
conflito retorna a tarefa existente. Não há endpoint de resolução nem integração
HTTP/SaaS nesta versão.

## Invariantes

- tenant é obtido da credencial e filtrado em todas as leituras/escritas;
- replay reapresenta o mesmo `operation_id`, `idempotency_key` e `event_id`;
- cursor só é persistido depois de `applied`;
- falha de persistência devolve o estado em memória ao valor anterior;
- nenhuma rejeição fiscal vira autorização local;
- versão de protocolo ou de evento desconhecida falha fechado.

## Verificação

```powershell
cargo test --manifest-path relay/Cargo.toml protocol::tests
cargo test --manifest-path relay/Cargo.toml sync::tests
```

Os testes são determinísticos, usam transportes roteirizados e um receptor em
memória, e não comprovam conectividade real, TLS, PostgreSQL local ou resposta de
um ambiente SaaS. O receptor de referência não persiste estado entre execuções e
não faz buffer de intervalos. A política local de conflitos e a trilha
persistente de revisão da RELAY-02B ficam no `LocalStore`; autoridade remota,
endpoint de resolução e integração HTTP/SaaS continuam fora desta fatia.
