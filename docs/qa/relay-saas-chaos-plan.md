# Plano de teste de caos — relay ↔ SaaS (AGENTS.md §5)

Status: **proposto** em 27/11/2026, fatia de desenho (Q2). Cobre a exigência
de AGENTS.md §5 ("Teste de caos do relay: queda de rede no meio da emissão,
reconexão, reconciliação") para a perna relay→`cmd/api` descrita em
`docs/contracts/relay-saas-sync-http-v1.md`. Depende da implementação do
cliente HTTP (ADR 0046) e do receptor Go (lacuna listada no contrato); este
plano pode ser executado **parcialmente hoje** com os testes já existentes em
memória (`relay/src/sync.rs`, `relay/src/protocol.rs`) e **totalmente** só
depois da issue de implementação.

## Objetivo

Provar, de forma reproduzível, que: (1) queda de rede no meio do envio não
perde nem duplica operação; (2) reconexão reaplica exatamente o pendente; (3)
rejeição/conflito do servidor nunca é tratado como sucesso; (4) expiração do
snapshot de licença/IA fecha o acesso mesmo sem rede para renovar.

## Cenários

### C1 — Queda de rede durante o `push`, meio do lote

**Setup:** servidor de teste HTTP que aceita a primeira operação do lote
(`200 applied`) e então recusa conexão (`ConnectionRefused`/timeout) nas
seguintes.

**Execução:** `SyncEngine::sync_once` processa um lote de N operações
`pending` do mesmo `node_id`.

**Critério de aceite:**
- a 1ª operação fica `synced` com `server_cursor` persistido;
- a 2ª (que falhou) fica `pending`, `attempts` incrementado em 1, `last_error`
  = `sync_retry:transport_unavailable` ou `sync_retry:transport_timeout`
  (nunca a mensagem HTTP crua);
- as operações seguintes do lote **não são tentadas** nesse ciclo (mesma regra
  já provada em `relay/src/sync.rs::transport_retry_is_limited_and_terminal_failure_is_visible`
  — o `SyncEngine` para na primeira falha de transporte);
- repetir `sync_once` com o servidor de teste saudável de novo reaplica a
  mesma 2ª operação com o mesmo `operation_id`/`idempotency_key`, sem recriar
  sequência, e avança para as demais.

**Comando (hoje, nível engine):**
```powershell
cargo test --manifest-path relay/Cargo.toml network_drop_keeps_operation_pending_and_reconnect_replays_same_id
```
**Comando (após issue HTTP):** equivalente com `reqwest` contra um servidor
`axum`/`wiremock` local que derruba a conexão no meio do lote — a ser
adicionado em `relay/tests/` pela issue de implementação.

### C2 — Reconexão após esgotar o limite de retry (falha terminal)

**Setup:** servidor sempre indisponível/timeout.

**Execução:** `sync_once` chamado `retry_limit` vezes (default 3).

**Critério de aceite:**
- na tentativa que atinge o limite, a operação vira `failed`, cria uma
  `ReviewTask` (deduplicada, sem payload/PII), e o modo de status muda para
  `degraded` (`RelayMode::Degraded`);
- tentativas adicionais depois do limite **não** reabrem a operação
  automaticamente — exige revisão explícita, nunca last-write-wins;
- reconectar o servidor depois disso **não** reaplica essa operação até que
  a `ReviewTask` seja resolvida (fora do escopo desta fatia: não há endpoint
  de resolução ainda).

**Comando:**
```powershell
cargo test --manifest-path relay/Cargo.toml transport_retry_is_limited_and_terminal_failure_is_visible
```

### C3 — Conflito de versão (`stale_version`) não é last-write-wins

**Setup:** servidor responde `409 stale_version` com `current_version`
diferente do `base_version` enviado em um `master_upsert`.

**Critério de aceite:**
- a operação vira `conflict`/`failed`, cria `ReviewTask` tipo
  `MasterConflict`, com `tenant_id`, `operation_id`, código sanitizado — sem
  payload nem resposta remota crua;
- reaplicar o mesmo lote depois **não** cria segunda `ReviewTask` para a mesma
  operação (dedup já provado em
  `relay/src/sync.rs::conflicts_in_master_money_and_fiscal_are_blocked_and_reviewed_once`);
- o valor do mestre nunca é sobrescrito localmente a partir da resposta de
  conflito.

**Comando (nível engine, já passa hoje):**
```powershell
cargo test --manifest-path relay/Cargo.toml conflicts_in_master_money_and_fiscal_are_blocked_and_reviewed_once
```

### C4 — Queda de rede no meio da emissão fiscal especificamente

**Setup:** operação `fiscal_emission` em trânsito; servidor cai antes de
responder (não dá tempo de saber se aplicou ou não do lado do SaaS).

**Critério de aceite:**
- a operação permanece `pending` (nunca `synced` nem `failed` por suposição);
- reconectar reaplica a **mesma** operação com o **mesmo** `idempotency_key`;
- se o servidor já tinha aplicado (duplicata do lado dele), a resposta ao
  replay é `duplicate` com o `server_cursor` original — o relay não emite uma
  segunda vez nem trata como novo documento;
- se o servidor rejeitar por regra fiscal, vira `fiscal_rejected` +
  `ReviewTask`, nunca "sucesso silencioso".

**Comando (hoje, nível engine):**
```powershell
cargo test --manifest-path relay/Cargo.toml fiscal_rejection_is_failed_once_and_creates_tenant_scoped_review_task
```
**Pendência:** o cenário real de "servidor caiu bem no meio, antes do ACK" só
é totalmente verificável com o cliente HTTP real (ADR 0046) e um servidor de
teste que aceita a conexão TCP, recebe os bytes e fecha sem responder — isso
entra na issue de implementação, não neste slice de desenho.

### C5 — Snapshot de licença/IA expira sem rede para renovar

**Setup:** `SignedSnapshot` com `valid_until = T`; relógio local avança além
de `T` sem conseguir alcançar `GET /v1/sync/snapshot`.

**Critério de aceite:**
- `SignedSnapshot::verify` retorna `Expired` a partir de `now >= valid_until`
  (já provado em `relay/src/snapshot.rs::expired_snapshot_fails_closed`);
- o relay passa a tratar módulos opcionais como inativos e a IA como
  desabilitada — **nunca** mantém o último estado "ativo" conhecido além de
  `valid_until`;
- quando a rede volta e um novo snapshot válido chega, o acesso é restaurado
  sem reinício manual.

**Comando (hoje, nível de verificação de assinatura):**
```powershell
cargo test --manifest-path relay/Cargo.toml expired_snapshot_fails_closed
cargo test --manifest-path relay/Cargo.toml wrong_tenant_fails_before_signature_acceptance
cargo test --manifest-path relay/Cargo.toml payload_tampering_invalidates_signature
```
**Pendência:** a busca periódica do snapshot via HTTP e o gatilho de
"desativar módulo ao expirar" em tempo real são parte da issue de
implementação (não existem em `relay/src/*.rs` hoje fora da verificação
criptográfica).

### C6 — Reordenação/lacuna de sequência após múltiplas quedas

**Setup:** duas quedas consecutivas deixam a origem reenviar um lote em que a
próxima `seq` esperada no servidor é maior que a enviada (gap) por causa de
uma operação que nunca chegou a ser criada localmente (ex.: processo do relay
reiniciado entre o incremento de `seq` e a persistência — cenário de borda).

**Critério de aceite:**
- o servidor responde `out_of_order` com `expected_seq`, nunca preenche o
  buraco silenciosamente;
- o relay trata como rejeição classificada (`Conflict`), nunca como sucesso;
- a reconciliação dessa lacuna fica explicitamente fora desta fatia (mesma
  limitação já registrada em `relay-sync-v1`: "não inventa o evento
  faltante").

## O que este plano não prova

- Não prova TLS real contra `hml.amparius.com.br` (isso é homologação manual,
  registrada em `docs/qa/homologacao-*.md`, depois que o cliente existir).
- Não prova comportamento sob carga (benchmark é item separado de
  AGENTS.md §5).
- Não prova que o control plane realmente assina e serve
  `GET /v1/sync/snapshot` — essa rota não existe ainda (ver lacunas no
  contrato).

## Evidência exigida no PR da issue de implementação

Cada cenário acima precisa de um teste automatizado reproduzível (comando +
saída colada, AGENTS.md §9.7) antes de ser considerado coberto. C1–C3 e C5 já
têm cobertura no nível de engine/criptografia hoje; C4 e C6 ficam pendentes até
o cliente HTTP e o receptor Go existirem.
