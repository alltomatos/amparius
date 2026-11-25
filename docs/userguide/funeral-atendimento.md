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
