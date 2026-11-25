# Autenticação e acesso

O Amparius usa OIDC para identificar o operador e exige MFA para acessar uma
rota protegida. O provedor OIDC/MFA ainda é uma dependência de implantação: o
SaaS fornece o contrato `OIDCVerifier`/`MFAVerifier`, mas não escolhe nem
embute um fornecedor externo.

Para um usuário ser aceito, o provisionamento precisa deixar a conta
`identity.app_user` ativa, associar o `auth_subject` emitido pelo IdP, habilitar
MFA e criar um vínculo em `identity.user_company`. Os papéis são atribuídos em
`identity.user_role`; declarar um role no token não concede acesso.

O cliente envia somente o bearer token. Tenant e empresa são derivados da
identidade validada e conferidos contra o banco; não devem ser enviados no
JSON. Rotas sem autenticação retornam `401`, escopo ou permissão insuficiente
retornam `403`, e uma dependência de identidade indisponível retorna `503`.

A primeira implementação pode ser verificada com:

```text
go test ./internal/platform/auth ./internal/platform/http ./internal/modules/identity
```

As rotas `/_dev/*` permanecem disponíveis apenas com `APP_ENV=development` e
`ENABLE_DEV_ONBOARDING=true`. Elas usam cabeçalhos sintéticos para testar RLS e
não podem ser expostas em produção.
