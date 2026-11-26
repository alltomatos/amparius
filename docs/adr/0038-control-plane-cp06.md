# ADR 0038 — Contrato administrativo de entitlement CP-06

Status: aceito para implementação sequencial · 26/11/2026

## Contexto

O control plane já possuía o catálogo, a cota por módulo, a licença por
empresa/CNPJ e a auditoria de ativação. A fatia CP-06 precisa expor detalhe,
histórico e operações administrativas sem aceitar tenant por cabeçalho, perder
alterações concorrentes ou repetir efeitos em retries HTTP.

## Decisões

1. `license_assignment.version` e `license_pool.version` são contadores
   monotônicos, incrementados na alteração correspondente. O `PATCH` exige a
   versão observada e falha com `STALE_VERSION` quando ela não é a atual.
2. `entitlement.mutation_idempotency` persiste a chave, operação, hash do
   pedido e resultado dentro da mesma transação da mudança. A mesma chave com
   pedido diferente falha com `IDEMPOTENCY_CONFLICT`; replay devolve o resultado
   persistido sem novo evento.
3. Alterações de quota são auditadas em `entitlement.quota_event`, append-only,
   com ator, quota anterior, quota nova e instante. O tenant é aplicado por
   `db.WithTenantTx` e RLS; não existe leitura cross-tenant.
4. O backend publica:
   - `GET /v1/companies/{companyId}/entitlements/{module}`;
   - `GET /v1/companies/{companyId}/entitlements/{module}/audit`;
   - `GET /v1/entitlements/{entitlementId}/audit`;
   - `PUT` no detalhe para conceder/reativar;
   - `PATCH` no detalhe para revogar, ajustar validade e/ou quota.
   Escritas exigem `Idempotency-Key` e `entitlement:write`; leituras exigem
   `entitlement:read`.
5. CNPJ continua mascarado no contrato de empresas. O contrato CP-06 não
   devolve documento integral, PII ou payload livre de auditoria.

## Consequências e limites

- A migration é forward-only; a seção `Down` existe somente para ciclos locais
  de teste Goose.
- O control plane continua sem tabela de empresa: a pertinência do CNPJ é
  confirmada pelo serviço público de `identity` antes de tocar o entitlement.
- A autenticação OIDC/MFA real continua sendo responsabilidade do bootstrap
  descrito no ADR 0024; o RBAC desta fatia usa o principal já autenticado.
