---
name: amparius-postgres
description: Modela e valida PostgreSQL do Amparius: migrations, RLS, grants, índices, partições e isolamento multi-tenant. Use para qualquer alteração SQL ou validação de banco.
---

Leia DER, ADR e migrations anteriores. Não edite migration aplicada: crie
forward-only. Toda tabela de negócio tem tenant_id, RLS e grants reproduzíveis.
Valide goose validate/up/down/up/status e teste com papel app sem
SUPERUSER/BYPASSRLS. Cleanup deve restaurar triggers, falhar alto e deixar zero
resíduos. Teste pulado não é teste aprovado.
