# Governança de agentes do Amparius

`AGENTS.md` é a fonte normativa do repositório, independente do modelo, IDE ou
cliente. Este documento organiza a operação; as regras técnicas permanecem em
`AGENTS.md`.

## Ordem de leitura

1. `AGENTS.md`.
2. `docs/roadmap/ROADMAP.md` e ADR/DER relacionados.
3. `skills/amparius-team-governance/SKILL.md`.
4. Skill do papel em `skills/`.
5. Skill especializada adicional, como `skills/nfse-documentacao-tecnica/`.

## Contrato operacional

- Uma fatia vertical por vez; sem paralelismo não autorizado.
- Relatório de outro agente é evidência a conferir, nunca prova automática.
- Preserve mudanças existentes e nunca edite migration aplicada.
- Tenant/RLS, idempotência, fail-closed fiscal, LGPD e ausência de segredos são
  critérios transversais.
- “Pronto” exige teste reproduzível, diff revisado, documentação e riscos
  restantes. Sem isso, use “em andamento”, “parcial” ou “bloqueado”.
- Após uma fatia, registre arquivos, comandos, resultados, commit/PR quando
  houver e o próximo item da fila.

## Mapa de papéis

| Papel | Skill | Responsabilidade principal |
|---|---|---|
| Backend Go | `skills/amparius-go-backend/` | módulos, API, worker, outbox e integrações |
| PostgreSQL | `skills/amparius-postgres/` | migrations, RLS, grants, índices e isolamento |
| Fiscal | `skills/amparius-fiscal/` | tax_rule, RTC, XSD, contingência e auditoria |
| Plataforma | `skills/amparius-platform/` | ADR, fronteiras, offline e sincronização |
| Rust edge | `skills/amparius-rust-edge/` | relay, cliente, fila local e sync |
| QA | `skills/amparius-qa-contract/` | aceite, regressão, golden e performance |
| Documentação | `skills/amparius-documentation/` | ADR, DER, roadmap e userguide |
| Produto/UX | `skills/amparius-product-ux/` | jornadas, estados, telas e ajuda contextual |
| Pesquisa NFS-e | `skills/amparius-nfse-research/` | fontes oficiais e versões técnicas |

Skills são contratos de trabalho, não substituem revisão humana, testes ou
permissões. Se o modelo mudar, o novo agente deve seguir os mesmos arquivos.
