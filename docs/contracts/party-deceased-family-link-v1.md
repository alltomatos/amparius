# Contrato HTTP de falecido, vínculo familiar e consentimento v1

Status: implementado para a issue #73 ([ADR 0042](../adr/0042-falecido-vinculo-familiar-consentimento-v1.md)).

Base `/v1`. As quatro rotas exigem `Authorization: Bearer <token>`.
Tenant e empresa vêm do principal autenticado. O binário atual, sem
autenticador OIDC/MFA configurado, responde `503 AUTHENTICATION_UNAVAILABLE`
nas quatro rotas. Com autenticador configurado, ambos os POSTs exigem
política de privacidade do tenant. Se a política estiver indisponível,
respondem `503 PRIVACY_POLICY_UNAVAILABLE`.

## `POST /v1/deceased`

Exige `party:write`. Cria um cadastro de falecido para uma pessoa física
já cadastrada no tenant do principal. A mesma transação grava a ação
`party.deceased.register` em `audit.log`, com o ID do cadastro, ator,
empresa, trace, códigos da política e data de retenção.

Requisição:

```json
{
  "personId": "uuid",
  "deathAt": "2026-11-20T10:00:00Z",
  "deathCertificate": "0000000-00",
  "burialSite": "Jazigo 12, Quadra B"
}
```

Os exemplos usam dados sintéticos. `personId` e `deathAt` (RFC3339) são
obrigatórios; `deathCertificate` e `burialSite` são opcionais, texto livre com
limite de 200 caracteres Unicode após normalização. Valor vazio é
armazenado como ausente. `personId` referenciando pessoa jurídica, pessoa inexistente ou
pessoa de outro tenant é rejeitado (ver seção Erros).

Resposta `201 Created`:

```json
{
  "id": "uuid",
  "personId": "uuid",
  "deathAt": "2026-11-20T10:00:00Z",
  "deathCertificate": "0000000-00",
  "burialSite": "Jazigo 12, Quadra B",
  "civilName": "Pessoa Exemplo",
  "socialName": null,
  "displayName": "Pessoa Exemplo"
}
```

## `GET /v1/deceased/{id}`

Exige `party:read`. Consulta por id no tenant do principal. Um id de outro
tenant é tratado como `404 NOT_FOUND`, igual a um id inexistente. Resposta igual ao corpo de criação.

## `POST /v1/deceased/{deceasedId}/family-links`

Exige `party:write`. Vincula o familiar identificado por `personId` ao
registro de falecido do path. O vínculo, `privacy.consent_record` e a
trilha em `audit.log` são gravados na mesma transação. O `personId` do
familiar deve ser diferente do ID da pessoa do cadastro de falecido.

Requisição:

```json
{
  "personId": "uuid",
  "relation": "child",
  "consentEvidenceRef": "d9ed095f-f5a6-4d21-b5b3-9694f54753e0"
}
```

`personId`, `relation` e `consentEvidenceRef` são obrigatórios.
`relation` deve ser um código ativo de `identity.domain_value` no domínio
`party_family_relation` (ex.: `child`, `spouse`, `sibling`); código
desconhecido ou inativo é rejeitado. `consentEvidenceRef` é um UUID não
nulo que identifica o artefato externo de consentimento, como um termo
assinado arquivado. A API persiste o UUID em formato canônico e rejeita
texto livre ou conteúdo do documento.

Para ambos os POSTs, `purposeCode`, `legalBasisCode` e `retentionUntil`
vêm exclusivamente da política aprovada para o tenant e são rejeitados
no corpo. O provedor de política é resolvido antes da transação de escrita.

Resposta `201 Created` (quando a política estiver disponível):

```json
{
  "id": "uuid",
  "relation": "child",
  "deceased": {
    "id": "uuid",
    "civilName": "Pessoa Falecida",
    "socialName": null,
    "displayName": "Pessoa Falecida"
  },
  "person": {
    "id": "uuid",
    "civilName": "Familiar Exemplo",
    "socialName": null,
    "displayName": "Familiar Exemplo"
  }
}
```

Nenhum nome, documento ou atributo pessoal é copiado para `party.family_link`
nem para o `detail` de `audit.log`; os nomes na resposta são resolvidos a
partir de `party.party`/`party.person` em cada consulta. O status de
consentimento gravado é sempre `granted` nesta fatia — retirada/negativa fica
para uma fatia futura.

## `GET /v1/family-links/{id}`

Exige `party:read`. Consulta por id no tenant do principal. Um id de outro
tenant é tratado como `404 NOT_FOUND`, igual a um id inexistente. Resposta
igual ao corpo de criação.

## Duplicidade e concorrência

A criação de falecido verifica `(tenant_id, person_id)` e a de vínculo
verifica `(tenant_id, deceased_id, person_id)`. O repositório serializa os
escritores dev e autenticado em transações `READ COMMITTED`. O primeiro
cadastro bem-sucedido retorna `201` e uma nova tentativa para a mesma chave
retorna `409`. Registros duplicados legados continuam consultáveis pelos
IDs originais. Nenhuma migração é necessária.

## Erros

Envelope igual ao de `docs/contracts/control-plane-context-v1.md`:

```json
{"code": "NOT_FOUND", "message": "falecido não encontrado", "details": {}, "traceId": "0123456789abcdef0123456789abcdef"}
```

| Código | HTTP | Quando |
| --- | --- | --- |
| `AUTHENTICATION_REQUIRED` | 401 | autenticação ausente ou inválida |
| `SCOPE_FORBIDDEN` | 403 | empresa sem vínculo `identity.user_company`, tenant/empresa fora do escopo do principal |
| `MFA_REQUIRED` | 403 | MFA desativado ou sem confirmação |
| `PERMISSION_DENIED` | 403 | papel sem `party:write`/`party:read` |
| `AUTHENTICATION_UNAVAILABLE` | 503 | sem `auth.Gate` configurado (ADR 0038 pendente) |
| `VALIDATION_ERROR` | 400 | corpo/campo inválido, ausente ou fora de limite, ou autovínculo |
| `PARTY_KIND_INVALID` | 409 | `personId` referencia pessoa jurídica onde pessoa física é exigida |
| `DECEASED_ALREADY_EXISTS` | 409 | pessoa já cadastrada como falecida no tenant |
| `FAMILY_LINK_ALREADY_EXISTS` | 409 | familiar já vinculado a este falecido |
| `NOT_FOUND` | 404 | id inexistente ou de outro tenant (falecido, vínculo ou pessoa referenciada) |
| `PRIVACY_POLICY_UNAVAILABLE` | 503 | autenticação configurada e política de privacidade indisponível para o tenant |
| `PARTY_UNAVAILABLE` | 503 | falha do repositório não mapeada nas anteriores |

Nenhuma mensagem de erro inclui documento, nome completo, relação em texto
livre com dado pessoal associável ou qualquer outro dado pessoal — apenas o
motivo da rejeição e o `traceId`.

## Limite explícito da fatia

A fatia cobre criação e consulta por ID. Listagem, alterações, retirada de
consentimento, verificação do artefato externo e expurgo ficam fora do
contrato. As rotas `/_dev` conservam os formatos de sucesso em modo de
desenvolvimento e compartilham a rejeição de duplicidades e autovínculos.

A disponibilidade em produção depende de OIDC/MFA (ADR 0038) e de um
provedor de política aprovada por tenant (ADR 0030).
