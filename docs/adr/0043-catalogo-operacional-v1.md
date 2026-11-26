# ADR 0043 — Catálogo operacional e resolução de oferta

A [#74](https://github.com/alltomatos/amparius/issues/74) implementa a
resolução de oferta no catálogo SaaS. Veja o [contrato HTTP](../contracts/catalog-operational-v1.md).

## Decisão

O catálogo expõe `ResolveServiceOffer(ctx, itemID, listID, on)` aos módulos
consumidores. A consulta obtém item, tabela e preço no schema do catálogo,
sob RLS no tenant da sessão. A API exige empresa válida, OIDC/MFA e RBAC.
Operadores e auditores têm leitura. Owner, admin e manager também têm escrita.
Sem autenticador, a API responde `503 AUTHENTICATION_UNAVAILABLE`.

A oferta exige IDs explícitos e data civil. A data deve alcançar o início
da tabela e estar no intervalo inclusivo do preço. A oferta de serviço
inativo ou fora do tenant recebe 404. Múltiplos preços elegíveis na mesma
data recebem 409, inclusive em dados legados. O cliente decodifica o valor
com precisão integral de 64 bits em centavos.

## Consequências

A decisão reusa as tabelas e os grants existentes. Descoberta e seleção no
cliente operacional seguem na [#75](https://github.com/alltomatos/amparius/issues/75).
Na [#76](https://github.com/alltomatos/amparius/issues/76), o contrato autenticado
deve determinar a data comercial no servidor, resolver a oferta e persistir
o ID do preço e o valor em centavos no snapshot para reutilização no replay.
