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

