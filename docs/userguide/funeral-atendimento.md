# Atendimento funerário — busca até ordem de serviço

Esta jornada é a sequência mínima do atendimento:

1. Busque o cliente em `GET /_dev/parties?q=termo` ou `?query=termo`.
2. Se não existir, cadastre-o em `POST /_dev/parties`. O cadastro não aceita
   `tenant_id`; o tenant vem do contexto autenticado/dev-only. Na API de
   produção, pessoa física tem o contrato `POST` e `GET /v1/parties` (#72),
   descrito em [autenticação e acesso](authentication.md#cadastro-de-pessoa-física-pela-api-autenticada-v1parties).
   Ele ainda responde `503` até o provedor OIDC/MFA ser conectado; em ambiente
   privado, teste só com dados sintéticos.
3. Consulte ou cadastre o falecido e vincule o familiar. Veja [Cadastro pela API](#cadastro-de-falecido-e-vínculo-familiar-pela-api).
4. Consulte a [oferta de serviço](#consulta-da-oferta-de-serviço). A abertura
   do contrato autenticado com essa oferta segue na [#76](https://github.com/alltomatos/amparius/issues/76).
   No fluxo dev, o contrato congela os itens e
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

## Cadastro de falecido e vínculo familiar pela API

1. Em `POST /v1/deceased`, informe o ID da pessoa física já cadastrada e
   a data e hora do óbito. Certidão e local de sepultamento são opcionais.
2. Em `POST /v1/deceased/{deceasedId}/family-links`, use o ID do
   falecido retornado no passo anterior. Informe o ID de outra pessoa
   física, o código ativo de parentesco e `consentEvidenceRef`, o UUID
   da evidência externa de consentimento. Autovínculo retorna 400.
3. Consulte pelo ID em `GET /v1/deceased/{id}` ou
   `GET /v1/family-links/{id}`. Os registros legados mantêm seus IDs.
   Novas tentativas de criação para um registro duplicado retornam 409.

As duas criações exigem permissão de escrita e política de privacidade
do tenant. As consultas exigem permissão de leitura. No binário atual, as
quatro rotas retornam `503 AUTHENTICATION_UNAVAILABLE`. Com autenticador
configurado e sem política disponível, os dois POSTs retornam
`503 PRIVACY_POLICY_UNAVAILABLE`. O fluxo do mockup Loja 02 é simulado.
Veja o [contrato HTTP](../contracts/party-deceased-family-link-v1.md) e
[auditoria e privacidade](auditoria-lgpd.md#falecido-e-vínculo-familiar).

## Consulta da oferta de serviço

Em `GET /v1/catalog/offers`, informe os IDs `item_id` e `list_id` e a
data civil `on` no formato `AAAA-MM-DD`. A tabela precisa ter iniciado.
O preço vale do início ao fim, incluindo as duas datas. Sem data final,
o preço tem prazo aberto. O valor vem em centavos. Em integrações, use
um parser JSON que preserve inteiros de 64 bits.

Operadores e auditores podem consultar. Owner, admin e manager também
podem cadastrar serviço, tabela e preço. A sessão exige empresa válida
e MFA. Se receber `404 OFFER_NOT_FOUND`, confira IDs, escopo, atividade
do serviço e vigência. Se receber `409 OFFER_AMBIGUOUS`, peça ao responsável
pelo catálogo que revise os preços vigentes na data. Sem OIDC/MFA no
servidor, a API retorna `503 AUTHENTICATION_UNAVAILABLE`.

A Loja 02 simula a seleção. Descoberta e seleção no cliente operacional
seguem na [#75](https://github.com/alltomatos/amparius/issues/75).
Veja o [contrato HTTP](../contracts/catalog-operational-v1.md).

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

## Cliente desktop offline (CL-02/CL-03)

Na tela nativa `Atendimento funeral`, informe apenas referências opacas da
parte, contrato, local, recurso e ordem. Nome, CPF, telefone, endereço e
descrição livre não são campos do formulário nem aparecem em logs.

O cliente oferece cinco ações na mesma superfície: criar jornada atômica com OS e
agenda, listar as OS conhecidas pela fila local, avançar para `in_progress`,
pausar com estado operacional local `paused` e concluir em `completed`. Cada
mutação de execução recebe uma `Idempotency-Key` própria por ordem e versão-base,
leva `expected_version` e é persistida na fila com `seq` do dispositivo. A tela
mostra a versão, `queued`, `done`, `error` ou `conflict`; uma OS com mutação
pendente, falha terminal ou conflito não aceita outra mutação local até a revisão.

Em `offline` e `contingency`, o estado visível é `queued` e o envio fica
aguardando sincronização. Em `online`, relay e identidade do operador são
obrigatórios; indisponibilidade não é apresentada como sucesso. O operador só
pode criar a jornada após confirmar que o contrato está `active`; o servidor
revalida tenant, RLS, entitlement, contrato e transição. Sem
`AMPARIUS_FUNERAL_ENTITLEMENT=active`, o módulo permanece bloqueado por
fail-closed. A projeção de OS é local à estação e não substitui a listagem
autoritativa do serviço funeral; uma OS só aparece nela depois que uma mutação
referente àquela OS foi conhecida pela fila. O servidor continua revalidando
tenant, RLS, entitlement, contrato, `expected_version` e transição.
