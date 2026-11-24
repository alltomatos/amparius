# ADR 0012 — Onboarding inicial transacional

Status: aceito · 24/11/2026

## Contexto

O MVP precisa criar o primeiro grupo econômico, uma empresa/CNPJ, um
estabelecimento e o proprietário inicial. As migrations existentes já possuem
`identity.tenant`, `identity.company` e `identity.establishment`, mas ainda não
há contrato persistido para usuário, papel ou idempotência do onboarding.

Também não existe autenticação de produção neste slice. Criar uma senha,
sessão ou vínculo OIDC aqui daria uma falsa aparência de segurança.

## Decisão

- Criar `identity.app_user`, `identity.role`, `identity.user_role` e
  `identity.onboarding_request` em migration nova, sem alterar migrations
  aplicadas.
- Usar `app_user` em vez de `user` para evitar ambiguidade com o identificador
  reservado do PostgreSQL. O papel `owner` é criado no tenant.
- Expor ao backend apenas `identity.create_onboarding(...)`, uma função
  `SECURITY DEFINER` com argumentos do onboarding, mas sem argumento
  `tenant_id`. O UUID do tenant é gerado no banco e o contexto RLS é definido
  antes de inserir os registros tenant-scoped.
- Serializar a chave com advisory lock transacional. A chave e o hash do
  payload são persistidos; replay com o mesmo payload devolve os mesmos IDs e
  replay com payload diferente falha.
- Manter RLS e índices iniciando por `tenant_id` nas tabelas de negócio. O
  acesso direto de escrita às tabelas novas não é concedido ao papel da
  aplicação; somente a função de bootstrap é executável.
- O proprietário inicial fica `pending_auth` e o email de desenvolvimento
  usa o domínio reservado `.invalid`. Vinculação de identidade externa,
  sessão, senha, MFA e recuperação de conta ficam para o contrato de
  autenticação.

## Consequências

O onboarding é uma exceção controlada ao fluxo normal que exige tenant no
contexto: ele ocorre antes de existir uma sessão autenticada, não aceita
`tenant_id` do cliente e só pode ser chamado pela função concedida ao papel da
aplicação. Todas as operações posteriores devem usar o tenant do contexto
autenticado e as políticas RLS normais.

O teste de integração precisa usar um papel sem `SUPERUSER` e sem
`BYPASSRLS`; o cleanup administrativo é feito apenas com a conexão de
administração do teste.
