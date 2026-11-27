# Propostas de issue vertical — relay→SaaS (RELAY-02D/E)

Status: **texto pronto, não aberto como issue** (conforme pedido do Líder
Amparius). Para o Engenheiro Rust Sync Fiscal executar depois, em sequência.
Base: ADR 0026, ADR 0045, contratos `relay-sync-v1`,
`relay-saas-sync-http-v1`, plano de caos `relay-saas-chaos-plan.md`.

Nenhuma destas issues deve tocar `skills/nfse-documentacao-tecnica/**`,
migrations ou código Go — a perna Go (receptor `/v1/sync/*`) é lacuna
registrada no contrato e precisa de issue própria, fora deste pacote Rust.

---

## Issue 1 — RELAY-02D: cliente HTTP `reqwest`/`rustls` para `SyncTransport`

**Escopo:** adicionar `reqwest` com `rustls-tls` a `relay/Cargo.toml` (ADR
0045) e implementar um adaptador `HttpSyncTransport` que satisfaz o trait
`SyncTransport` (`relay/src/sync.rs`), cobrindo `submit_versioned` para enviar
um `SyncEnvelope` via `POST /v1/sync/operations` conforme
`docs/contracts/relay-saas-sync-http-v1.md`. Não inclui o receptor Go (fora de
escopo — é lacuna de API registrada, issue separada).

**Fora de escopo explícito:**
- Não implementa `GET /v1/sync/snapshot` (issue 2).
- Não implementa papel de serviço/rotação de token (limitação registrada no
  contrato); usa Bearer de configuração local (variável de ambiente ou
  arquivo de credencial, nunca hardcoded).
- Não altera `relay/src/http.rs` (servidor local, perna estação→relay).
- Não altera `skills/nfse-documentacao-tecnica/**` nem migrations.

**Critérios de aceite:**
1. `cargo tree --manifest-path relay/Cargo.toml -p reqwest -e features`
   mostra `rustls-tls` e `cargo tree --manifest-path relay/Cargo.toml | Select-String openssl-sys`
   retorna vazio.
2. Classificação de resposta (`Accepted`/`Conflict`/`Rejected`/`Transient`/
   `AuthExpired`) implementada e testada por tabela, uma linha por caso da
   tabela de `relay-saas-sync-http-v1` (push), incluindo o código novo
   `stale_version`.
3. Redirecionamento desabilitado (`redirect::Policy::none()`); teste prova que
   uma resposta de redirect é tratada como `Rejected`, nunca seguida.
4. `tenant_id` do token (simulado no teste) divergente do `tenant_id` local
   produz `403 tenant_mismatch` tratado como `Rejected` fail-closed — nunca
   retry automático.
5. Timeout configurável por chamada (não só no `Client` global); teste prova
   que `push` e um eventual `pull` (issue 2) podem ter orçamentos diferentes.
6. Retry limitado com backoff exponencial e full jitter determinístico
   (semente `(tenant_id, origin_node, event_id, attempt)`); teste prova que o
   mesmo estado gera o mesmo atraso (reprodutível sem relógio global), igual
   ao padrão do ADR 0035.
7. Cenário C1 do plano de caos (`docs/qa/relay-saas-chaos-plan.md`) reproduzido
   com servidor de teste HTTP real (ex.: `wiremock` ou `axum` de teste) que
   aceita a 1ª operação e derruba a conexão na 2ª: resultado igual ao já
   provado no nível de engine (`network_drop_keeps_operation_pending_and_reconnect_replays_same_id`),
   agora com socket real.
8. `cargo fmt --manifest-path relay/Cargo.toml -- --check`,
   `cargo test --manifest-path relay/Cargo.toml` e
   `cargo clippy --manifest-path relay/Cargo.toml --all-targets --all-features -- -D warnings`
   passam, com saída colada no PR.

**Dependências:** nenhuma issue Rust anterior pendente (ADR 0026/0035 já
aceitos); depende de `docs/contracts/relay-saas-sync-http-v1.md` estar
estável (este slice). **Não depende** do receptor Go existir — pode (e deve)
ser testada com servidor de teste local, igual ao padrão já usado para
`relay/src/http.rs` e para o cliente (ADR 0035).

---

## Issue 2 — RELAY-02E: verificação periódica de snapshot assinado via HTTP

**Escopo:** implementar a busca de `GET /v1/sync/snapshot`
(`docs/contracts/relay-saas-sync-http-v1.md`) usando o mesmo
`HttpSyncTransport`/`reqwest::Client` da Issue 1, verificar com
`SignedSnapshot::verify` (`relay/src/snapshot.rs`, já existente) e expor, no
status do relay (`relay/src/http.rs::status`, ou campo novo equivalente), se o
snapshot vigente está `valid`/`expired`/`unavailable`.

**Fora de escopo explícito:**
- Não implementa o assinador no control plane (Go) — lacuna registrada,
  issue separada fora deste pacote.
- Não decide a UX de "módulo desativado" na UI do cliente — isso é do
  Designer de Produto/Engenheiro Go responsável pela UI, só o estado fica
  disponível no relay.
- Não altera `skills/nfse-documentacao-tecnica/**` nem migrations.

**Critérios de aceite:**
1. Snapshot expirado (`now >= valid_until`) nunca é tratado como válido,
   mesmo sem rede para buscar um novo — reaproveita
   `expired_snapshot_fails_closed` já existente e adiciona o caminho de busca
   HTTP ao redor dele.
2. Falha de rede (timeout/5xx) ao buscar snapshot novo **não estende** a
   validade do snapshot anterior além do `valid_until` original (cenário C5
   do plano de caos).
3. `TenantMismatch`/`InvalidSignature`/`UnsupportedSchema` na resposta são
   tratados como "sem snapshot válido" (fail-closed), nunca como "manter
   última configuração conhecida".
4. Teste prova que, ao expirar, módulos opcionais somem do conjunto
   "licenciado" local e a IA é marcada indisponível — sem exigir que o
   caller saiba calcular isso por conta própria (expor um método explícito,
   ex. `VerifiedSnapshot::is_usable(now)` ou equivalente).
5. `cargo test --manifest-path relay/Cargo.toml` cobre os cenários C5 do
   plano de caos com saída colada no PR.

**Dependências:** Issue 1 (reaproveita o `reqwest::Client`/classificação de
transporte). Depende de `relay/src/snapshot.rs` permanecer estável (não
prevê mudança de schema).

---

## Observação para quem triar

Nenhuma das duas issues cria o lado Go (`internal/modules/sync`, assinatura
do snapshot no control plane). Essa é uma lacuna distinta, de outro papel
(Go/control plane), e deve ser aberta separadamente referenciando a mesma
`docs/contracts/relay-saas-sync-http-v1.md` para as duas pontas ficarem
coerentes. Sem o lado Go, as Issues 1 e 2 só podem ser validadas contra
servidor de teste local — o que já é suficiente para o critério de aceite
listado, mas não prova o ciclo real em homologação.
