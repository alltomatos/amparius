# Contrato HTTP de autenticação e autorização v1

Status: vigente para HARDEN-01A (#54), 25/11/2026.

## Entrada

Rotas protegidas usam `Authorization: Bearer <token>`. O token é validado pelo
adaptador OIDC injetado no processo; a API não aceita tenant, company, role ou
permission do corpo, query string ou cabeçalho de escopo.

O adaptador devolve apenas `issuer`, `subject`, `tenant_id` e `company_id` como
escopo candidato. O vínculo efetivo é resolvido no banco:

1. `identity.app_user.auth_subject` precisa existir e estar `active`;
2. `identity.app_user.mfa_enabled` precisa ser `true`;
3. `identity.user_company` precisa conter o par usuário/CNPJ;
4. `identity.company` precisa confirmar o CNPJ no mesmo tenant, sob RLS;
5. os papéis são lidos de `identity.user_role` e avaliados pela allowlist RBAC.

Depois disso, o caso de uso recebe `db.TenantFromContext`,
`db.CompanyFromContext` e `auth.PrincipalFromContext`.

## Respostas de bloqueio

| Situação | HTTP | Código |
|---|---:|---|
| bearer ausente, malformado ou identidade inválida | 401 | `AUTHENTICATION_REQUIRED` |
| MFA não elevado ou escopo não permitido | 403 | `MFA_REQUIRED` / `SCOPE_FORBIDDEN` |
| papel sem a permissão requerida | 403 | `PERMISSION_DENIED` |
| OIDC/MFA/RLS indisponível ou configuração incompleta | 503 | `AUTHENTICATION_UNAVAILABLE` |

Todos os envelopes são genéricos e não incluem PII, token ou detalhes do
provedor. Respostas carregam `Cache-Control: no-store`.

## Montagem

O servidor registra uma `http.AuthenticatedRoute` com padrão net/http, handler
e `auth.Permission`. O middleware `auth.Require` compõe o contexto confiável e
nega por padrão quando o autenticador ou qualquer dependência obrigatória não
existe.

As rotas `/_dev/*` continuam exclusivas de desenvolvimento e não satisfazem
este contrato.
