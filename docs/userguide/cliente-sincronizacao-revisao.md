# Sincronização e revisão no cliente (CL-05)

Esta tela ajuda o operador a acompanhar o envio da fila local ao relay e a
encaminhar conflitos para revisão. Ela não é uma tela de edição, não resolve
conflitos e não substitui a autoridade do relay ou do SaaS.

## Abrir a tela

No **Painel do dia**, selecione **Sincronização e revisão**. A tela usa a fila
persistida da estação e o resultado da última execução de sincronização.

O cliente mostra somente sequência, tipo, referência opaca, status, tentativas,
código técnico e contagens. Não mostra tenant, dispositivo, payload, XML,
token, nome, documento, contato ou resposta crua do relay.

## Estados

| Estado | O que significa |
| --- | --- |
| Sem execução | ainda não houve envio pelo transporte |
| Pendências | há operações aguardando envio, em ordem de sequência |
| Sincronizando | o cliente está enviando; a ação fica desabilitada |
| Concluído | o resultado mostra sincronizadas, duplicadas, falhas e conflitos |
| Interrompido | uma falha transitória parou o envio; pendências permanecem na fila |
| Revisão aberta | uma tarefa é apresentada em modo somente leitura |

As contagens de **duplicadas** representam replay idempotente aceito pelo relay,
não uma nova operação criada pelo cliente.

## Tentar novamente com segurança

Use **Tentar novamente** quando restarem operações `pending` após timeout,
indisponibilidade ou outra interrupção de transporte. O cliente reapresenta a
mesma operação, com o mesmo `operation_id`, `seq`, `idempotency_key` e hash.

Isso não cria nova sequência nem duplica contrato, caixa ou emissão. Falha
terminal, conflito e rejeição fiscal não são reabertos por este botão; corrija a
causa e encaminhe a tarefa ao fluxo autorizado de revisão.

## Abrir uma revisão

Selecione **Abrir revisão** na operação ou na tarefa aberta. O detalhe mostra:

- tipo da revisão;
- operação opaca;
- código técnico sanitizado;
- orientação de revisão e preservação do estado local.

O detalhe é somente leitura. Não há botão para escolher vencedor, fechar tarefa,
alterar valor ou aplicar `last-write-wins`. Em dinheiro e fiscal, o conflito
permanece bloqueado até uma decisão autorizada e auditada.

## O que fazer em cada situação

1. **Pendência:** mantenha o cliente aberto ou tente sincronizar quando o relay
   estiver disponível.
2. **Interrupção de transporte:** use **Tentar novamente**; a fila mantém a
   operação original se a rede continuar indisponível.
3. **Duplicada:** nenhuma ação de correção é necessária; é confirmação de
   idempotência.
4. **Falha ou conflito:** abra a revisão e acione o suporte conforme o processo
   da empresa. Não recrie a operação com outra chave.
5. **Conflito financeiro/fiscal:** não edite arquivo local e não tente resolver
   pelo valor mais recente.

## Limites desta versão

- A resolução efetiva da tarefa, fechamento, aplicação de snapshot e decisão de
  vencedor ainda exigem fluxo autorizado posterior.
- O cliente não exibe nem altera o contexto de tenant; o relay valida tenant e
  aplica RLS no seu próprio contexto.
- A tela não transmite XML fiscal, não renova credencial e não substitui TLS ou
  o sincronizador relay→SaaS.

## Referências

- Contrato: [client-sync-review-v1.md](../contracts/client-sync-review-v1.md)
- Decisão: [ADR 0037](../adr/0037-cliente-sync-review.md)
- Aceite: [frontend-acceptance.md](../qa/frontend-acceptance.md)
