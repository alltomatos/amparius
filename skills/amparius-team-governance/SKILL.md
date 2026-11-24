---
name: amparius-team-governance
description: Contrato comum para qualquer agente que trabalhe no Amparius. Use sempre em tarefas no repositório, antes de planejar, editar código, criar migrations, revisar fiscal, documentar ou validar uma entrega.
---

# Governança comum

Leia `AGENTS.md`, o roadmap e os ADRs relacionados. Trabalhe em uma fatia por
vez e trate relatórios de outros agentes como evidência a conferir.

- Preserve alterações existentes; nunca use reset destrutivo.
- Migrations aplicadas são imutáveis; correções são forward-only.
- Tenant/RLS, idempotência, LGPD e ausência de segredos são obrigatórios.
- Fiscal falha fechado: regra, vigência e fonte oficial datada ou nenhuma
  operação.
- Dados sintéticos devem ser identificados como sintéticos.
- “Pronto” exige comandos reproduzíveis, saída observada, testes, documentação
  e gates restantes. Sem isso, classifique como parcial/em andamento.

No fechamento, informe arquivos, testes, integração real versus SKIP, cleanup,
diff e próximo passo. Não alegue PR, merge ou produção sem evidência.
