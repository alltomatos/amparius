# Engenharia — Amparius

Documento interno da equipe. O README da raiz é comercial e não deve conter detalhes técnicos.

## Stack

| Camada | Tecnologia |
|---|---|
| SaaS | Go (binários `api` e `worker`) |
| Borda | Rust (relay local e cliente desktop) |
| Dados | PostgreSQL 16+, Redis 7+, RabbitMQ 3.13+ |
| Arquivos | Object storage S3-compatible (XML, PDF, anexos) |
| IA | Gateway MCP interno + provedores de modelo |

## Forma

Monólito modular: um repositório, dois deploys no SaaS (`api`, `worker`) e um binário de relay. Módulos com schema e contrato próprios; integração por serviço público ou evento versionado. Microsserviço só com ADR e evidência de gargalo.

Implantação por **células** em VPS (ver `docs/architecture/SCALING.md`): cada célula atende um conjunto de tenants; o control plane decide a colocação.

## Estrutura prevista

```
cmd/api/            HTTP do SaaS
cmd/worker/         filas, jobs, emissão fiscal
internal/modules/   identity, party, catalog, finance, fiscal, funeral, cemetery, floriculture, inventory, documents, ai, sync
internal/platform/  banco, fila, cache, auth, observabilidade
migrations/         SQL forward-only
relay/              workspace Rust do relay (papel servidor da loja)
client/             workspace Rust do cliente desktop (papel estação)
docs/               PRD, arquitetura, DER, UML, ADRs, pesquisa
```

## Documentos

- `docs/prd/PRD.md` — produto e escopo
- `docs/architecture/ARCHITECTURE.md` — arquitetura, relay, fiscal, IA
- `docs/architecture/SCALING.md` — células em VPS, capacidade e migração de tenant
- `docs/design/MOCKUPS.md` — telas, decisões visuais e como regenerar
- `docs/design/AI-ASSISTANT.md` — contrato do assistente contextual
- `docs/data/DER.md` — modelo de dados
- `docs/uml/UML.md` — componentes, sequências, estados
- `docs/adr/` — decisões registradas
- `docs/research/receita-federal.md` — RTC e DANFSe
- `docs/roadmap/ROADMAP.md` — fases e critérios de saída
- `AGENTS.md` — regras obrigatórias para agentes e pessoas

## Ambiente

```bash
.\scripts\dev.ps1 up                        # docker compose up -d (Postgres, Redis, RabbitMQ, MinIO)
go run ./cmd/api                            # SaaS
go run ./cmd/worker                         # worker
cargo run --manifest-path relay/Cargo.toml  # relay (servidor da loja)
cargo run --manifest-path client/Cargo.toml # client (estação)
```

## Estado

Fase 1 em andamento: `cmd/api` + `cmd/worker` (Go, health), `relay/` +
`client/` (Rust, health), migrações `identity` e `party/catalog` com RLS,
acesso Go a `party` e `catalog` com tenant obrigatório por transação e teste
real de isolamento PostgreSQL.

## Regras que valem para todo mundo

1. Fiscal não se estima.
2. Toda query passa por RLS com tenant no contexto.
3. Dinheiro em NUMERIC/int64; nunca float.
4. Migração forward-only.
5. Mudança estrutural exige ADR.
6. Nada de segredo no repositório.
7. Pronto exige teste ou evidência.

## Acesso PostgreSQL e RLS

### Control plane e enforcement de entitlement

O control plane usa um PostgreSQL separado, acessado pelo DSN
`AMPA_CONTROL_POSTGRES_DSN`; o DSN da célula (`AMPA_TEST_POSTGRES_DSN`) não
pode ser reutilizado. O pacote `internal/modules/entitlement` consulta apenas
`entitlement.module` e `entitlement.license_assignment` e não escreve
`entitlement_event` neste slice.

O catálogo permite diretamente os módulos `core` ativos. Módulos `optional`
exigem `license_assignment.status = 'active'`, o mesmo `module_code` e
`company_id`, além de `valid_from <= data <= valid_to` (com `valid_to` nulo
permitido). Módulo inexistente/inativo, assignment ausente, vencido ou
revogado bloqueia; falha de banco também bloqueia com erro distinto
`ErrEntitlementUnavailable`.

O checker exige o tenant em `db.WithTenant(ctx, tenantID)` e abre uma transação
que executa `set_config('app.tenant_id', ..., true)` antes da consulta, para
que o RLS do control plane seja efetivo. O `company_id` é recebido pelo caso
de uso a partir do contexto autenticado; não é derivado nem confiado ao corpo
HTTP. Ainda não existe um `company context` global, portanto a integração HTTP
deve preservar essa fronteira ao adaptar a chamada.

O teste de integração do control plane (`AMPA_CONTROL_POSTGRES_DSN`) deve usar
um papel de aplicação sem `SUPERUSER` e sem `BYPASSRLS` (por exemplo,
`amparius_control_app`). Sem esse DSN, o teste é marcado como `SKIP` e o
relatório deve registrar a ausência claramente.

### Migration Finance

`migrations/0003_finance.sql` cria o schema `finance` para contas, centros de
custo, contas bancarias, titulos, parcelas, pagamentos e caixa. Os codigos de
dominio continuam em `identity.domain_value`; a migration nao cria enums PG.
Todas as tabelas carregam `tenant_id` e RLS. `finance.ledger_entry` e
particionado por competencia mensal e, assim como pagamentos e movimentos de
caixa, e append-only. O limite de pagamento e validado no banco em trigger,
bloqueando a parcela durante a soma dos pagamentos para evitar sobrepagamento
por corrida.

Os repositórios Go de `party`, `catalog` e `finance` usam `internal/platform/db`.
O repositório Finance expõe valores monetários como centavos `int64` e converte
explicitamente para `NUMERIC(18,2)`, mantendo pagamentos, movimentos de caixa e
lançamentos em transações isoladas por tenant. O tenant
é colocado no `context.Context` com `db.WithTenant`; cada operação abre uma
transação e executa `SELECT set_config('app.tenant_id', $1, true)` antes de
qualquer query. Sem tenant no contexto, a operação falha com
`db.ErrTenantRequired`.

O teste de isolamento deve usar `AMPA_TEST_POSTGRES_DSN` apontando para um
papel de aplicação sem `SUPERUSER` e sem `BYPASSRLS`. O teste Finance também
exige `AMPA_TEST_POSTGRES_ADMIN_DSN`, uma segunda conexão administrativa usada
somente no `t.Cleanup` para remover os tenants de teste e seus registros
append-only; sem esse DSN, nenhum dado Finance é criado. O usuário
administrativo `amparius` do Compose é superusuário e não serve para provar RLS,
mas pode ser usado exclusivamente no cleanup. Em ambiente local, após as
migrações, o papel de aplicação pode ser preparado com:

```sql
CREATE ROLE amparius_app LOGIN PASSWORD 'defina-localmente' NOSUPERUSER NOBYPASSRLS;
GRANT USAGE ON SCHEMA identity, party, catalog, finance TO amparius_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity, party, catalog, finance TO amparius_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity, party, catalog, finance TO amparius_app;
GRANT EXECUTE ON FUNCTION public.uuidv7() TO amparius_app;
```

O segredo não deve ser versionado; o DSN é somente variável de ambiente.

### Migration Fiscal

`migrations/0004_fiscal.sql` cria o schema `fiscal` para a configuração fiscal
por empresa, certificados referenciados por cofre/KMS e regras tributárias
datadas. Os códigos são domínios em `identity.domain_value`, validados por
trigger; não há enums PostgreSQL, alíquotas embutidas nem tabelas de códigos
fiscais nesta etapa.

`fiscal.tax_rule` aceita regras oficiais globais (`tenant_id IS NULL`) somente
para leitura e regras de exceção isoladas por tenant. A vigência usa intervalo
half-open e exclusão GiST para impedir sobreposição da mesma chave lógica.
Regras oficiais são append-only; novas publicações devem ser novas linhas com
fonte, versão, data e hash SHA-256. O motor fiscal futuro deve falhar fechado
com `ErrTaxRuleNotFound` quando não encontrar regra oficial datada.

O checker Go consulta somente linhas vigentes em `valid_from <= competência <
valid_to` (ou sem `valid_to`). Se houver regra do tenant, ela tem precedência
explícita sobre a global; uma única candidata é obrigatória e ausência ou
ambiguidade falha fechada. Erros de banco retornam `ErrTaxRuleUnavailable`,
distinto de `ErrTaxRuleNotFound`. O tenant é obrigatório no contexto via
`db.WithTenant`, e a transação configura `app.tenant_id` antes da consulta.
Company/establishment e município devem ser resolvidos pela sessão autenticada
e pelo caso de uso, nunca confiados diretamente ao corpo HTTP. Este slice não
importa nem insere dados tributários oficiais.

As combinações de `tax_type`, escopo e códigos fiscais são validadas no banco;
as formas de cálculo exigem os campos próprios de `basis_kind` (inclusive
faixa do Simples e valor por unidade). Hashes de certificado e regra são
hexadecimais minúsculos; `source_published_at` e `import_batch` identificam a
origem obrigatória de cada regra.

Certificados nunca armazenam senha, PIN, chave privada, PFX, token ou bytes
criptográficos: apenas metadados e uma referência opaca ao Vault/KMS.

`0005_fiscal_documents.sql` acrescenta a trilha operacional fiscal: `fiscal_document`
e `fiscal_item` particionados por competência mensal, `tax_calc` com entrada e
hash da regra usada no snapshot, e `emission_attempt` com chave de idempotência
por tenant e resposta bruta/hash por tentativa. As tabelas têm RLS, chaves
compostas tenant/documento/competência para impedir referências cruzadas e
histórico append-only onde aplicável; nenhum dado tributário ou seed de alíquota
é inserido. A emissão guarda apenas a referência do XML em object storage e seu
hash no documento.

### Contrato Go de documentos fiscais

`internal/modules/fiscal/documents.go` concentra o acesso às tabelas da
migração 0005. Todas as operações recebem `context.Context` primeiro e usam
`db.WithTenantTx`; sem tenant no contexto a operação falha fechada. O contrato
expõe documentos, itens, snapshots `tax_calc` e tentativas de emissão. Valores
monetários chegam e saem como centavos `int64`, convertidos para `numeric` no
SQL; quantidade e alíquota permanecem textuais para não usar `float`.

Snapshots, itens e tentativas são somente inclusão. `RegisterEmissionAttempt`
usa `(tenant_id, idempotency_key)` como chave de replay e devolve a tentativa
original sem atualizar a resposta. O histórico pode ser consultado por
documento/competência e as respostas são apenas corpo, tipo, protocolo e hash;
segredos não fazem parte do contrato.

A migration 0008 adiciona à tentativa os metadados de transporte necessários
à NFS-e (`http_status`, `ambiente`, `chave_acesso`, `dps_id` e
`request_hash`). Hashes são SHA-256 hexadecimais minúsculos; ambiente é domínio
versionado (`producao_restrita` ou `producao`) e nenhuma coluna guarda segredo.

### Outbox transacional e dispatch

`migrations/0007_outbox.sql` cria o schema `outbox` com `outbox.event` e a
trilha append-only `outbox.event_attempt`. O evento carrega `tenant_id`,
`aggregate_type`, `aggregate_id`, `event_type` (`<modulo>.<entidade>.<acao>`),
`event_version`, `payload jsonb` objeto, `idempotency_key`, `status`,
`attempts`, `max_attempts`, `next_attempt_at`, `last_error`, `created_at` e
`processed_at`. Os estados são domínios em `identity.domain_value`
(`outbox_event_status`, `outbox_attempt_outcome`), sem enum PostgreSQL. O `id`
é `uuidv7` e todo tempo é `timestamptz`.

A chave `(tenant_id, idempotency_key)` é única; reenviar a mesma chave com o
mesmo conteúdo devolve o evento original, e com conteúdo diferente falha com
`outbox.ErrIdempotencyConflict`. Um trigger rejeita `DELETE`, congela os campos
de identidade e só aceita `pending -> processing` e
`processing -> processing|processed|pending|failed`; `attempts` avança uma
unidade por claim. `outbox.event_attempt` registra `claimed`, `succeeded`,
`retry` e `failed` com unicidade por tentativa/resultado e também é append-only.

O claim é por tenant e roda sob RLS: um único `UPDATE ... FROM (SELECT ... FOR
UPDATE SKIP LOCKED)` incrementa `attempts`, marca `processing` e grava o lease
em `next_attempt_at`. A única leitura cross-tenant do projeto é
`outbox.pending_tenants(integer)`, função `SECURITY DEFINER` que devolve apenas
`tenant_id` de quem tem evento elegível; ela é executada por
`db.WithSystemTx`, a transação sem tenant reservada ao dispatcher. Nenhum
payload atravessa essa função.

`internal/modules/outbox` expõe `Append`, `GetByID`, `GetByIdempotencyKey`,
`Claim`, `Succeed`, `Fail`, `ReapExpired` e `PendingTenants`, todos com
`context.Context` primeiro e SQL explícito. `Fail` persiste `last_error`
(limitado a 1000 runes), aplica backoff exponencial de 2s a 1h e, ao atingir
`max_attempts`, fecha o evento como `failed`; `ReapExpired` cobre worker que
morreu depois do último claim. O `Dispatcher` recebe um `Handler`, drena cada
tenant em sua própria transação e nunca registra payload em log. Ele não é
ligado ao `cmd/worker` neste slice porque ainda não existe handler de negócio:
marcar eventos como processados sem efeito seria pior que não consumir.

As permissões são explícitas: `amparius_app` recebe `SELECT/INSERT/UPDATE` em
`outbox.event`, `SELECT/INSERT` em `outbox.event_attempt` e `EXECUTE` apenas em
`outbox.pending_tenants`. O papel não tem `SUPERUSER` nem `BYPASSRLS`; o teste
de integração usa `AMPA_TEST_POSTGRES_DSN` com `amparius_app` e
`AMPA_TEST_POSTGRES_ADMIN_DSN` apenas no `t.Cleanup`. Retenção e expurgo de
eventos processados ficam para um slice próprio, com política de auditoria e
LGPD definida.
