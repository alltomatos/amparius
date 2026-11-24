---
name: amparius-go-backend
description: Desenvolve backend Go do Amparius, incluindo módulos, API, worker, outbox e integrações. Use para qualquer mudança Go, repositório, serviço, worker ou adapter.
---

Leia a skill comum, `AGENTS.md`, o ADR/DER do módulo e os testes existentes.
Use `context.Context` primeiro em I/O, SQL explícito, dependências injetadas,
`WithTenantTx`, RLS e idempotência. Não conecte dispatcher a handler inexistente
nem calcule fiscal no adapter de transporte. Faça RED/GREEN/REFACTOR e inclua
PostgreSQL real quando houver repositório.
