# Roadmap — Amparius

Versão 0.1 · fases com critério de saída. Nada avança sem o critério da fase anterior.

## Fase 0 — Fundação (1–2 semanas)

- Repositório, AGENTS.md, PRD, arquitetura, DER, UML, ADRs.
- `docker compose` com Postgres, Redis e RabbitMQ; object storage S3-compatible configurável por ambiente.
- Esqueleto `cmd/api`, `cmd/worker`, migrações, health, observabilidade básica.
- Esqueleto do relay em Rust com health e banco local.

**Saída:** `make up` sobe o ambiente; API responde health; relay conecta e registra nó.

## Fase 1 — Espinha dorsal (4–6 semanas)

- `identity` + `party` + `catalog` com RLS e testes de isolamento.
- `entitlement`: catálogo de módulos, licença por módulo/cota e enforcement nas 3 camadas (ADR 0007).
- `finance`: plano de contas, títulos, parcelas, baixas, caixa e ledger.
- `fiscal`: configuração, motor de tributos com tabelas datadas, NFS-e (1 município), XML, XSD, DANFSe.
- Outbox + worker + idempotência.

**Saída:** emitir NFS-e de um serviço com cálculo reproduzível e XML validado, ponta a ponta, com teste golden.

## Fase 2 — Setor e relay (4–6 semanas)

- Pacote funerário: contrato, OS, velório, agenda, jazigo.
- Pacote floricultura: catálogo, pedido, entrega, lote.
- Relay: operação offline de contrato/caixa, fila, sync, conflitos.
- Contingência fiscal e reconciliação.

**Saída:** jornada J1 e J2 do PRD executáveis; queda de rede não impede contrato nem caixa; sync reconcilia.

## Fase 3 — Gestão e IA (4–6 semanas)

- Cemitério: mapa, concessão, sepultamento, exumação.
- Cremação: crematório, cremação, urna (módulo próprio — ADR 0007).
- Relatórios e fechamento; exportação para contador.
- Gateway MCP, agentes de leitura (fiscal, cobrança, atendimento), auditoria de ferramenta.
- Portal do cliente e API pública.

**Saída:** fechamento mensal exportável; copiloto responde com dados do tenant sem vazar PII.

## Fase 4 — Escala (contínuo)

- Segunda região / DR, partições e réplicas de leitura.
- Mais municípios e UFs; NF-e/NFC-e.
- App de campo; integrações bancárias e WhatsApp.
- Performance: SLOs, caos, capacidade.

## Prioridades em caso de corte

1. Fiscal correto e auditável.
2. Caixa e título funcionando offline.
3. Contrato/OS do funerário.
4. Resto.
