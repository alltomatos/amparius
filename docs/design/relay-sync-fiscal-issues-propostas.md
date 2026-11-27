# Propostas de issue vertical — relay→SaaS (RELAY-02D/E)

Status: **texto pronto, não aberto como issue** (conforme pedido do Líder
Amparius). Para o Engenheiro Rust Sync Fiscal executar depois, em sequência.
Base: ADR 0026, ADR 0046, contratos `relay-sync-v1`,
`relay-saas-sync-http-v1`, plano de caos `relay-saas-chaos-plan.md`.

Nenhuma destas issues deve tocar `skills/nfse-documentacao-tecnica/**`,
migrations ou código Go — a perna Go (receptor `/v1/sync/*`) é lacuna
registrada no contrato e precisa de issue própria, fora deste pacote Rust.

---

## Issue 1 — RELAY-02D: cliente HTTP `reqwest`/`rustls` para `SyncTransport`

**Escopo:** adicionar `reqwest` com `rustls-tls` a `relay/Cargo.toml` (ADR
0046) e implementar um adaptador `HttpSyncTransport` que satisfaz o trait
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

## Issue 3 — RELAY-02F: receptor Go `/v1/sync/*` e assinador de snapshot no control plane

**Escopo:** criar o módulo Go simétrico ao lado Rust, cobrindo as duas rotas
que faltam em `cmd/api` conforme a tabela de lacunas de
`docs/contracts/relay-saas-sync-http-v1.md`:
1. `POST /v1/sync/operations` — novo módulo `internal/modules/sync` (schema
   próprio, sem JOIN cross-schema, AGENTS.md §4) reimplementando em Go a
   lógica já provada em Rust por `relay/src/protocol.rs::SyncReceiver`:
   ordenação por `(tenant_id, origin_node, seq)`, dedup por
   `idempotency_key`, detecção de `out_of_order`/`conflict`/`stale_version`,
   e amarração de tenant via RLS (nunca tenant vindo do corpo, só do Bearer —
   mesmo padrão de `auth-http-v1`/`control-plane-context-v1`).
2. `GET /v1/sync/snapshot` — assinador Ed25519 no control plane que produz um
   `SignedSnapshot` byte-idêntico ao esperado por
   `relay/src/snapshot.rs::SignedSnapshot::verify`: mesma ordem de campos do
   `UnsignedSnapshot` (`schema_version`, `key_id`, `tenant_id`, `version`,
   `issued_at`, `valid_until`, `payload`) serializada em JSON canônico antes
   da assinatura, para o lado Rust aceitar sem reimplementar a verificação.

**Fora de escopo explícito:**
- Não implementa nem altera o cliente HTTP do relay (Issues 1 e 2, já
  cobertas no lado Rust).
- Não implementa papel de serviço/rotação de token do relay (limitação já
  registrada no contrato); usa o mesmo mecanismo de autenticador de teste de
  homologação enquanto ADR 0024 (OIDC/MFA) permanece adiado.
- Não altera `skills/nfse-documentacao-tecnica/**` nem migrations fora do
  necessário para criar o schema próprio do novo módulo `sync`.

**Critérios de aceite:**
1. Golden test prova que o JSON canônico gerado pelo assinador Go, assinado
   e verificado por `relay/src/snapshot.rs::SignedSnapshot::verify` (via
   fixture cruzada ou teste de integração), é aceito sem erro —
   byte-identidade com o que o Rust espera, não só "formato parecido".
2. `POST /v1/sync/operations` responde `applied`/`master_applied`/
   `duplicate`/`out_of_order`/`stale_version`/`tenant_mismatch`/
   `fiscal_rejected`/`rejected` exatamente como a tabela de push de
   `relay-saas-sync-http-v1.md`, com teste por tabela (uma linha por caso).
3. `GET /v1/sync/snapshot` nunca estende `valid_until` por falha de rede ou
   indisponibilidade de origem — se não houver dado fresco, retorna o
   snapshot assinado válido mais recente ou erro explícito, nunca inventa
   validade nova sem recalcular a partir da fonte de verdade.
4. Tenant mismatch e token sem escopo de `relay_service_account` (lacuna
   registrada no contrato) retornam erro fail-closed, nunca "melhor
   esforço".
5. `go test ./internal/modules/sync/...` e o teste de integração cruzada com
   `cargo test --manifest-path relay/Cargo.toml` (verificação de assinatura)
   passam, com saída colada no PR (AGENTS.md §9.7).

**Dependências:** depende de `docs/contracts/relay-saas-sync-http-v1.md`
(este slice) e da estrutura de módulo já usada por
`internal/modules/{identity,party,catalog,finance,fiscal,entitlement,outbox,controlplane}`
como referência de padrão; não depende da Issue 1/2 estarem implementadas
para começar (pode ser testada com um cliente de teste Go/`reqwest` de
fixture), mas a validação ponta-a-ponta em homologação só fecha com as três
prontas.

## Observação para quem triar

A Issue 3 (RELAY-02F) cobre o lado Go que faltava; é de outro papel
(Go/control plane) e deve ser atribuída separadamente, mas já tem texto
pronto aqui para não depender de nova rodada de desenho. Sem ela, as Issues
1 e 2 só podem ser validadas contra servidor de teste local — suficiente
para os critérios de aceite listados, mas não prova o ciclo real em
homologação.
