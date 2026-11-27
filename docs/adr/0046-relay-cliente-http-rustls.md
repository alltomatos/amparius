# ADR 0046 — Cliente HTTP do relay para o SaaS (reqwest + rustls)

Status: proposto em 27/11/2026 — slice de desenho (sem código), base para RELAY-02D/E.

## Contexto

O ADR 0026 e o contrato `relay-sync-v1` definem o `SyncEngine`/`SyncTransport` do
relay, mas documentam explicitamente que "o engine desta versão não abre
sockets nem simula integração com o SaaS". Hoje `relay/Cargo.toml` não tem
nenhum cliente HTTP: só `axum` (servidor, usado em `relay/src/http.rs` para o
contrato `RELAY-OPERATIONS v1`, que é a perna estação→relay) e, em
`dev-dependencies`, `http-body-util` só para exercitar esse servidor em teste.
Não existe dependência capaz de abrir conexão de saída.

O cliente (estação→relay, loopback) já resolveu o problema análogo no
ADR 0035 com `reqwest = { version = "0.12", default-features = false,
features = ["json"] }`, sem TLS porque o relay escuta em loopback ou atrás de
TLS local. A perna relay→SaaS desta vez cruza rede real até uma célula de
homologação com TLS Let's Encrypt (`hml.amparius.com.br`, Caddy/ACME —
`docs/userguide/operacao-staging.md`), então TLS é obrigatório no cliente.

## Decisão

Adotar `reqwest` também no relay, pelas mesmas razões do ADR 0035 (consistência
de família de crate, API `async` que já casa com `tokio` presente em
`relay/Cargo.toml`, suporte nativo a JSON), mas com TLS pure-Rust:

```toml
reqwest = { version = "0.12", default-features = false, features = ["json", "rustls-tls"] }
```

- **TLS:** `rustls-tls`, não `default-tls` (que traria OpenSSL/`native-tls` e
  dependência de biblioteca de sistema). `rustls` é pure-Rust, como
  `ed25519-dalek` e `sha2` já usados em `relay/src/snapshot.rs` e
  `relay/src/auth.rs`; evita variação de build entre Windows/Linux e simplifica
  o binário standalone do servidor da loja (ADR 0006).
- **Tamanho/dependências:** `default-features = false` remove o cliente DNS
  assíncrono padrão não necessário e o suporte a cookies/multipart que o relay
  não usa; mantém só `json` e `rustls-tls`. Isso é o mesmo padrão de
  minimização já aceito no ADR 0035.
- **Redirecionamento:** desabilitado (`redirect::Policy::none()`), espelhando a
  regra do ADR 0035 item 3 — o token Bearer não pode seguir redirect para outro
  host.
- **Timeout:** por requisição (`RequestBuilder::timeout`), não só no
  `Client` global, para permitir orçamento diferente entre `pull` de snapshot
  (mais tolerante) e `push` de operação (mais curto, para não travar a fila).

### Alternativas descartadas

- **`ureq`:** cliente síncrono. O relay já é `async`/`tokio` (servidor `axum`
  no mesmo processo); um cliente bloqueante exigiria `spawn_blocking` em cada
  chamada do `SyncEngine`, complicação sem ganho real de tamanho (`ureq` com
  `rustls` não é materialmente menor que `reqwest` minimizado).
- **`hyper` direto:** nível baixo demais para este slice — reimplementaria
  pool de conexão, retry de handshake TLS e parsing de resposta que `reqwest`
  já resolve; aumentaria a superfície de bugs no adaptador de transporte sem
  necessidade comprovada de controle fino.
- **`isahc`:** depende de `libcurl` (biblioteca de sistema), o que reintroduz o
  problema que `rustls` evita e complica build/cross-compile para o instalador
  Windows do servidor da loja.

## Consequências

- `relay/Cargo.toml` passa a declarar `reqwest` com `rustls-tls`; nenhuma
  dependência de OpenSSL/`native-tls` entra na árvore do relay.
- O novo adaptador HTTP implementa `SyncTransport::submit_versioned` (contrato
  `relay-sync-v1`) usando `reqwest::Client` como campo da struct, reaproveitando
  a classificação de resposta já validada no ADR 0035
  (`Accepted`/`Conflict`/`Rejected`/`Transient`) adaptada aos códigos do novo
  contrato `relay-saas-sync-http-v1`.
- `cargo tree --manifest-path relay/Cargo.toml` deve mostrar `rustls`/`ring` ou
  `aws-lc-rs` (conforme feature padrão da versão pinada) e nenhuma dependência
  `openssl-sys`; isso é o critério de aceite verificável desta ADR.
- Este ADR não implementa o adaptador; apenas fixa a escolha de crate/TLS para
  a issue vertical que o Engenheiro Rust Sync Fiscal executa depois (ver
  `docs/design/relay-sync-fiscal-issues-propostas.md`).

## Verificação (quando a issue de implementação rodar)

```powershell
cargo tree --manifest-path relay/Cargo.toml -p reqwest -e features
cargo tree --manifest-path relay/Cargo.toml | Select-String "openssl-sys"
```

A segunda linha deve retornar vazio.

Contrato relacionado: `docs/contracts/relay-saas-sync-http-v1.md`. Base:
ADR 0026, ADR 0035, contrato `relay-sync-v1`.
