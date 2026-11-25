# ADR 0016 — Contrato funerário e referências do agregado

Status: aceito para a primeira fatia do módulo funerário  
Data: 2026-11-25

## Contexto

O atendimento precisa registrar um contrato com cliente, falecido, familiares
já cadastrados e itens do catálogo sem criar parties implicitamente. O contrato
é tenant-scoped, pode operar no relay e precisa deixar uma trilha de status que
não seja apagada.

## Decisão

1. `funeral.contract` referencia `identity.company`, `party.party`,
   `party.deceased` e `catalog.catalog_item` por chaves compostas com
   `tenant_id`; a empresa também vem do contexto confiável.
2. Familiares são referências a `party.family_link` existentes, sempre ligados
   ao mesmo falecido. Nenhuma operação cria party, deceased, family_link ou item
   de catálogo.
3. Cada item congela descrição, unidade, quantidade e preço em centavos no
   momento do contrato. O banco usa `numeric(18,2)` e a API usa inteiros
   `*_amount_cents`.
4. A criação e as mudanças de status publicam eventos na outbox na mesma
   transação. `Idempotency-Key` da criação é persistida com hash do payload;
   replay idêntico devolve o mesmo contrato e replay divergente retorna
   conflito.
5. Status válidos são `draft`, `active`, `completed` e `cancelled`. Estados
   terminais não reabrem. Cada criação gera a entrada inicial do histórico e
   cada transição gera uma entrada append-only.
6. A primeira superfície HTTP é dev-only, com `no-store`, JSON estrito e
   escopo tenant+empresa. A autorização do módulo funeral continua passando
   pelo entitlement existente.

## Consequências

- As migrations `0023_funeral_contracts.sql` e
  `0024_funeral_contract_immutability.sql` são forward-only e habilitam RLS
  nas quatro tabelas do módulo, com UPDATE limitado a status e timestamp.
- O contrato pode ser consultado sem expor nomes pessoais ou mensagens de erro
  com PII; o cliente resolve as referências pelos seus próprios contratos de
  cadastro.
- Pagamentos, parcelamento, ordem de serviço e agenda permanecem fatias
  posteriores; esta fatia apenas congela o valor dos itens e publica o evento
  do contrato.
