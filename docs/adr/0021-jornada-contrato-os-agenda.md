# ADR 0021 — Jornada atômica contrato, OS e agenda

- **Status:** aceito para a issue FUN-02C (#40)
- **Data:** 25/11/2026
- **Escopo:** módulo `funeral`, migration `0031_funeral_journey.sql`

## Contexto

As fatias FUN-01C, FUN-02A e FUN-02B já permitiam criar contrato ativo, ordem
de serviço e agenda separadamente. A jornada de atendimento precisa executar a
abertura da OS e a reserva da agenda como uma decisão única, sem deixar uma OS
órfã quando a reserva falha.

## Decisão

1. `POST /_dev/funeral/contracts/{id}/journey` exige contrato `active` e cria
   `service_order`, `schedule`, históricos iniciais e `journey_mutation` na
   mesma transação PostgreSQL.
2. A chave de idempotência da jornada tem `request_hash`; o replay devolve os
   mesmos IDs opacos da OS e da agenda. Chave reutilizada com conteúdo diferente
   falha com `IDEMPOTENCY_CONFLICT`.
3. A agenda referencia a OS pelo UUID e o banco valida que OS, contrato, local,
   recurso, tenant e empresa são coerentes. Falhas são convertidas para códigos
   HTTP estáveis, sem SQL, PII ou texto livre de domínio.
4. A transação publica `funeral.service_order.created`,
   `funeral.schedule.created` e `funeral.journey.created`, todos v1 e sem PII.
   O evento de contrato existente continua sendo a preparação da emissão fiscal;
   esta jornada não inventa regra fiscal nem transmite documento.
5. A rota permanece dev-only, com JSON estrito, tenant/empresa somente no
   contexto confiável, `no-store` e entitlement do módulo antes do domínio.

## Limites

- Não há consumidor de negócio para os eventos nem emissão fiscal síncrona.
- A autenticação de produção, relay offline e reconciliação ficam fora desta
  fatia.
- Dados de cliente, falecido, familiar, nomes e descrição não entram nos
  eventos; são consultados somente nas tabelas do módulo apropriado.
