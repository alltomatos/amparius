# ADR 0024 — Autenticação OIDC, MFA, RBAC e escopo seguro

Status: aceito e implementado em 25/11/2026 — HARDEN-01A (#54)

## Contexto

As primeiras superfícies HTTP eram deliberadamente `/_dev/*` e recebiam o
tenant por cabeçalho de desenvolvimento. Isso permitia testar RLS, mas não era
uma fronteira de autenticação de produção. O onboarding cria `identity.app_user`
como `pending_auth` e já possui `auth_subject`, `mfa_enabled`, papéis e RLS; o
que faltava era ligar essa identidade a um token OIDC validado, a um segundo
fator e à empresa/CNPJ efetivamente permitido.

## Decisões

1. A API recebe somente `Authorization: Bearer <token>`. O contrato
   `auth.OIDCVerifier` é injetado pelo bootstrap e deve validar assinatura,
   issuer, audience, expiração e nonce conforme o provedor escolhido. Esta
   implementação não inventa nem embute Keycloak, Auth0 ou outro provedor.
2. O subject OIDC é resolvido em `identity.app_user` por
   `identity.Repository.ResolveSubject`, sob RLS e com `status = 'active'`.
   Papéis efetivos vêm de `identity.user_role`/`identity.role`; roles declaradas
   no token não concedem privilégio.
3. A tabela `identity.user_company`, criada pela migration 0032, vincula o
   usuário a cada CNPJ permitido. O `company_id` do token só é aceito quando há
   esse vínculo e `identity.company` confirma o mesmo tenant.
4. MFA é obrigatório em duas camadas: o usuário precisa estar com
   `mfa_enabled = true` e o adaptador injetado `auth.MFAVerifier` precisa
   confirmar a elevação (TOTP, WebAuthn ou `amr/acr` do IdP). Sem ambos, nega.
5. `auth.RolePolicy` é uma allowlist copiada na construção, com papéis
   `owner`, `admin`, `manager`, `operator` e `auditor`. Permissões e papéis
   desconhecidos falham fechado.
6. `auth.Require` compõe `Principal`, `db.WithTenant` e `db.WithCompany` somente
   depois de todas as validações. Contexto pré-existente divergente é recusado;
   tenant/company vindos de JSON ou cabeçalho não são considerados.
7. `http.AuthenticatedRoute` é o ponto de montagem das rotas de produção. Uma
   rota registrada sem `Authenticator` responde `503`; credencial ausente ou
   inválida responde `401`; MFA/escopo/permissão insuficiente respondem `403`.
   As rotas `/_dev/*` continuam opt-in e não são autenticação de produção.

## Observabilidade e LGPD

O middleware registra apenas `auth_decision`, método, permissão, resultado e
request id quando disponível. Token bruto, subject, email, nome, erro do IdP e
corpo da requisição não entram no log. A resposta de erro é genérica para não
revelar se um subject ou empresa existe.

## Consequências e pendências

- O pacote e seus testes são independentes de provedor e permitem integrar o
  IdP escolhido por `OIDCVerifierFunc`/`MFAVerifierFunc` ou adaptadores reais.
- Continua pendente escolher/configurar o provedor OIDC/MFA externo, publicar o
  bootstrap que injeta esses adaptadores e migrar cada handler de negócio de
  `/_dev/*` para `http.AuthenticatedRoute`. Até lá, nenhuma rota dev-only deve
  ser publicada como produção.
- A migration 0032 tem `Down` apenas para o ciclo local Goose; produção segue a
  regra forward-only e qualquer correção futura deve ser uma nova migration.

## Verificação

```text
go test ./internal/platform/auth ./internal/platform/http ./internal/modules/identity
go test -p 1 ./... -count=1
```

Os testes cobrem acesso positivo, bearer ausente/malformado, MFA ausente,
empresa de outro tenant, conflito de contexto, RBAC, dependência ausente e
ausência de token/subject nos logs.
