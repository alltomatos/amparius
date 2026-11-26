# Atendimento funerário — busca até ordem de serviço

Esta jornada é a sequência mínima do atendimento:

1. Busque o cliente em `GET /_dev/parties?q=termo` ou `?query=termo`.
2. Se não existir, cadastre-o em `POST /_dev/parties`. O cadastro não aceita
   `tenant_id`; o tenant vem do contexto autenticado/dev-only.
3. Consulte ou cadastre o falecido e os familiares pelos endpoints de party.
   O contrato só aceita referências já existentes.
4. Escolha o item do catálogo e abra o contrato. O contrato congela os itens e
   gera o plano de parcelas.
5. Ative o contrato. Só contratos `active` podem abrir OS.
6. Abra a OS:

   `POST /_dev/funeral/contracts/{contract_id}/service-orders`

   com `Idempotency-Key` e JSON estrito:

   ```json
   {
     "kind": "funeral_service",
     "description": "atendimento inicial",
     "scheduled_for": "2026-11-25T12:00:00-03:00"
   }
   ```

7. Atualize descrição ou horário com `PATCH /_dev/funeral/service-orders/{id}`
   ou avance para `in_progress` com
   `PATCH /_dev/funeral/service-orders/{id}/status`, sempre com uma nova
   `Idempotency-Key`. Conclua com
   `POST /_dev/funeral/service-orders/{id}/complete`.

A resposta inclui o estado atual e `status_history`. Repetir uma chave com o
mesmo conteúdo devolve o mesmo agregado; reutilizá-la com conteúdo diferente
é conflito. A descrição não é publicada na outbox. A rota é dev-only e não
substitui autenticação de produção.

## Agenda de velório e sepultamento

Depois de ativar o contrato, cadastre o local e, se necessário, um recurso:

```http
POST /_dev/funeral/locations
Idempotency-Key: location-001
Content-Type: application/json

{"name":"Sala principal","kind":"wake_room","time_zone":"America/Fortaleza"}
```

```http
POST /_dev/funeral/resources
Idempotency-Key: resource-001
Content-Type: application/json

{"location_id":"<location_id>","name":"Sala principal","kind":"room"}
```

Agende o velório ou sepultamento com um intervalo explícito. `starts_at` e
`ends_at` aceitam RFC3339 e são persistidos em UTC; `time_zone` deve ser um
nome IANA. A janela é semiaberta: um evento que começa exatamente no fim de
outro não conflita.

```http
POST /_dev/funeral/schedules
Idempotency-Key: schedule-001
Content-Type: application/json

{"contract_id":"<contract_id>","service_order_id":"<service_order_id>","kind":"wake","location_id":"<location_id>","resource_id":"<resource_id>","starts_at":"2026-11-25T12:00:00-03:00","ends_at":"2026-11-25T14:00:00-03:00","time_zone":"America/Fortaleza"}
```

Use `GET /_dev/funeral/contracts/{contract_id}/schedules` para listar a agenda,
`PATCH /_dev/funeral/schedules/{id}` para alterar intervalo/local/recurso e
`POST /_dev/funeral/schedules/{id}/confirm` ou
`POST /_dev/funeral/schedules/{id}/cancel` para confirmar ou cancelar. Toda
escrita exige uma nova `Idempotency-Key`; o corpo dessas duas transições é
opcional e, quando enviado, aceita somente `{"reason":"..."}`. Uma agenda
confirmada continua reservando local e recurso. Sobreposição do mesmo local ou
recurso responde `409 SCHEDULE_CONFLICT`. A agenda mantém histórico
append-only e eventos sem PII. Mapa de cemitério, jazigo/concessão,
recorrência e regras de horário comercial não fazem parte desta fatia.

## Jornada atômica

Quando o contrato já está `active` e o local/recurso já existe, a OS e a
agenda podem ser abertas juntas:

```http
POST /_dev/funeral/contracts/{contract_id}/journey
Idempotency-Key: journey-001
Content-Type: application/json

{"service_order":{"kind":"funeral_service","description":"atendimento inicial","scheduled_for":"2026-11-25T12:00:00-03:00"},"schedule":{"kind":"wake","location_id":"<location_id>","resource_id":"<resource_id>","starts_at":"2026-11-25T12:00:00-03:00","ends_at":"2026-11-25T14:00:00-03:00","time_zone":"America/Fortaleza"}}
```

Contrato, OS, agenda, históricos e eventos são confirmados juntos. Repetir a
mesma chave e corpo devolve os mesmos IDs; usar a chave com outro conteúdo
responde `409 IDEMPOTENCY_CONFLICT`. Conflito de janela responde
`409 SCHEDULE_CONFLICT` e não deixa uma OS parcial. A resposta não expõe dados
do cliente e os eventos v1 carregam somente IDs opacos, estados e datas.

## Cliente desktop offline (CL-02)

Na tela nativa `Atendimento funeral`, informe apenas referências opacas da
parte, contrato, local, recurso e ordem. Nome, CPF, telefone, endereço e
descrição livre não são campos do formulário nem aparecem em logs.

O cliente oferece três ações: criar jornada atômica com OS e agenda, avançar a
ordem para `in_progress` e concluir a ordem em `completed`. Cada ação recebe
uma `Idempotency-Key` estável por referência e é persistida na fila com `seq` do
dispositivo.

Em `offline` e `contingency`, o estado visível é `queued` e o envio fica
aguardando sincronização. Em `online`, relay e identidade do operador são
obrigatórios; indisponibilidade não é apresentada como sucesso. O operador só
pode criar a jornada após confirmar que o contrato está `active`; o servidor
revalida tenant, RLS, entitlement, contrato e transição. Sem
`AMPARIUS_FUNERAL_ENTITLEMENT=active`, o módulo permanece bloqueado por
fail-closed.
