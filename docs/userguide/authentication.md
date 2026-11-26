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

## Cadastro de pessoa física pela API autenticada (`/v1/parties`)

A rota de produção de pessoa física segue o [contrato
party-pf-v1](../contracts/party-pf-v1.md) e o [ADR 0041](../adr/0041-cadastro-pf-autenticado-v1.md):

- `POST /v1/parties` exige a permissão `party:write`. Responde `201` com o
  cadastro no corpo, campo `id` incluído. Não há cabeçalho `Location` nem rota
  `GET /v1/parties/{id}` nesta fatia.
- `GET /v1/parties?q=termo&limit=25` exige `party:read`. O parâmetro `q` é
  obrigatório e aceita até 200 caracteres após retirar espaços das extremidades.
  `limit` vale de 1 a 100 e o
  padrão é 25. A busca devolve somente pessoas físicas do tenant, e o filtro
  de tipo roda antes do limite, então uma pessoa jurídica com o mesmo termo não
  ocupa a vaga de uma pessoa física.
- O tenant e a empresa vêm do token validado. A empresa da sessão precisa estar
  vinculada ao usuário; sem o vínculo a chamada retorna `403 SCOPE_FORBIDDEN`.
  Um papel sem a permissão retorna `403 PERMISSION_DENIED`.
- Sem credencial, a resposta é `401 AUTHENTICATION_REQUIRED`. Enquanto o
  provedor OIDC/MFA não estiver conectado ao bootstrap, toda chamada a
  `/v1/parties` retorna `503 AUTHENTICATION_UNAVAILABLE`.

### Como testar em ambiente privado

Use somente dados sintéticos, como `Pessoa Exemplo` e o documento
`000.000.000-00`. O campo `document` é gravado e devolvido sem máscara após
remover espaços das extremidades.

As verificações de integração sobem contra PostgreSQL real, com um papel de
aplicação que não tem `SUPERUSER` nem `BYPASSRLS`, e um segundo DSN administrativo
para preparar e limpar os dados de teste. O verificador OIDC é de teste: ele mapeia tokens sintéticos a
identidades, e não é o provedor de produção. Sem os dois DSNs, os casos são
pulados (`SKIP`), nunca simulados.

```bash
AMPA_TEST_POSTGRES_DSN='postgres://amparius_app:<senha-local>@127.0.0.1:5432/amparius_teste?sslmode=disable' \
AMPA_TEST_POSTGRES_ADMIN_DSN='postgres://<admin-local>:<senha-local>@127.0.0.1:5432/amparius_teste?sslmode=disable' \
go test -p 1 ./internal/modules/party -run '^TestProductionPartiesIntegration' -count=1 -v
```

Sem os DSNs, os testes unitários do handler rodam com fakes:

```text
go test ./internal/modules/party -run '^TestProductionParties' -count=1 -v
```

A última execução registrada na #72 teve 8 casos aprovados em PostgreSQL 16
temporário, com migrations até 0035, e as contagens de `identity.tenant` e
`party.party` voltaram a zero no cleanup. Isso prova o contrato no handler e no
repositório, não o login real, a UI nem o ambiente de produção.

## Estado do bootstrap do control plane

As rotas CP-06 do binário `cmd/api` ainda não possuem um provedor OIDC/MFA
conectado. Enquanto o provedor, o contrato de claims e o adaptador seguro não
forem escolhidos, elas permanecem fail-closed e respondem `503
AUTHENTICATION_UNAVAILABLE`; nenhum token sintético ou cabeçalho de
desenvolvimento é aceito nessas rotas. O mesmo vale para `/v1/parties`. O
desbloqueio exige a decisão e o gate descritos no [ADR
0038](../adr/0038-control-plane-cp06.md).
