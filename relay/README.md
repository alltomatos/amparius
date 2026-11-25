# Amparius Relay

Relay local do Amparius no papel de servidor da loja.

## Requisitos

- Rust MSRV: 1.85 (edition 2024)

## Executar

```powershell
cargo run
Invoke-WebRequest http://127.0.0.1:8080/healthz
```

## RELAY-02A

O protocolo de sincronização é um `SyncEnvelope` versionado (`protocol_version` e
`event_version` iguais a `1`) com tenant, origem, sequência, identificador opaco,
chave de idempotência e hash de conteúdo. `SyncTransport::submit_versioned` recebe a
operação e o envelope; a implementação padrão delega para `submit`, preservando
adaptadores da RELAY-01C.

`SyncReceiver` é o receptor de referência: aplica em ordem estrita por origem,
devolve `duplicate` com o cursor original em replay exato, recusa intervalo com
`out_of_order` e trata mesma posição ou chave de idempotência com conteúdo
diferente como `conflict`, sem sobrescrever. Versão desconhecida, tenant divergente
ou envelope adulterado são recusados. Reaplicar o lote após reconexão não duplica
evento nem regride o cursor.

O receptor é em memória e não persiste estado; o adaptador HTTP autenticado e a
autoridade remota do SaaS continuam em fatias posteriores.

Contrato: `../docs/contracts/relay-sync-v1.md`. Decisão:
`../docs/adr/0027-relay-protocolo-sincronizacao-versionado.md`.

## RELAY-02B

A reconciliação classifica operações como `master`, `money` ou `fiscal`. Mestres
(`master_upsert`, com `base_version` inteiro) só são sincronizados após aceite
do servidor autoritativo (`SyncOutcome::MasterApplied`). Conflitos em qualquer
domínio marcam a operação como `conflict` e criam uma `ReviewTask` persistente,
tenant-scoped e deduplicada por operação; nenhum payload, resposta remota ou
PII entra na tarefa. Dinheiro e fiscal nunca usam last-write-wins.

Não há endpoint para resolver tarefas, aplicação de snapshot de mestre ou
adaptador HTTP/SaaS nesta fatia. Os testes usam transporte roteirizado e provam
conflito, idempotência, persistência e isolamento de tenant.

Contrato: `../docs/contracts/relay-sync-v1.md`. Decisão:
`../docs/adr/0028-relay-reconciliacao-conflitos.md`.

## RELAY-01C

`SyncEngine` fornece a fronteira local para reconexão: lê pendentes em ordem, preserva
`operation_id`/`idempotency_key` quando o transporte cai e persiste o `server_cursor`
somente após confirmação. Rejeição fiscal fica como `failed` e cria uma tarefa de
revisão deduplicada e tenant-scoped. O transporte de rede ainda não está implementado
nesta fatia; portanto os testes usam um transporte roteirizado e não comprovam
conectividade real.

Contrato: `../docs/contracts/relay-sync-v1.md`. Decisão:
`../docs/adr/0026-relay-sync-reconexao-revisao-fiscal.md`.

Configuração opcional por ambiente:

- `AMPARIUS_RELAY_BIND_ADDR` (default: `127.0.0.1:8080`)
- `AMPARIUS_RELAY_POSTGRES_URL` (default de desenvolvimento; ainda não conecta)
- `AMPARIUS_RELAY_STORE_PATH` (default: `relay-data/store.json`; dados locais são ignorados pelo Git)

## RELAY-01A

A biblioteca do relay fornece o store temporário local em JSON, com escopo explícito por
`tenant_id` e expiração por registro. A escrita usa arquivo temporário e substituição; o
store não é a fonte de verdade do SaaS e o PostgreSQL local/sync ainda são fatias posteriores.

O cache de autenticação persiste somente o hash SHA-256 do token, nunca o token bruto. Tanto
credenciais quanto snapshots falham fechados quando `valid_until <= now`. Snapshots de
enforcement são envelopes Ed25519 versionados, vinculados ao tenant e aceitos somente quando
a versão é monotônica.

Contrato detalhado: `../docs/contracts/relay-cache-v1.md`. Decisão arquitetural:
`../docs/adr/0022-relay-store-snapshot-auth-cache.md`.

## RELAY-01B

A fila offline de contrato e caixa é um log append-only por dispositivo. Cada operação tem
`seq` monotônica em `(tenant_id, node_id)`, chave de idempotência local, hash de conteúdo e
status (`pending`, `synced`, `failed`, `conflict`). Replay com o mesmo conteúdo não duplica;
a mesma chave com conteúdo diferente falha em vez de sobrescrever. O store JSON passa ao
schema `2` e migra arquivos `1` na abertura.

Rotas autenticadas pela credencial em cache (cabeçalhos `x-amparius-tenant`,
`x-amparius-subject`, `x-amparius-token`; `x-amparius-node` no `POST`):

```powershell
# abrir caixa offline
Invoke-RestMethod -Method Post http://127.0.0.1:8080/relay/v1/operations `
  -Headers @{
    "content-type" = "application/json"
    "x-amparius-tenant" = "tenant-a"
    "x-amparius-subject" = "operator-1"
    "x-amparius-token" = $env:AMPARIUS_RELAY_TOKEN
    "x-amparius-node" = "node-1"
  } `
  -Body '{"kind":"cash_open","entity_id":"cash-1","idempotency_key":"op-1","payload":{"opening_amount_cents":50000}}'

# status da fila
Invoke-RestMethod "http://127.0.0.1:8080/relay/v1/status?node_id=node-1" -Headers @{
  "x-amparius-tenant" = "tenant-a"
  "x-amparius-subject" = "operator-1"
  "x-amparius-token" = $env:AMPARIUS_RELAY_TOKEN
}
```

Contrato detalhado: `../docs/contracts/relay-operations-v1.md`. Decisão arquitetural:
`../docs/adr/0023-relay-fila-offline-contrato-caixa.md`. Transmissão, sincronização e
reconciliação continuam na RELAY-02.

## Verificação

```powershell
cargo fmt --manifest-path relay/Cargo.toml -- --check
cargo clippy --manifest-path relay/Cargo.toml --all-targets -- -D warnings
cargo test --manifest-path relay/Cargo.toml
```

