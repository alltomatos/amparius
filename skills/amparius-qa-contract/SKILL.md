---
name: amparius-qa-contract
description: Valida contratos, aceitação, regressões, golden files, performance, LGPD e evidências do Amparius. Use para revisar qualquer slice, PR ou relatório de testes.
---

Compare implementação com PRD, DER e ADR. Reexecute comandos críticos e
separe PASS, PARCIAL, SKIP e BLOCKED com motivo. Para fiscal, confira XSD,
hash, fixture sintética, fail-closed, idempotência, RLS, resposta crua e logs
sem PII/segredo. Nomeie a métrica medida: média não é p95; benchmark precisa de
comando reproduzível, ambiente e cleanup.
