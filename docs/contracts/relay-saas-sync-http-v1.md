# Contrato RELAY-SAAS-SYNC-HTTP v1 (desenho — sem implementação)

Status: **proposto** em 27/11/2026, fatia Q2 (desenho/contrato). Não implementado;
`relay/Cargo.toml` não tem cliente HTTP e `cmd/api` não expõe nenhuma rota de
ingestão de sincronização (ver "Lacunas de API"). Base: ADR 0026, ADR 0035,
ADR 0045, contratos `relay-sync-v1`, `relay-operations-v1`, `auth-http-v1`,
`control-plane-context-v1`, `control-plane-entitlement-cp06-v1`.

## Fato verificado que motiva este contrato

`relay/Cargo.toml` só declara `axum` (servidor) e, em `dev-dependencies`,
`http-body-util`/`tower` para testar esse servidor. Não há `reqwest`, `hyper`
cliente nem qualquer crate capaz de abrir socket de saída em
`relay/src/*.rs`. `SyncEngine::sync_once` (`relay/src/sync.rs`) só chama o
trait `SyncTransport` injetado; os testes usam `ScriptedTransport`/
`EnvelopeTransport` em memória. Ou seja, a sincronização relay→SaaS hoje é
simulação local (AGENTS.md regra 4, "a operação não para", e a saída da Fase 2
do roadmap — "sync reconcilia" — não têm evidência real de rede).

## Escopo

Definir, para UM ciclo real em homologação (`hml.amparius.com.br`), o
protocolo HTTP entre o relay (cliente) e `cmd/api` (servidor) para:

1. **push** da fila pendente do relay (`SyncEnvelope`, protocolo v1/evento v1
   já definidos em `relay-sync-v1`);
2. **pull** de um snapshot assinado de licenças/entitlement e cota de IA, para
   o relay decidir localmente o que pode operar quando a rede cai de novo.

Não cobre: adaptador real de SEFIN/NFS-e (fica em FIS-02B/C), UI, PostgreSQL
local do relay, OIDC/MFA de produção (ADR 0024, adiado — ver "Limitação
explícita").

## Dois saltos continuam distintos

Como em `relay-sync-v1`: cliente→relay (`POST /relay/v1/operations`, loopback,
autoridade de append é do relay) é um salto diferente de relay→SaaS (este
contrato, rede real, autoridade de aplicação é do `cmd/api`). Este contrato
não muda o primeiro salto.

## Autenticação do relay

### Limitação explícita (OIDC/MFA adiado)

O contrato `auth-http-v1` exige Bearer validado por adaptador OIDC com MFA
elevado (`identity.app_user.mfa_enabled = true`) para todo humano. O relay
**não é um humano** e não tem MFA. Login administrativo real continua
dependente do ADR 0024, ainda não montado em produção (`cmd/api` não injeta
`PlatformAuthenticator`; rotas `/admin/v1` respondem `503` até lá, conforme
`platform-admin-licenses-v1`).

Para este slice, o relay autentica com a **mesma identidade de teste já usada
na homologação** (`docs/userguide/operacao-staging.md`: "a integração injeta
um autenticador de teste... sem autenticador, retorna `503
AUTHENTICATION_UNAVAILABLE`"). Isso significa:

- O relay recebe, por configuração local (variável de ambiente ou arquivo de
  credencial local, nunca hardcoded), um Bearer token de uma conta de serviço
  de teste já vinculada a `(tenant_id, company_id)` no banco de homologação,
  igual ao usado pelos testes de `GET /v1/funeral/agenda` e
  `GET /v1/finance/titles` no mesmo guia.
- Esse token **não tem MFA real nem rotação automática** nesta fatia. É uma
  limitação registrada, não uma simulação escondida: o relay não finge ter
  OIDC, apenas usa a mesma porta de entrada de teste que já existe em
  homologação.
- Produção exige um papel de serviço próprio (`relay_service_account` ou
  equivalente) com rotação de token e sem depender do autenticador de teste;
  isso fica fora deste slice e deve abrir ADR próprio quando o OIDC/MFA de
  produção (ADR 0024) for retomado.

### Formato

`Authorization: Bearer <token-de-teste>` em toda chamada relay→`cmd/api`,
igual às demais rotas `/v1` (`auth-http-v1`). O relay **não envia**
`X-Tenant-Id` nem qualquer tenant/company no corpo — o contrato
`control-plane-context-v1` já estabelece que isso é ignorado para autorização
e o tenant/empresa vêm do principal resolvido no banco. Isso também cobre a
regra "tenant sempre no contexto (RLS)": o relay nunca decide seu próprio
tenant: ele carrega o `tenant_id` local (do `AuthenticatedSubject` do relay,
`relay/src/auth.rs`) só para filtrar o que envia, e o servidor valida de forma
independente a partir do token.

**Invariante de falha fechada:** se o `tenant_id` local do relay não coincidir
com o `tenant_id` resolvido pelo servidor a partir do token, a resposta é
`403 TENANT_MISMATCH` (mesmo código de `control-plane-context-v1`) e o relay
trata como `Rejected` — nunca tenta "corrigir" e reenviar com outro tenant.

## Lacunas de API (o que falta em `cmd/api`)

Nenhuma destas rotas existe hoje. `grep` em `cmd/api` e `internal/modules/*`
não encontra `relay`, `SyncReceiver`, `/v1/sync` ou `/v1/relay`; não há módulo
`internal/modules/sync`. Isto é o levantamento de gap, não uma proposta de
código:

| Rota faltante | Direção | Equivalente de referência |
|---|---|---|
| `POST /v1/sync/operations` (push em lote, idempotente) | relay → cmd/api | `SyncReceiver::apply` (`relay/src/protocol.rs`, hoje só em memória/teste, sem contraparte Go) |
| `GET /v1/sync/cursor?origin_node=` | relay → cmd/api | não existe; precisa de tabela/view com cursor por `(tenant_id, origin_node)` |
| `GET /v1/sync/snapshot` (licenças + cota de IA, assinado) | relay → cmd/api | parcialmente coberto por `GET /v1/companies/{id}/entitlements` (`control-plane-context-v1`) e `ai.*` (ADR 0008), mas **sem** envelope assinado Ed25519 equivalente a `relay/src/snapshot.rs::SignedSnapshot` |
| Módulo Go `internal/modules/sync` (ou extensão de `controlplane`) | — | não existe; hoje `internal/modules/` tem `catalog, controlplane, entitlement, finance, fiscal, funeral, identity, outbox, party` — nenhum trata ingestão de operação do relay |
| Papel/credencial de serviço do relay (`relay_service_account`) | — | não existe; hoje auth é sempre humano (`identity.app_user` com MFA) |

O relay já tem, do lado dele, o que falta para consumir essas rotas quando
existirem: `SyncEnvelope` (protocolo versionado), `SignedSnapshot`
(verificação Ed25519 fail-closed) e `SyncTransport` (ponto de injeção). O
trabalho que falta é simétrico: Go precisa de um receptor persistente
equivalente ao `SyncReceiver` de referência, e de um assinador de snapshot
compatível com `SignedSnapshot::verify`.

## Push: `POST /v1/sync/operations` (proposto)

Corpo: um `SyncEnvelope` por operação (mesmo schema de `relay-sync-v1`,
`protocol_version=1`, `event_version=1`). Cabeçalho `Idempotency-Key` repete
`event_id`, como já faz o cliente no ADR 0035 item 1.

Resposta, por envelope, reaproveita a tabela de `relay-sync-v1` §"Ordem,
duplicidade e reaplicação":

| Situação | HTTP | `status` na resposta |
|---|---:|---|
| `seq` esperado para a origem, aplicado | `200`/`201` | `applied` + `server_cursor` |
| `master_upsert` aceito com versão autoritativa do servidor | `200` | `master_applied` + `server_cursor` + `authoritative_version` |
| mesmo `event_id`/conteúdo já aplicado | `200` | `duplicate` + `server_cursor` original |
| `seq` acima do esperado | `409` | `out_of_order` + `expected_seq` |
| mesma `seq`/`idempotency_key` com conteúdo diferente | `409` | `conflict` |
| `master_upsert` cujo `base_version` não bate com o mestre atual no servidor | `409` | **`stale_version`** + `current_version` |
| `tenant_id` do envelope ≠ tenant do token | `403` | `tenant_mismatch` |
| rejeição fiscal (emissão recusada pela regra fiscal) | `200` com corpo de rejeição classificada | `fiscal_rejected` + código sanitizado |
| `protocol_version`/`event_version` desconhecida ou envelope adulterado | `400` | `rejected` |

`stale_version` é o código novo deste contrato — nomeado para ficar
consistente com `409 STALE_VERSION` já usado em
`control-plane-entitlement-cp06-v1` e `platform-admin-licenses-v1` para o
mesmo tipo de conflito (versão otimista obsoleta), em vez de inventar outro
nome para o mesmo conceito.

**Lote e ordem:** o `cmd/api` processa o lote em ordem de `seq` por
`(tenant_id, origin_node)` e **para no primeiro `out_of_order`/`conflict`**
daquela origem — não pula para o próximo item da mesma origem, para não abrir
buraco na sequência (mesma regra do receptor de referência). Itens de outras
origens no mesmo lote continuam sendo processados.

## Pull: `GET /v1/sync/snapshot` (proposto)

Retorna um `SignedSnapshot` (mesmo schema de `relay/src/snapshot.rs`:
`schema_version`, `key_id`, `tenant_id`, `version`, `issued_at`,
`valid_until`, `payload`, `signature`) assinado Ed25519 pela chave do control
plane. `payload` contém, no mínimo:

```json
{
  "entitlements": [{"module": "funeral", "status": "active", "validTo": null}],
  "ai_quota": {"mode": "hard_stop", "remaining_cents": 5000}
}
```

- `valid_until` é curto (minutos, não horas) — o relay deve buscar de novo
  antes de expirar, não cachear por padrão longo.
- O relay verifica com `SignedSnapshot::verify(verifying_key, tenant_id, now)`,
  já implementado. Qualquer erro (`Expired`, `TenantMismatch`,
  `InvalidSignature`, `UnsupportedSchema`) é **fail-closed**: o relay trata
  módulo opcional como inativo e IA como desabilitada, igual ao comportamento
  já decidido para IA offline (nota de memória da equipe: "Offline: assistente
  desabilitado, sem consumo"). Nunca usa o último snapshot válido além de
  `valid_until`, mesmo que a rede esteja indisponível — a ausência de
  snapshot válido não autoriza uso, autoriza negação.
- Se `GET /v1/sync/snapshot` responder `5xx`/timeout, o relay trata como
  "sem snapshot novo": continua com o anterior **somente até `valid_until`**
  e depois fecha. Isso é diferente de `relay-sync-v1`, onde indisponibilidade
  de transporte mantém a operação `pending` para retry — aqui, indisponibilidade
  nunca estende a validade de um snapshot expirado.

## Falha, retry e backoff (relay → cmd/api)

Reaproveita a classificação do ADR 0035 (cliente→relay), adaptada a esta
perna com TLS real:

| Classe | Quando | Ação |
|---|---|---|
| `Accepted` | `200`/`201` com `applied`/`duplicate`/`master_applied`/`fiscal_rejected` | marca `synced`/idempotente, nunca reenvia |
| `Conflict` | `409` (`conflict`, `out_of_order`, `stale_version`) | marca `conflict`, cria `ReviewTask`, não repete automaticamente |
| `Rejected` | `400`, `403` (exceto `tenant_mismatch` tratado como erro fatal de config) | marca `failed`, `ReviewTask`, não repete |
| `Transient` | timeout, erro de conexão/TLS, `5xx`, `429` | retry com backoff |
| `AuthExpired` | `401` | interrompe o ciclo, não marca operação como falha — token precisa ser renovado fora do `SyncEngine` |

Retry: `max_attempts` configurável (nunca zero, default `3`, mesmo default de
`DEFAULT_SYNC_RETRY_LIMIT` em `relay/src/store.rs`), backoff exponencial com
full jitter determinístico (mesma técnica do ADR 0035, trocando a semente por
`(tenant_id, origin_node, event_id, attempt)` para reprodutibilidade em teste).
Ao esgotar tentativas, a operação já ia para `failed` + `ReviewTask`
(`relay-sync-v1` §"Retry limitado e falha terminal") — este contrato não muda
essa regra, só define quem dispara a tentativa real pela rede.

`push` e `pull` têm orçamentos de timeout independentes (ADR 0045): `pull` do
snapshot pode esperar mais porque não bloqueia a fila; `push` de operação deve
ter timeout curto para não travar `sync_once` em uma única operação lenta.

## Invariantes

- tenant do envelope e tenant do token precisam coincidir; divergência é
  `403 tenant_mismatch` fail-closed, nunca "a maioria vence";
- nenhuma indisponibilidade de rede estende a validade de um snapshot de
  licença/IA expirado;
- nenhum `5xx`/timeout é tratado como sucesso; nenhum `409` é reenviado sem
  revisão explícita;
- `master_upsert` só sincroniza com `authoritative_version` do servidor,
  nunca com versão local;
- o relay nunca envia o Bearer para um host diferente do configurado
  (sem redirect, ADR 0045).

## Verificação (quando a issue de implementação rodar)

Testes determinísticos com um servidor HTTP de teste local (igual ao padrão do
ADR 0035 para a perna cliente→relay), cobrindo cada linha das tabelas acima,
mais os cenários do plano de caos (`docs/qa/relay-saas-chaos-plan.md`). Nenhum
teste desta fatia depende de `hml.amparius.com.br` real nem de banco
PostgreSQL de homologação — isso fica para a homologação manual do ciclo
completo, registrada em `docs/qa/homologacao-*.md` quando o adaptador existir.

## Limites desta fatia

- Não implementa `internal/modules/sync` nem o cliente HTTP do relay — isso é
  trabalho de issue futura (`docs/design/relay-sync-fiscal-issues-propostas.md`).
- Não assina de fato nenhum snapshot em homologação; descreve o formato que a
  issue futura precisa produzir no control plane.
- Não resolve renovação de token do relay nem papel de serviço dedicado —
  registrado como limitação explícita, não como pendência silenciosa.
- Não substitui OIDC/MFA de produção (ADR 0024); usa a identidade de teste já
  presente em homologação, como autorizado pelo brief desta fatia.
