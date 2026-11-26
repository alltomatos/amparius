# Contrato HTTP de cadastro de pessoa física v1

Status: implementado, 26/11/2026 (ADR 0041, issue #72 / E2E-01B1).

Base: `/v1`. As duas rotas exigem `Authorization: Bearer <token>` e são
montadas por `httpserver.AuthenticatedRoute`. Tenant e empresa vêm do
principal autenticado; nenhum campo do corpo ou cabeçalho os substitui.
Até a decisão de provedor OIDC/MFA (ADR 0038), ambas respondem `503
AUTHENTICATION_UNAVAILABLE`.

## `POST /v1/parties`

Exige `party:write`. Cria uma pessoa física (`kind=person`) no tenant do
principal. O corpo não aceita `tenant_id`, `company_id`, `kind` nem `status`.

Requisição:

```json
{"name": "Pessoa Exemplo", "document": "000.000.000-00", "social_name": null}
```

Os exemplos usam dados sintéticos. Nesta fatia o campo `document` é gravado e
devolvido sem máscara após remover espaços das extremidades. Em testes de
ambiente privado, use valores sintéticos.

Resposta `201 Created`, sem cabeçalho `Location`: esta fatia não expõe
`GET /v1/parties/{id}`, então o único jeito de obter o cadastro criado é o
corpo da resposta (campo `id`) ou uma busca subsequente por `GET
/v1/parties`.

```json
{
  "id": "uuid",
  "kind": "person",
  "name": "Pessoa Exemplo",
  "civilName": "Pessoa Exemplo",
  "socialName": null,
  "displayName": "Pessoa Exemplo",
  "document": "000.000.000-00",
  "status": "active"
}
```

`name` é obrigatório; `document` e `social_name` são opcionais, sujeitos aos
limites de 64 e 200 caracteres, respectivamente. O nome civil tem limite de
200 caracteres. Nome social vazio é armazenado como ausente.

## `GET /v1/parties`

Exige `party:read`. Busca **somente pessoas físicas** (`kind=person`) do
tenant do principal por nome civil, nome social ou documento; o filtro de
kind é aplicado no próprio SQL, antes de `ORDER BY`/`LIMIT`, então uma
pessoa jurídica do mesmo tenant com o mesmo termo nunca ocupa uma posição do
limite em vez de uma pessoa física correspondente.

Parâmetros de query: `q` (texto livre obrigatório, de 1 a 200 caracteres após
remover espaços das extremidades) e `limit` (1–100, padrão 25; fora da faixa é `400
VALIDATION_ERROR`).

Resposta `200 OK`:

```json
{"items": [{
  "id": "uuid",
  "kind": "person",
  "name": "Pessoa Exemplo",
  "civilName": "Pessoa Exemplo",
  "socialName": null,
  "displayName": "Pessoa Exemplo",
  "document": "000.000.000-00",
  "status": "active"
}]}
```

## Erros

Envelope igual ao de `docs/contracts/control-plane-context-v1.md`:

```json
{"code": "VALIDATION_ERROR", "message": "name é obrigatório", "details": {}, "traceId": "0123456789abcdef0123456789abcdef"}
```

Códigos usados nesta fatia, alinhados com os que `auth.Require` realmente
emite (`docs/contracts/auth-http-v1.md`): `AUTHENTICATION_REQUIRED` (401,
autenticação ausente ou inválida), `SCOPE_FORBIDDEN` (403, empresa sem
vínculo `identity.user_company` com o usuário, tenant/empresa fora do escopo
do principal), `MFA_REQUIRED` (403, MFA desativado para o usuário ou sem
confirmação pelo verificador),
`PERMISSION_DENIED` (403, papel sem a permissão `party:write`/`party:read`),
`AUTHENTICATION_UNAVAILABLE`
(503, sem `auth.Gate` configurado — caso atual, ADR 0038 pendente),
`VALIDATION_ERROR` (400) e `PARTY_UNAVAILABLE`
(503, falha do repositório). Nenhuma mensagem de erro inclui documento, nome
completo ou qualquer outro dado pessoal da pessoa física — apenas o motivo
da rejeição e o `traceId`.

## Regra de cadastro por tenant

Party é um cadastro **tenant-scoped**, não company-scoped: `party.party` não
tem `company_id` (DER §1). O principal autenticado fixa o tenant no
contexto HTTP (via `db.TenantFromContext`), e toda criação/busca desta
fatia é isolada por esse tenant através de RLS — qualquer pessoa física
visível em `GET /v1/parties` pertence ao mesmo tenant do principal,
independentemente da empresa (CNPJ) associada à sessão. A empresa da sessão
precisa estar vinculada ao usuário em `identity.user_company`; sem esse
vínculo a requisição falha com `403 SCOPE_FORBIDDEN` antes do handler. A verificação de
empresa no `scope()` do handler é uma defesa redundante (auth.Require já
garante tenant e empresa consistentes antes de chamar o handler) e não
implica isolamento por empresa dentro do cadastro de party em si.

## Limite explícito da fatia

Party é um cadastro tenant-scoped (DER §1: `party.party` não tem
`company_id`); portanto esta fatia não cria isolamento por empresa dentro do
mesmo tenant, apenas autenticação e permissão por papel. Atualização,
remoção, pessoa jurídica e vínculos (endereço, contato, representante) do
RF-003 não fazem parte deste contrato.
