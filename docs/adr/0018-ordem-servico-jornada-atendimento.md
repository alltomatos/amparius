# ADR 0018 — Ordem de serviço e jornada de atendimento funerário

Status: aceito para FUN-01C (#34)
Data: 2026-11-25

## Contexto

As fatias FUN-01A e FUN-01B já registram cliente/falecido, contrato, parcelas
e solicitações para os módulos financeiro e fiscal. Faltava a unidade
operacional que o atendente abre, atualiza e conclui durante o atendimento.

A jornada deve continuar referenciando cadastros existentes. Abrir uma OS não
pode criar party, pessoa, falecido, familiar, contrato ou item de catálogo por
efeito colateral.

## Decisão

1. O schema `funeral` passa a guardar `service_order`, ligada por chave
   composta `(tenant_id, contract_id)` ao contrato e por `(tenant_id,
   company_id)` à empresa do contexto. O agregado aceita mais de uma OS por
   contrato.
2. A OS congela somente o tipo operacional (`kind`) e a descrição informada
   pelo atendente; não recalcula preço nem regra fiscal. `scheduled_for` e
   `completed_at` são `timestamptz` UTC.
3. Os estados são `open`, `in_progress`, `completed` e `cancelled`. A fatia
   abre em `open`, pode ser atualizada enquanto não terminal, aceita transição
   explícita de status e é concluída por uma operação própria. Toda mudança de estado gera uma linha append-only em
   `service_order_status_history`.
4. A criação exige contrato `active`; empresa e tenant vêm do contexto
   confiável, nunca do corpo. A criação usa `Idempotency-Key` persistida. Patch
   e conclusão persistem comandos em `service_order_mutation`, com hash do
   conteúdo, para replay idêntico sem duplicar efeito/histórico/evento.
5. A API dev-only expõe a jornada incremental: busca/cadastro de party,
   falecido e familiar, catálogo, contrato/parcelas e então
   `POST /_dev/funeral/contracts/{contract_id}/service-orders`. A busca de
   party aceita `q` ou `query`; nenhum `tenant_id` vindo do cliente é aceito.
6. A OS publica eventos v1 na mesma transação do agregado:
   `funeral.service_order.created`, `funeral.service_order.updated`,
   `funeral.service_order.status_changed` e `funeral.service_order.completed`.
   Os payloads têm somente IDs opacos,
   estados e datas; descrição e nomes ficam fora da outbox.

## Consequências

- `migrations/0028_funeral_service_orders.sql` é forward-only, habilita RLS
  nas três tabelas novas e concede ao `amparius_app` somente os privilégios
  necessários.
- O histórico e os comandos de idempotência não podem ser alterados ou
  apagados pelo papel da aplicação.
- A rota retorna JSON estrito, `no-store` e mensagens de erro genéricas sem
  PII. Ela continua explicitamente dev-only até existir autenticação de
  produção.
- A OS é operacional e não substitui baixa financeira, emissão fiscal,
  agenda, velório ou sincronização do relay. Consumidores de negócio da
  outbox continuam fora desta fatia.

## Aceite e limites

- Testes unitários/HTTP cobrem abertura, update, conclusão, JSON estrito e
  ausência de PII em erros.
- Teste PostgreSQL real usa papel `NOSUPERUSER NOBYPASSRLS`, verifica replay,
  histórico, isolamento por empresa e cleanup administrativo.
- Performance de leitura da OS usa índices iniciados por `tenant_id`; não há
  benchmark de carga de produção nesta fatia.
