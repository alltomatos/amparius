# Equipe de agentes do Amparius

Este mapa registra responsabilidade e skill, não acopla o projeto a um modelo.
A troca de modelo preserva o papel, os arquivos de governança e os critérios de
aceite.

| Papel | Responsabilidade | Skill | Modelo observado em 24/11/2026 |
|---|---|---|---|
| Líder Amparius | fila, revisão e decisão de aceite | governança comum | definido pela sessão |
| Engenheiro Go | módulos, API, worker, outbox e integrações | `amparius-go-backend` | gpt-5.6-luna |
| Engenheiro de Dados | PostgreSQL, migrations, RLS e grants | `amparius-postgres` | gpt-5.6-luna |
| Especialista Fiscal | tax_rule, RTC, XSD e contingência | `amparius-fiscal` | deepseek-v4.1-flash |
| Arquiteto de Plataforma | ADR, DER, offline e sync | `amparius-platform` | gpt-5.6-luna |
| Engenheiro Rust | relay, cliente e sync offline | `amparius-rust-edge` | gpt-5.6-luna |
| QA de Contrato | aceite, regressão e performance | `amparius-qa-contract` | claude-sonnet-5 |
| Documentação/Userguide | ADR, DER, roadmap e ajuda | `amparius-documentation` | claude-sonnet-5 |
| Designer de Produto | UX, estados e ajuda contextual | `amparius-product-ux` | claude-sonnet-5 |
| Pesquisador NFS-e | fontes oficiais e versões técnicas | `amparius-nfse-research` + `nfse-documentacao-tecnica` | openrouter/free |

## Regra de substituição de modelo

O novo agente deve ler `AGENTS.md`, `docs/AI-AGENT-GOVERNANCE.md`, esta tabela
e a skill do papel. A capacidade do modelo não muda os gates: fiscal continua
fail-closed, banco continua com RLS e entrega continua exigindo evidência.

## Fila padrão

1. Líder lê o escopo e fecha a fatia.
2. Um especialista implementa ou documenta.
3. Líder confere diff e executa verificações locais.
4. QA valida critérios e classifica gaps.
5. Documentação atualiza contratos após o aceite.
6. Commit/PR só após a evidência; o próximo item começa depois do gate.
