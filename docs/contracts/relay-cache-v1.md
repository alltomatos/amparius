# Contrato RELAY-CACHE v1

Escopo: store temporário, cache de autenticação e snapshot de enforcement do relay
(RELAY-01A). Este contrato não define sync, fila de saída ou o schema do PostgreSQL.

## Store temporário

Cada registro local tem a forma:

```json
{
  "tenant_id": "tenant opaco",
  "key": "chave da projeção local",
  "expires_at": 1732500000,
  "payload": { "dados": "mínimos" }
}
```

Regras:

- `tenant_id` e `key` não podem ser vazios;
- a chave `(tenant_id, key)` é upsert, evitando duplicação local;
- leitura com outro tenant retorna ausência, sem revelar o conteúdo;
- `expires_at <= now` é expiração e a leitura falha fechada;
- o payload é uma projeção temporária: não é fonte de verdade nem deve conter segredo;
- o arquivo pertence ao diretório local ignorado pelo Git.

## Credencial cacheada

O cache mantém `tenant_id`, `subject_id`, `issued_at`, `valid_until` e `token_hash`. O hash é
SHA-256 do token emitido pelo SaaS; o token bruto não é persistido. A autenticação exige
tenant, sujeito, token correto e `issued_at <= now < valid_until`. Ausência, tenant divergente,
token incorreto ou validade encerrada negam o acesso.

## Snapshot assinado

O envelope v1 é:

```json
{
  "schema_version": 1,
  "key_id": "control-plane-key-1",
  "tenant_id": "tenant opaco",
  "version": 7,
  "issued_at": 1732496400,
  "valid_until": 1732582800,
  "payload": { "enforcement": "derivado" },
  "signature": "bytes Ed25519 serializados pelo JSON"
}
```

A assinatura cobre todos os campos, exceto `signature`, em serialização JSON determinística
da estrutura Rust. A chave pública correta, o tenant esperado, o schema, a janela
`issued_at <= now < valid_until` e a assinatura são obrigatórios. Um snapshot aceito precisa
ter versão maior que a versão local; rollback é rejeitado.

## Não coberto nesta versão

Provisionamento/rotação de chaves, criptografia do volume local, PostgreSQL, sync por cursor,
fila de saída e endpoints HTTP são contratos posteriores. O binário apenas abre o store no
startup; os casos de uso ainda consomem a biblioteca nas próximas fatias.
