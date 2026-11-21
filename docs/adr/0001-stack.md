# ADR 0001 — Stack e forma da solução

Status: aceito · 21/11/2026

## Contexto

Precisamos de um ERP multi-tenant com operação local tolerante a queda de internet, cálculo fiscal auditável e espaço para IA. A equipe é pequena; complexidade operacional precoce mata o produto.

## Decisão

- **Go** no SaaS (`api` e `worker`): HTTP, casos de uso, jobs, integrações fiscais.
- **Rust** no relay de borda e no cliente desktop: binário único, previsível, sem runtime pesado.
- **PostgreSQL** como fonte de verdade, **Redis** para cache/lock, **RabbitMQ** para eventos e jobs.
- **Monólito modular**: um repositório, um deploy de API e um de worker; módulos com schema e contrato próprios.
- **Sem Kubernetes** no início: containers gerenciados + Postgres gerenciado.

## Consequências

- Deploy simples e transações locais entre módulos do mesmo processo.
- Extrair serviço exige ADR com evidência de gargalo ou isolamento regulatório.
- Rust no relay evita arrastar runtime JVM/Node para a máquina do cliente.
- O time precisa dominar duas linguagens; contratos e testes de integração passam a ser obrigatórios.

## Alternativas descartadas

- Microsserviços desde o início: custo operacional sem evidência.
- Node/TypeScript no núcleo: o produto anterior já mostrou custo de performance e complexidade.
- Relay em Go: viável, mas Rust dá binário menor e menor consumo no computador do cliente.
