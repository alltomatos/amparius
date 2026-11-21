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
- `docs/data/DER.md` — modelo de dados
- `docs/uml/UML.md` — componentes, sequências, estados
- `docs/adr/` — decisões registradas
- `docs/research/receita-federal.md` — RTC e DANFSe
- `docs/roadmap/ROADMAP.md` — fases e critérios de saída
- `AGENTS.md` — regras obrigatórias para agentes e pessoas

## Ambiente (quando o código começar)

```bash
docker compose up -d      # Postgres, Redis, RabbitMQ, MinIO
go run ./cmd/api          # SaaS
go run ./cmd/worker       # worker
cargo run -p amparius-relay
```

## Estado

Fase 0 — documentação. Nenhum código de produção ainda.

## Regras que valem para todo mundo

1. Fiscal não se estima.
2. Toda query passa por RLS com tenant no contexto.
3. Dinheiro em NUMERIC/int64; nunca float.
4. Migração forward-only.
5. Mudança estrutural exige ADR.
6. Nada de segredo no repositório.
7. Pronto exige teste ou evidência.
