# Contrato RELAY-SYNC v1

Escopo: processamento local da fila do relay após a reconexão (RELAY-01C). Este
contrato define a fronteira do sincronizador; não afirma que exista um cliente
HTTP do SaaS nesta fatia.

## Fluxo

1. O sincronizador lê, por `tenant_id` e `node_id`, somente operações `pending`, em
   ordem crescente de `seq`.
2. O transporte recebe a operação inteira, incluindo `operation_id`,
   `idempotency_key` e `content_hash`.
3. O transporte responde `applied` com `server_cursor`, ou uma rejeição classificada.
4. `applied` marca a operação como `synced` e persiste o cursor.
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

## Invariantes

- tenant é obtido da credencial e filtrado em todas as leituras/escritas;
- replay reapresenta o mesmo `operation_id` e `idempotency_key`;
- cursor só é persistido depois de `applied`;
- falha de persistência devolve o estado em memória ao valor anterior;
- nenhuma rejeição fiscal vira autorização local.

## Verificação

```powershell
cargo test --manifest-path relay/Cargo.toml sync::tests
```

Os testes são determinísticos, usam transportes roteirizados e não comprovam
conectividade real, TLS, PostgreSQL local ou resposta de um ambiente SaaS.
