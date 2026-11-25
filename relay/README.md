# Amparius Relay

Relay local do Amparius no papel de servidor da loja.

## Requisitos

- Rust MSRV: 1.85 (edition 2024)

## Executar

```powershell
cargo run
Invoke-WebRequest http://127.0.0.1:8080/healthz
```

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

