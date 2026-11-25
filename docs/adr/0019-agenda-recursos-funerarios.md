# ADR 0019 — Agenda funerária, locais e recursos

- **Status:** aceito para a issue FUN-02A (#36)
- **Data:** 25/11/2026
- **Escopo:** módulo `funeral`, migration `0029_funeral_schedule.sql`

## Contexto

Velórios e sepultamentos precisam reservar um local e, opcionalmente, um
recurso operacional ligado ao contrato e à ordem de serviço. A reserva deve
ser consistente em múltiplas empresas do mesmo tenant, sobreviver a replay e
não expor dados de party na outbox.

## Decisão

1. `funeral.location` é o local operacional da empresa; `funeral.resource` é
   um recurso ativo pertencente a um local. Ambos usam UUIDv7, tenant/company,
   RLS e chave de idempotência.
2. `funeral.schedule` representa somente `wake` (velório) ou `burial`
   (sepultamento). Possui referências opacas obrigatórias a contrato e local,
   e referência opcional à OS e ao recurso.
3. O intervalo é semiaberto `[starts_at, ends_at)`, armazenado como
   `timestamptz` UTC, com `time_zone` IANA explícito preservando o fuso da
   operação. Intervalo vazio ou invertido falha.
4. Conflito de local e de recurso é decidido por constraints PostgreSQL
   `EXCLUDE USING gist` sob `status = scheduled`. Isso serializa a decisão
   também quando duas transações tentam reservar a mesma janela ao mesmo
   tempo; o serviço converte SQLSTATE `23P01` em conflito determinístico.
5. Alterações e cancelamento usam `schedule_mutation`; o histórico de estado é
   append-only. A agenda não é apagada. Criação, histórico e evento de outbox
   são uma única transação.
6. Eventos v1 publicam somente UUIDs, tipo, status, intervalo e timezone. Não
   há nome, descrição, documento ou contato no payload.

## HTTP dev-only

As rotas ficam atrás do opt-in existente e do entitlement `funeral`:

- `POST/GET /_dev/funeral/locations`;
- `POST/GET /_dev/funeral/resources?location_id=...`;
- `POST/GET /_dev/funeral/schedules`;
- `GET /_dev/funeral/contracts/{id}/schedules`;
- `GET/PATCH /_dev/funeral/schedules/{id}`;
- `POST /_dev/funeral/schedules/{id}/cancel`.

JSON é estrito, `Idempotency-Key` é obrigatório nas escritas, respostas têm
`no-store` e erros são códigos estáveis sem PII.

## Limites

Esta fatia não implementa mapa de cemitério, concessão/jazigo, integração com
o módulo `cemetery`, recorrência, participantes, feriados, disponibilidade
automática ou autenticação de produção. O timezone é validado como nome IANA,
mas a política de horário comercial e DST pertence à borda de apresentação;
o banco decide apenas o instante UTC e o intervalo informado.
