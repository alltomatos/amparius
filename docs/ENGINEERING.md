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

### Migration Finance

`migrations/0003_finance.sql` cria o schema `finance` para contas, centros de
custo, contas bancarias, titulos, parcelas, pagamentos e caixa. Os codigos de
dominio continuam em `identity.domain_value`; a migration nao cria enums PG.
Todas as tabelas carregam `tenant_id` e RLS. `finance.ledger_entry` e
particionado por competencia mensal e, assim como pagamentos e movimentos de
caixa, e append-only. O limite de pagamento e validado no banco em trigger,
bloqueando a parcela durante a soma dos pagamentos para evitar sobrepagamento
por corrida.

Os repositórios Go de `party` e `catalog` usam `internal/platform/db`. O tenant
é colocado no `context.Context` com `db.WithTenant`; cada operação abre uma
transação e executa `SELECT set_config('app.tenant_id', $1, true)` antes de
qualquer query. Sem tenant no contexto, a operação falha com
`db.ErrTenantRequired`.

O teste de isolamento deve usar `AMPA_TEST_POSTGRES_DSN` apontando para um
papel de aplicação sem `SUPERUSER` e sem `BYPASSRLS`. O usuário administrativo
`amparius` do Compose é superusuário e não serve para provar RLS. Em ambiente
local, após as migrações, o papel pode ser preparado com:

```sql
CREATE ROLE amparius_app LOGIN PASSWORD 'defina-localmente' NOSUPERUSER NOBYPASSRLS;
GRANT USAGE ON SCHEMA identity, party, catalog TO amparius_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA identity, party, catalog TO amparius_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA identity, party, catalog TO amparius_app;
GRANT EXECUTE ON FUNCTION public.uuidv7() TO amparius_app;
```

O segredo não deve ser versionado; o DSN é somente variável de ambiente.
