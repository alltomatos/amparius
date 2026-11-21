# ADR 0002 — Relay local e operação offline

Status: aceito · 21/11/2026 · refinado pelo ADR 0006

## Contexto

Funerária e cemitério não podem parar no velório porque a internet caiu. Emissão fiscal também precisa sobreviver a indisponibilidade de SEFAZ/prefeitura.

## Decisão

- O relay é um serviço **Rust** instalado no cliente, com **PostgreSQL local**, fila de saída e cache de autenticação.
- Operações críticas rodam localmente: contrato, OS, recebimento, caixa, agenda, sepultamento.
- Efeitos remotos entram em log de operações com `seq` por nó e `idempotency_key`.
- Sincronização é **ordenada por sequência**, não por relógio; servidor responde com cursor.
- Dados mestres são autoritativos no servidor; dados operacionais têm origem no nó.
- Conflito em dinheiro ou fiscal **não é resolvido automaticamente**: vira tarefa de revisão auditada.
- Fiscal em contingência é marcado como tal, com prazo e reconciliação posterior.

## Consequências

- Duas cópias do domínio (Go no SaaS, Rust no relay) exigem contrato explícito e testes de paridade.
- Instalação e atualização do relay viram produto (auto-update assinado, diagnóstico remoto).
- O que não funciona offline precisa ser dito na UI, não escondido.

## Critérios de aceite

1. Queda de rede durante contrato e caixa não perde dado nem duplica operação.
2. Sync de 1.000 operações conclui e reconcilia com cursor.
3. Conflito de valor gera tarefa e bloqueia sobrescrita.
