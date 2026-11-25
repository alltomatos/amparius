# ADR 0005 — Escala por células em VPS

Status: aceito · 21/11/2026

## Contexto

O Amparius começa em VPS com Docker, sem Kubernetes. O crescimento esperado é de muitos clientes pequenos e médios; um único servidor não sustenta o crescimento nem limita o impacto de uma falha.

## Decisão

- A unidade de escala é a **célula**: uma VPS com a pilha completa (edge, api, worker, Postgres, Redis, RabbitMQ, object storage S3-compatible ou endpoint externo, backup).
- Cada célula atende um conjunto de tenants; **nenhum dado cruza células**.
- Um **control plane** pequeno mantém diretório de tenants, provisionamento, billing, autenticação global e status. Ele não guarda dado de negócio.
- Roteamento por hostname no edge, com mapeamento em cache vindo do control plane.
- Escala vertical (S → M → L) até o teto de 70% de utilização; acima disso, **adicionar célula**.
- Migração de tenant entre células é operação suportada, com janela curta e verificação de hash.
- Tenants muito grandes ou grupos econômicos podem receber **célula dedicada**.

## Consequências

- Blast radius de uma falha fica restrito aos tenants daquela célula.
- Provisionamento vira produto: Terraform + Ansible + registro automático.
- O control plane precisa ser altamente disponível, mas sua queda não para a operação.
- Não há consulta analítica cross-tenant; consolidações usam exportação, não join entre bancos.
- Custo cresce em degraus previsíveis, acompanhando a receita.

## Alternativas descartadas

- **Um banco único para todos**: simples no início, mas vira ponto único de falha e de latência; migração posterior é caríssima.
- **Kubernetes desde o início**: poder sem equipe dedicada vira fragilidade operacional.
- **Banco por tenant**: isolamento máximo, custo e operação inviáveis para milhares de clientes pequenos.
