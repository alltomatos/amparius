# ADR 0020 — Confirmação, conflito e cancelamento da agenda

- **Status:** aceito para a issue FUN-02B (#38)
- **Data:** 25/11/2026
- **Escopo:** módulo `funeral`, migration `0030_funeral_schedule_confirmation.sql`

## Decisão

1. A agenda usa os estados `scheduled`, `confirmed`, `cancelled` e
   `completed`. As transições públicas desta fatia são `scheduled -> confirmed`
   e `scheduled|confirmed -> cancelled`; estados cancelados ou concluídos são
   terminais.
2. `confirmed` continua ocupando local e recurso. As constraints PostgreSQL
   `EXCLUDE USING gist` cobrem `scheduled` e `confirmed`, portanto duas
   transações concorrentes têm um único vencedor e o perdedor recebe
   `SCHEDULE_CONFLICT`/`ErrScheduleConflict`.
3. Confirmação e cancelamento usam `schedule_mutation` com
   `Idempotency-Key`. A chave é vinculada ao agregado, operação e conteúdo;
   replay idêntico devolve o mesmo agregado e conteúdo diferente é conflito.
   A segunda consulta de idempotência ocorre depois do lock do agregado para
   fechar a corrida entre dois requests iguais.
4. Cada transição grava `schedule_status_history` append-only na mesma
   transação da mudança e da outbox. O motivo opcional fica somente no
   histórico; eventos não carregam texto livre ou PII.
5. Eventos v1 são `funeral.schedule.confirmed` e
   `funeral.schedule.cancelled`, com referências opacas, estados e versão 1.

## Contrato HTTP dev-only

- `POST /_dev/funeral/schedules/{id}/confirm`
- `POST /_dev/funeral/schedules/{id}/cancel`

Ambas exigem `Idempotency-Key`. O corpo é opcional e, quando presente, aceita
somente `{"reason":"..."}`. Resposta `200` traz o agregado; transição
inválida responde `400 INVALID_SCHEDULE`, idempotência divergente responde
`409 IDEMPOTENCY_CONFLICT` e conflito de reserva responde `409
SCHEDULE_CONFLICT`. JSON é estrito, sem tenant/company no corpo e sem PII em
erros.

## Limites

Autenticação de produção, autorização por papel, recorrência, disponibilidade
automática, feriados, mapa de cemitério e consumidores de negócio continuam
fora desta fatia. O histórico registra motivo textual fornecido pelo operador,
mas não identifica o principal autenticado; isso depende do contrato de
autenticação de produção.
