# Engenharia — Amparius

Documento interno da equipe. O README da raiz é comercial e não deve conter detalhes técnicos.

## Stack

| Camada | Tecnologia |
|---|---|
| SaaS | Go (binários `api` e `worker`) |
| Borda | Rust (relay local e cliente desktop) |
| Dados | PostgreSQL 16+, Redis 7+, RabbitMQ 3.13+ |
| Arquivos | Object storage S3-compatible (XML, PDF, anexos) |
| IA | Gateway MCP interno + provedores de modelo |

## Forma

Monólito modular: um repositório, dois deploys no SaaS (`api`, `worker`) e um binário de relay. Módulos com schema e contrato próprios; integração por serviço público ou evento versionado. Microsserviço só com ADR e evidência de gargalo.

Implantação por **células** em VPS (ver `docs/architecture/SCALING.md`): cada célula atende um conjunto de tenants; o control plane decide a colocação.

## Estrutura prevista

```
cmd/api/            HTTP do SaaS
cmd/worker/         filas, jobs, emissão fiscal
internal/modules/   identity, party, catalog, finance, fiscal, funeral, cemetery, floriculture, inventory, documents, ai, sync
internal/platform/  banco, fila, cache, auth, observabilidade
migrations/         SQL forward-only
relay/              workspace Rust do relay (papel servidor da loja)
client/             workspace Rust do cliente desktop (papel estação)
docs/               PRD, arquitetura, DER, UML, ADRs, pesquisa
```

## Documentos

- `docs/prd/PRD.md` — produto e escopo
- `docs/architecture/ARCHITECTURE.md` — arquitetura, relay, fiscal, IA
- `docs/architecture/SCALING.md` — células em VPS, capacidade e migração de tenant
- `docs/design/MOCKUPS.md` — telas, decisões visuais e como regenerar
- `docs/design/AI-ASSISTANT.md` — contrato do assistente contextual
- `docs/data/DER.md` — modelo de dados
- `docs/uml/UML.md` — componentes, sequências, estados
- `docs/adr/` — decisões registradas
- `docs/research/receita-federal.md` — RTC e DANFSe
- `docs/roadmap/ROADMAP.md` — fases e critérios de saída
- `AGENTS.md` — regras obrigatórias para agentes e pessoas

## Ambiente

```bash
.\scripts\dev.ps1 up                        # docker compose up -d (Postgres, Redis, RabbitMQ, MinIO)
go run ./cmd/api                            # SaaS
go run ./cmd/worker                         # worker
cargo run --manifest-path relay/Cargo.toml  # relay (servidor da loja)
cargo run --manifest-path client/Cargo.toml # client (estação)
```

## Estado

Fase 1 em andamento: `cmd/api` + `cmd/worker` (Go, health), `relay/` +
`client/` (Rust, health), migrações `identity` e `party/catalog` com RLS,
acesso Go a `party` e `catalog` com tenant obrigatório por transação e teste
real de isolamento PostgreSQL.

### Onboarding inicial de identidade

`migrations/0009_identity_onboarding.sql` adiciona `identity.app_user`,
`identity.role`, `identity.user_role` e `identity.onboarding_request`, todos
com `tenant_id` quando são tenant-scoped, índices iniciados por `tenant_id` e
RLS. O repositório Go fica em `internal/modules/identity`.

O método `identity.Repository.Onboard` não recebe `tenant_id`: chama a função
transacional `identity.create_onboarding`, que gera o UUID, cria o grupo
econômico, a empresa, o estabelecimento e o owner. A chave de onboarding é
idempotente e o mesmo valor com payload diferente falha. A função é um
bootstrap interno pré-autenticação; o usuário criado fica `pending_auth` e
não há senha, sessão, MFA ou vínculo OIDC neste slice. Os dados de
`DevelopmentRequest` (CNPJ e email `.invalid`) são sintéticos.

O `cmd/api` registra `POST /_dev/onboarding` somente quando
`APP_ENV=development` e `ENABLE_DEV_ONBOARDING=true`. Sem o opt-in, a API não
abre conexão PostgreSQL no boot por causa desta rota e mantém somente as rotas
base. Essa rota não tem autenticação de produção, exige `Idempotency-Key` e aceita
somente sobreposições conhecidas da carga sintética no corpo JSON; `tenant_id`,
`onboarding_key` e campos desconhecidos são rejeitados. Cidade, estado e CEP
continuam fixos em Fortaleza/CE. A rota não deve ser publicada nem habilitada
em ambiente de produção.

Com o mesmo opt-in, `cmd/api` também registra `POST /_dev/parties` e
`GET /_dev/parties/{id}`. O tenant vem exclusivamente do cabeçalho
`X-Amparius-Development-Tenant-ID`, validado como UUID e injetado no contexto
antes do repositório `party`; `tenant_id` no corpo e campos desconhecidos são
rejeitados. Esta fatia aceita somente `kind=person`, com `name` obrigatório,
`document` opcional e `status` restrito a `active`/`inactive` (padrão
`active`). Toda resposta carrega `X-Amparius-Development-Only: true`. Não há
autenticação de produção, então as rotas não devem ser publicadas.

O cadastro de pessoa aceita `social_name` opcional (ADR 0013). O repositório
persiste o valor em `party.person.social_name`; vazio ou só espaços é
normalizado para ausência (`NULL`) e nunca copia o nome civil. `name` continua
o nome civil e a resposta adiciona, sem remover campos, `civil_name`,
`social_name` (nulo quando ausente) e `display_name` (nome social não vazio
para pessoa; nome civil nos demais casos). Nenhum endpoint aceita
`tenant_id`; o nome social não é usado em identificação, documento ou regra
fiscal.

`PATCH /_dev/parties/{id}` altera somente `social_name` da pessoa: exige o
campo no corpo (texto ou `null`), rejeita `tenant_id` e campos desconhecidos,
recusa pessoa jurídica com `409` e preserva nome civil, documento e
identificadores. `null`, vazio ou só espaços limpam para `NULL`. A alteração
avança `version` e `updated_at` de `party.person` e de `party.party` na mesma
transação e continua restrita ao tenant do cabeçalho.

### Cadastro dev-only de falecido

Com o mesmo opt-in (`APP_ENV=development` e `ENABLE_DEV_ONBOARDING=true`), a API
registra `POST /_dev/deceased` e `GET /_dev/deceased/{id}`. O tenant é aceito
somente pelo cabeçalho `X-Amparius-Development-Tenant-ID`; `tenant_id` no corpo
e campos desconhecidos são rejeitados. O POST exige `person_id` de uma pessoa
física já existente no tenant e `death_at` em RFC3339. `death_certificate` e
`burial_site` são opcionais, normalizados para ausência quando vazios e
limitados a 200 runas por segurança de transporte; o limite não representa
regra legal. Pessoa inexistente ou de outro tenant responde `404` sem revelar
qual caso ocorreu; pessoa jurídica responde `409`. O endpoint não cria party ou
person implicitamente nem cria vínculo familiar: o vínculo é uma fatia separada,
descrita abaixo.

O POST devolve `201` com cabeçalho `Location: /_dev/deceased/{id}` e envelope
`{"status":"ok","deceased":{...}}`; o GET devolve `200` com o mesmo envelope.
Erros usam `{"status":"error","error":"..."}`: `400` para cabeçalho ou carga
inválidos, `404` para pessoa/falecido não encontrado, `409` para pessoa
jurídica, `405` para método não permitido, `500` para falha inesperada e `503`
quando o serviço não está ligado. `death_at` entra em RFC3339 e sai em
RFC3339Nano. A fatia não inventa dado sintético próprio: `person_id` precisa
apontar para uma pessoa criada pelo fluxo dev de cadastro, e
`death_certificate`/`burial_site` são textos livres fornecidos pelo chamador.

A resposta resolve os nomes na origem e expõe `civil_name`, `social_name` e
`display_name`; `party.deceased` guarda apenas a referência `person_id` e os
dados do óbito, sem duplicar nome. Todas as respostas, inclusive erros, têm
`X-Amparius-Development-Only: true` e `Cache-Control: no-store`. A tabela
existente já possui FK composta e RLS; `migrations/0011_party_deceased_grants.sql`
apenas acrescenta ao papel `amparius_app` os privilégios mínimos que faltavam
para as queries do módulo; `USAGE` em `party` e a leitura de `party.person`
continuam vindo de `0010_party_social_name.sql`. O teste de integração usa uma
role temporária `NOSUPERUSER/NOBYPASSRLS` com somente esse conjunto efetivo e
verifica que ela não pode inserir/atualizar party/person. O padrão dev existente
não usa `Idempotency-Key` nem possui unicidade por pessoa, portanto cada POST
válido cria um novo registro; não foi inventada idempotência sem
coluna/contrato no schema atual.

Os grants de escrita do cadastro base são independentes em
`migrations/0012_party_core_grants.sql`: `SELECT/INSERT/UPDATE` em
`party.party`, sem `DELETE`, porque os endpoints atuais não possuem caminho de
remoção. Essa separação não altera RLS nem concede escrita adicional ao fluxo
de falecido.

Esta fatia cobre apenas parte do RF-004: o cadastro/consulta do falecido e a
resolução de nomes. Familiares ficam na fatia de vínculo familiar descrita a
seguir; consentimento, base legal e retenção definida não fazem parte deste
slice, e nenhuma política jurídica foi inferida ou criada.

### Vínculo familiar dev-only

Com o mesmo opt-in (`APP_ENV=development` e `ENABLE_DEV_ONBOARDING=true`), a API
registra `POST /_dev/family-links` e `GET /_dev/family-links/{id}` e grava em
`party.family_link`. Sem o opt-in as duas rotas não são registradas e respondem
`404`; nada abaixo vale em produção.

Tenant e cabeçalhos. O tenant vem somente do cabeçalho
`X-Amparius-Development-Tenant-ID`, validado como UUID não nulo e injetado no
contexto antes do repositório; ausente ou inválido responde `400`. Nenhum
endpoint aceita `tenant_id` no corpo. Todas as respostas, inclusive erros, têm
`X-Amparius-Development-Only: true` e `Cache-Control: no-store`, e não há
autenticação de produção.

Contrato do POST. O corpo é exatamente um objeto JSON e aceita apenas
`deceased_id`, `person_id` e `relation`; `tenant_id` e qualquer campo
desconhecido são rejeitados com `400`, assim como corpo vazio, JSON inválido ou
mais de um JSON. `deceased_id` e `person_id` são UUIDs obrigatórios e
`relation` é texto obrigatório, aparado nas pontas e limitado a 40 runas — limite
de transporte, não regra legal. Nem o falecido nem o familiar são criados
implicitamente: `deceased_id` precisa apontar para `party.deceased` existente e
`person_id` para `party.party` com `kind=person` e a linha companheira em
`party.person`, ambos no mesmo tenant.

Códigos. `deceased_id` inexistente ou de outro tenant responde `404`, assim como
`person_id` inexistente ou de outro tenant — a resposta não revela qual caso
ocorreu. `person_id` de pessoa jurídica responde `409`. `relation` é validada
contra o domínio ativo `party_family_relation` em `identity.domain_value`; valor
fora do domínio responde `400` antes de qualquer escrita. A validação fica no
repositório, na mesma transação tenant-scoped do INSERT, e o trigger
`party.validate_family_link_domains` continua como rede de segurança.

Respostas. O POST devolve `201` com `Location: /_dev/family-links/{id}` e
envelope `{"status":"ok","family_link":{...}}`; o GET devolve `200` com o mesmo
envelope, `404` quando o vínculo não existe no tenant do cabeçalho e `400`
quando o `id` da rota não é UUID válido. Erros usam
`{"status":"error","error":"..."}`: `400` para cabeçalho/carga/relação/id
inválidos, `404` para falecido, familiar ou vínculo não encontrado, `409` para
pessoa jurídica, `405` com `Allow` para método não permitido, `500` para falha
inesperada e `503` quando o serviço não está ligado. Apenas leitura e criação
existem: não há `PATCH` nem `DELETE` nesta fatia.

Nomes e persistência. A resposta resolve os nomes na origem e expõe, em cada
lado do vínculo, `id`, `civil_name`, `social_name` e `display_name`;
`party.family_link` guarda apenas `deceased_id`, `person_id` e `relation`, sem
coluna de nome, e o teste de integração verifica isso em `information_schema`.
O teste também confirma que o POST não cria `party` implícita.

Migration 0013. A tabela já existe desde `0002_party_catalog.sql`, com FK
composta por tenant, RLS e `UNIQUE (tenant_id, id)`;
`migrations/0013_party_family_link_grants.sql` só ajusta permissões do papel de
aplicação, sem alterar modelo, política ou dados. Concede ao papel `amparius_app`
`SELECT/INSERT` em `party.family_link` — sem `UPDATE`/`DELETE`, porque a fatia
não expõe alteração nem remoção — e `SELECT` em `identity.domain_value`, leitura
global e somente leitura exigida tanto pela validação quanto pelo trigger na
inserção. `USAGE` em `identity` vem de `0009`, por isso não é repetido nem
revogado no Down, que revoga exatamente os dois `GRANT SELECT/INSERT` acima.

Teste. O teste de integração usa uma role temporária
`NOSUPERUSER/NOBYPASSRLS` com os grants da 0013 mais o mínimo que as queries do
módulo exigem (`SELECT` em `party.party`, `party.person` e `party.deceased` e
`EXECUTE` em `public.uuidv7()`), valida os privilégios por
`has_table_privilege`/`has_function_privilege` e prova isolamento cruzado entre
tenants.

Sem idempotência. Não há `Idempotency-Key`, chave de idempotência persistida nem
unicidade por (falecido, familiar, relação) nesta fatia: a única unicidade da
tabela é `(tenant_id, id)`. Como no cadastro dev de falecido, cada POST válido
cria um novo vínculo e a repetição não é deduplicada. A decisão acompanha o
contrato dev existente e não inventa idempotência sem coluna, chave ou
unicidade definidas no schema; a deduplicação de vínculos fica para um slice
próprio, com regra de negócio e migração.

RF-004 parcial. Com este vínculo, o RF-004 continua **parcial**: cadastro e
consulta do falecido, vínculo com familiares existentes e resolução de nomes
estão cobertos, mas consentimento, base legal e retenção definida seguem fora do
escopo. Nenhuma política jurídica foi inferida ou criada, e o slice não registra
consentimento nem prazo de guarda.

### Catálogo dev-only

Com o mesmo opt-in (`APP_ENV=development` e `ENABLE_DEV_ONBOARDING=true`), a API
registra a fatia vertical mínima de catálogo:

- `POST /_dev/catalog/items`, `GET /_dev/catalog/items` e
  `GET /_dev/catalog/items/{id}`;
- `POST /_dev/catalog/price-lists`, `GET /_dev/catalog/price-lists` e
  `GET /_dev/catalog/price-lists/{id}`;
- `POST /_dev/catalog/prices`, `GET /_dev/catalog/prices` e
  `GET /_dev/catalog/prices/{id}`.

Sem o opt-in nenhuma dessas rotas é registrada e todas respondem `404`. A fatia
usa as tabelas já existentes em `0002_party_catalog.sql`; `0002` não foi
editada e `catalog.tax_classification` não é lida nem escrita.

Tenant e contrato. O tenant vem somente do cabeçalho
`X-Amparius-Development-Tenant-ID`, validado como UUID não nulo e injetado no
contexto antes do repositório; ausente ou inválido responde `400`. Esse
cabeçalho é checado antes do despacho por rota, então falta ou formato inválido
devolve `400` mesmo quando o método ou o recurso também estariam errados.
Nenhuma rota aceita `tenant_id` no corpo: o corpo é exatamente um objeto JSON,
limitado a 16 KiB, e campos desconhecidos são rejeitados com `400`, assim como
JSON inválido ou mais de um JSON. As respostas deste handler, inclusive erros,
têm `Content-Type: application/json`, `X-Content-Type-Options: nosniff`,
`X-Amparius-Development-Only: true` e `Cache-Control: no-store`; não há
autenticação de produção, e a requisição não precisa declarar
`Content-Type`.

Itens. O POST exige `kind`, `name` e `unit`; `active` é opcional e assume
`true`. `kind` e `unit` são validados contra os domínios ativos
`catalog_item_kind` e `catalog_unit` de `identity.domain_value` na mesma
transação tenant-scoped da escrita; valores fora do domínio respondem `400` e o
trigger `catalog_item_validate_domains` continua como rede de segurança. `name`
é aparado e limitado a 200 runas — limite de transporte, não regra legal. O POST
devolve `201` com `Location: /_dev/catalog/items/{id}` e envelope
`{"status":"ok","item":{...}}`.

Listas de preços. O POST exige `name` (até 200 runas) e `valid_from` em ISO
date (`AAAA-MM-DD`); devolve `201` com `Location:
/_dev/catalog/price-lists/{id}` e envelope `{"status":"ok","price_list":{...}}`.

Preços. O POST exige `item_id` e `list_id` (UUIDs) e `valid_from` em ISO date;
`valid_to` é opcional e, quando presente, também precisa estar em ISO date e não
pode ser anterior a `valid_from`; formato inválido responde `400`.
`item_id` e `list_id` precisam existir no mesmo tenant; referência de outro
tenant ou inexistente responde `404` sem revelar qual caso ocorreu, e a FK
composta por tenant permanece como rede de segurança. `amount_cents` é
obrigatório e aceita somente literal JSON inteiro: string, decimal, expoente,
`null` e valor negativo respondem `400`. O valor é gravado como
`NUMERIC(18,2)` a partir de centavos `int64`, sem `float`; a resposta devolve
`amount_cents` como inteiro e `valid_to` como `null` quando ausente. O POST
devolve `201` com `Location: /_dev/catalog/prices/{id}` e envelope
`{"status":"ok","price":{...}}`.

Listagens. As três listagens aceitam `?limit=` inteiro positivo, com padrão 50
e teto efetivo de 100 linhas no repositório; `limit` ausente usa o padrão e
valor não inteiro ou não positivo responde `400`. Listas vazias serializam como
`[]`, nunca `null`.

Códigos. `400` para cabeçalho, JSON, campo, domínio, data, valor ou `limit`
inválidos; `404` para registro inexistente ou de outro tenant; `405` com
`Allow` para método não permitido; `500` para falha inesperada; `503` quando o
handler recebe serviço nulo — defesa do handler, já que o binário da API sempre
injeta o repositório. Apenas criação e leitura existem: não há `PATCH` nem
`DELETE` nesta fatia.

Migration 0014. `migrations/0014_catalog_dev_grants.sql` concede ao papel
`amparius_app` `USAGE` no schema `catalog` e `SELECT/INSERT` nas três tabelas,
sem `UPDATE`/`DELETE`, porque a fatia não expõe alteração nem remoção. O
`SELECT` em `identity.domain_value`, exigido pela validação e pelo trigger na
inserção, já vem de `0013_party_family_link_grants.sql`; o `USAGE` em `party`,
exigido pela chamada `party.assert_domain_value` do trigger, já vem de
`0010_party_social_name.sql`. Nenhum dos dois é repetido em 0014 para que o
Down não revogue permissão de outra fatia. RLS e políticas de `0002` não foram
alteradas.

Teste. O teste de integração usa uma role temporária
`NOSUPERUSER/NOBYPASSRLS` com os grants da 0014 mais o mínimo que o trigger e as
queries exigem (`SELECT` em `identity.domain_value`, `EXECUTE` em
`public.uuidv7()` e `USAGE` em `party`), valida por
`has_schema_privilege`/`has_table_privilege`/`has_function_privilege` que não há
`UPDATE`/`DELETE` nem escrita em `identity.domain_value`, prova criação/consulta,
isolamento cruzado entre tenants, rejeição de `tenant_id` no corpo e de
`amount_cents` decimal, e confirma o cleanup administrativo com zero tenants
residuais.

Sem idempotência. Não há `Idempotency-Key`, chave persistida nem unicidade
natural além de `(tenant_id, id)`: cada POST válido cria um novo registro e a
repetição não é deduplicada. A fatia acompanha o contrato dev existente e não
inventa idempotência sem coluna ou contrato no schema.

RF-010 parcial. Esta entrega cobre apenas produto/serviço, unidade, lista de
preços e vigência. `NCM`, `CST`, `cClassTrib`, código `LC 116` e a tabela
`catalog.tax_classification` permanecem fora do escopo e o RF-010 continua
**parcial**; nenhum código ou alíquota fiscal foi inferido, criado ou copiado.

`GET /readyz` continua um stub nesta fase: responde sucesso sem verificar
PostgreSQL, Redis, RabbitMQ ou MinIO.

## Regras que valem para todo mundo

1. Fiscal não se estima.
2. Toda query passa por RLS com tenant no contexto.
3. Dinheiro em NUMERIC/int64; nunca float.
4. Migração forward-only.
5. Mudança estrutural exige ADR.
6. Nada de segredo no repositório.
7. Pronto exige teste ou evidência.

## Acesso PostgreSQL e RLS

### Control plane e enforcement de entitlement

O control plane usa um PostgreSQL separado, acessado pelo DSN
`AMPA_CONTROL_POSTGRES_DSN`; o DSN da célula (`AMPA_TEST_POSTGRES_DSN`) não
pode ser reutilizado. O pacote `internal/modules/entitlement` consulta apenas
`entitlement.module` e `entitlement.license_assignment` e não escreve
`entitlement_event` neste slice.

O catálogo permite diretamente os módulos `core` ativos. Módulos `optional`
exigem `license_assignment.status = 'active'`, o mesmo `module_code` e
`company_id`, além de `valid_from <= data <= valid_to` (com `valid_to` nulo
permitido). Módulo inexistente/inativo, assignment ausente, vencido ou
revogado bloqueia; falha de banco também bloqueia com erro distinto
`ErrEntitlementUnavailable`.

O checker exige o tenant em `db.WithTenant(ctx, tenantID)` e abre uma transação
que executa `set_config('app.tenant_id', ..., true)` antes da consulta, para
que o RLS do control plane seja efetivo.

#### Contexto confiável de empresa

`db.WithCompany(ctx, companyID)` fixa a empresa/CNPJ autenticada na mesma
camada de escopo de requisição que o tenant; `db.CompanyFromContext` a lê. É
uma primitiva de plataforma, não um campo de payload: nenhum endpoint aceita
`company_id` do corpo, e a empresa só entra no contexto depois da autenticação
(ou, na fase dev, de um cabeçalho explícito, nunca do JSON).

`Checker.Check(ctx, module, at)` deriva **tenant e empresa do contexto**;
`Checker.CheckCompany(ctx, module, companyID, at)` existe apenas para
chamadores internos que já resolveram a empresa de forma confiável (por
exemplo, um worker processando o CNPJ de um documento fiscal). Nos dois
caminhos, módulo `core` não consulta assignment; módulo `optional` exige
assignment ativa e vigente para aquele `company_id`.

Sem tenant ou sem empresa no contexto, a chamada bloqueia com
`ErrEntitlementUnavailable` (falha ao decidir); parâmetro inválido, módulo
inexistente/inativo, assignment ausente/vencido/revogado e empresa de outro
tenant bloqueiam com `ErrModuleDisabled`. Ambos os sentinels continuam
distinguíveis por `errors.Is`.

Como defesa em profundidade contra uma empresa forjada no contexto, o checker
aceita `entitlement.WithCompanyResolver`. O módulo `identity` implementa o
contrato em `Repository.CompanyBelongsToTenant`, consultando
`identity.company` sob RLS: empresa inexistente, excluída ou de outro tenant
devolve `(false, nil)` e nega com `ErrModuleDisabled`; falha de leitura devolve
erro e bloqueia com `ErrEntitlementUnavailable`. O entitlement **não importa**
`identity`; a composição injeta o resolver na construção do checker, no
binário que já tem os dois DSNs (control plane e célula).

O teste de integração do control plane (`AMPA_CONTROL_POSTGRES_DSN`) deve usar
um papel de aplicação sem `SUPERUSER` e sem `BYPASSRLS` (por exemplo,
`amparius_control_app`). Sem esse DSN, o teste é marcado como `SKIP` e o
relatório deve registrar a ausência claramente. A verificação do resolver
(`TestCompanyBelongsToTenantScopesByTenantAndRLS`) usa
`AMPA_TEST_POSTGRES_DSN` para a célula e `AMPA_TEST_POSTGRES_ADMIN_DSN` para o
cleanup administrativo, com o papel de aplicação `amparius_app`.

### Migration Finance

`migrations/0003_finance.sql` cria o schema `finance` para contas, centros de
custo, contas bancarias, titulos, parcelas, pagamentos, caixa e ledger. Os
codigos de dominio continuam em `identity.domain_value`; a migration nao cria
enums PG. Todas as tabelas carregam `tenant_id` e RLS. `finance.ledger_entry` e
particionado por competencia mensal e, assim como pagamentos e movimentos de
caixa, e append-only. O limite de pagamento e validado no banco em trigger,
bloqueando a parcela durante a soma dos pagamentos para evitar sobrepagamento
por corrida.

Como `0003` ja foi aplicada, `migrations/0015_finance_grants.sql` corrige os
privilegios sem editar a migration original: `amparius_app` recebe `USAGE` em
`finance` e `SELECT, INSERT` nas nove tabelas do modelo. A `0016` remove
`UPDATE, DELETE` que existiam fora do contrato; a `0017` concede somente
`UPDATE` em `finance.installment`, exigido pelo `SELECT ... FOR UPDATE` do
trigger de sobrepagamento. Nao ha `DELETE` para o modelo, e pagamentos,
movimentos, ledger e historico continuam protegidos tambem pelos triggers
append-only/historicos de `0003`. Centros de custo e contas bancarias ja
possuem grants para o modelo, embora o repositorio Go atual ainda nao exponha
casos de uso para eles.

Os repositórios Go de `party`, `catalog` e `finance` usam `internal/platform/db`.
O repositório Finance expõe valores monetários como centavos `int64` e converte
explicitamente para `NUMERIC(18,2)`, mantendo pagamentos, movimentos de caixa e
lançamentos em transações isoladas por tenant. O tenant
é colocado no `context.Context` com `db.WithTenant`; cada operação abre uma
transação e executa `SELECT set_config('app.tenant_id', $1, true)` antes de
qualquer query. Sem tenant no contexto, a operação falha com
`db.ErrTenantRequired`.

O teste de isolamento deve usar `AMPA_TEST_POSTGRES_DSN` apontando para um
papel de aplicação sem `SUPERUSER` e sem `BYPASSRLS`. O teste Finance também
exige `AMPA_TEST_POSTGRES_ADMIN_DSN`, uma segunda conexão administrativa usada
somente no `t.Cleanup` para remover os tenants de teste e seus registros
append-only; sem esse DSN, nenhum dado Finance é criado. O usuário
administrativo `amparius` do Compose é superusuário e não serve para provar RLS,
mas pode ser usado exclusivamente no cleanup. Em ambiente local, após as
migrações, o papel de aplicação pode ser preparado com:

As grants do Finance sao aplicadas por `0015`; a criacao da role e os grants de
`identity`, `party` e `catalog` continuam sendo responsabilidade das
migrations respectivas. O papel deve permanecer `NOSUPERUSER NOBYPASSRLS`.

O segredo não deve ser versionado; o DSN é somente variável de ambiente.

O teste de integração Finance agora comprova os grants efetivos, isolamento
cruzado em todas as tabelas do schema, ledger balanceado, rejeicao de mutacoes,
sobrepagamento concorrente e cleanup administrativo sem residuos. Sem os dois
DSNs, o teste faz `SKIP` antes de criar dados.

Esta subissue (#16 / FIN-01A) entrega o modelo, RLS, grants e invariantes de
banco. A #17 (FIN-01B) entrega a idempotencia persistida e os casos de uso
transacionais de pagamento e caixa. A #18 (FIN-01C) ainda precisa expor o
contrato HTTP dev-only, jornada, JSON estrito, headers/no-store e
observabilidade minima.

Na #17, `finance.payment` e `finance.cash_movement` passam a aceitar uma chave
de idempotencia por tenant, com indice unico e replay que devolve o registro
original sem exigir UPDATE em tabelas append-only. Os metodos
`CreatePaymentIdempotent` e `CreateCashMovementIdempotent` rejeitam chave vazia
ou acima de 200 runas; os metodos legados geram uma chave interna de
compatibilidade. A migration `0018_finance_idempotency.sql` e forward-only.
Autenticação de produção permanece fora desta superfície dev-only.

### API Finance dev-only (#18 / FIN-01C)

Com `APP_ENV=development` e `ENABLE_DEV_ONBOARDING=true`, `cmd/api` registra
somente a superfície HTTP dev-only do Finance. Sem os dois valores, as rotas não
existem e respondem `404`. O tenant é aceito exclusivamente em
`X-Amparius-Development-Tenant-ID` e imediatamente colocado no contexto com
`db.WithTenant`; `tenant_id` no JSON é campo desconhecido e é rejeitado.

As rotas mínimas são:

- `POST/GET /_dev/finance/accounts` e `GET /_dev/finance/accounts/{id}`;
- `POST/GET /_dev/finance/titles` e `GET /_dev/finance/titles/{id}`;
- `POST/GET /_dev/finance/installments`, `GET /_dev/finance/installments/{id}`
  e `POST/GET /_dev/finance/titles/{id}/installments`;
- `POST /_dev/finance/payments` para baixa;
- `POST /_dev/finance/cash-sessions` e `GET /_dev/finance/cash-sessions/{id}`;
- `POST/GET /_dev/finance/cash-movements` (a consulta exige `session_id`);
- `POST/GET /_dev/finance/ledger-entries` (a consulta aceita `account_id`).

Os corpos são objetos JSON estritos, limitados a 16 KiB, com datas em
`AAAA-MM-DD`, instantes em RFC3339 e valores somente como inteiros
`*_cents` (`int64`). Campos desconhecidos, JSON duplicado, referências inválidas
e valores monetários em decimal/string são rejeitados. Baixas e movimentos de
caixa exigem `Idempotency-Key`; a chave é encaminhada ao repositório persistente
e um replay devolve o registro original sem duplicação. As FKs compostas do
Finance e o RLS recusam referências de outro tenant.

Toda resposta dessas rotas traz `X-Amparius-Development-Only: true`,
`Cache-Control: no-store`, `Content-Type: application/json` e não expõe a
mensagem SQL ou dados pessoais em erros. O middleware HTTP mantém o
`X-Request-Id` e registra método, rota, status, bytes e duração; não registra
query string nem IP. Nenhuma regra fiscal é criada ou inferida nessa jornada.

Os testes unitários de contrato estão em
`internal/modules/finance/http_test.go`. A prova HTTP PostgreSQL real está em
`internal/modules/finance/http_integration_test.go`: exige
`AMPA_TEST_POSTGRES_DSN` com `amparius_app` (`NOSUPERUSER NOBYPASSRLS`) e
`AMPA_TEST_POSTGRES_ADMIN_DSN` somente para cleanup. Sem essas variáveis o teste
faz `SKIP` antes de criar dados. Com as variáveis, reproduza com:

```powershell
$env:AMPA_TEST_POSTGRES_DSN = 'postgres://...'
$env:AMPA_TEST_POSTGRES_ADMIN_DSN = 'postgres://...'
go test ./internal/modules/finance -run 'TestDevelopmentFinanceHTTPPostgresTenantIsolationAndCleanup' -v -count=1
```

### Migration Fiscal

`migrations/0004_fiscal.sql` cria o schema `fiscal` para a configuração fiscal
por empresa, certificados referenciados por cofre/KMS e regras tributárias
datadas. Os códigos são domínios em `identity.domain_value`, validados por
trigger; não há enums PostgreSQL, alíquotas embutidas nem tabelas de códigos
fiscais nesta etapa.

`fiscal.tax_rule` aceita regras oficiais globais (`tenant_id IS NULL`) somente
para leitura e regras de exceção isoladas por tenant. A vigência usa intervalo
half-open e exclusão GiST para impedir sobreposição da mesma chave lógica.
Regras oficiais são append-only; novas publicações devem ser novas linhas com
fonte, versão, data e hash SHA-256. O motor fiscal futuro deve falhar fechado
com `ErrTaxRuleNotFound` quando não encontrar regra oficial datada.

O checker Go consulta somente linhas vigentes em `valid_from <= competência <
valid_to` (ou sem `valid_to`). Se houver regra do tenant, ela tem precedência
explícita sobre a global; uma única candidata é obrigatória e ausência ou
ambiguidade falha fechada. Erros de banco retornam `ErrTaxRuleUnavailable`,
distinto de `ErrTaxRuleNotFound`. O tenant é obrigatório no contexto via
`db.WithTenant`, e a transação configura `app.tenant_id` antes da consulta.
Company/establishment e município devem ser resolvidos pela sessão autenticada
e pelo caso de uso, nunca confiados diretamente ao corpo HTTP. Este slice não
importa nem insere dados tributários oficiais.

As combinações de `tax_type`, escopo e códigos fiscais são validadas no banco;
as formas de cálculo exigem os campos próprios de `basis_kind` (inclusive
faixa do Simples e valor por unidade). Hashes de certificado e regra são
hexadecimais minúsculos; `source_published_at` e `import_batch` identificam a
origem obrigatória de cada regra.

Certificados nunca armazenam senha, PIN, chave privada, PFX, token ou bytes
criptográficos: apenas metadados e uma referência opaca ao Vault/KMS.

`0005_fiscal_documents.sql` acrescenta a trilha operacional fiscal: `fiscal_document`
e `fiscal_item` particionados por competência mensal, `tax_calc` com entrada e
hash da regra usada no snapshot, e `emission_attempt` com chave de idempotência
por tenant e resposta bruta/hash por tentativa. As tabelas têm RLS, chaves
compostas tenant/documento/competência para impedir referências cruzadas e
histórico append-only onde aplicável; nenhum dado tributário ou seed de alíquota
é inserido. A emissão guarda apenas a referência do XML em object storage e seu
hash no documento.

### Contrato Go de documentos fiscais

`internal/modules/fiscal/documents.go` concentra o acesso às tabelas da
migração 0005. Todas as operações recebem `context.Context` primeiro e usam
`db.WithTenantTx`; sem tenant no contexto a operação falha fechada. O contrato
expõe documentos, itens, snapshots `tax_calc` e tentativas de emissão. Valores
monetários chegam e saem como centavos `int64`, convertidos para `numeric` no
SQL; quantidade e alíquota permanecem textuais para não usar `float`.

Snapshots, itens e tentativas são somente inclusão. `RegisterEmissionAttempt`
usa `(tenant_id, idempotency_key)` como chave de replay e devolve a tentativa
original sem atualizar a resposta. O histórico pode ser consultado por
documento/competência e as respostas são apenas corpo, tipo, protocolo e hash;
segredos não fazem parte do contrato.

A migration 0008 adiciona à tentativa os metadados de transporte necessários
à NFS-e (`http_status`, `ambiente`, `chave_acesso`, `dps_id` e
`request_hash`). Hashes são SHA-256 hexadecimais minúsculos; ambiente é domínio
versionado (`producao_restrita` ou `producao`) e nenhuma coluna guarda segredo.

### Outbox transacional e dispatch

`migrations/0007_outbox.sql` cria o schema `outbox` com `outbox.event` e a
trilha append-only `outbox.event_attempt`. O evento carrega `tenant_id`,
`aggregate_type`, `aggregate_id`, `event_type` (`<modulo>.<entidade>.<acao>`),
`event_version`, `payload jsonb` objeto, `idempotency_key`, `status`,
`attempts`, `max_attempts`, `next_attempt_at`, `last_error`, `created_at` e
`processed_at`. Os estados são domínios em `identity.domain_value`
(`outbox_event_status`, `outbox_attempt_outcome`), sem enum PostgreSQL. O `id`
é `uuidv7` e todo tempo é `timestamptz`.

A chave `(tenant_id, idempotency_key)` é única; reenviar a mesma chave com o
mesmo conteúdo devolve o evento original, e com conteúdo diferente falha com
`outbox.ErrIdempotencyConflict`. Um trigger rejeita `DELETE`, congela os campos
de identidade e só aceita `pending -> processing` e
`processing -> processing|processed|pending|failed`; `attempts` avança uma
unidade por claim. `outbox.event_attempt` registra `claimed`, `succeeded`,
`retry` e `failed` com unicidade por tentativa/resultado e também é append-only.

O claim é por tenant e roda sob RLS: um único `UPDATE ... FROM (SELECT ... FOR
UPDATE SKIP LOCKED)` incrementa `attempts`, marca `processing` e grava o lease
em `next_attempt_at`. A única leitura cross-tenant do projeto é
`outbox.pending_tenants(integer)`, função `SECURITY DEFINER` que devolve apenas
`tenant_id` de quem tem evento elegível; ela é executada por
`db.WithSystemTx`, a transação sem tenant reservada ao dispatcher. Nenhum
payload atravessa essa função.

`internal/modules/outbox` expõe `Append`, `GetByID`, `GetByIdempotencyKey`,
`Claim`, `Succeed`, `Fail`, `ReapExpired` e `PendingTenants`, todos com
`context.Context` primeiro e SQL explícito. `Fail` persiste `last_error`
(limitado a 1000 runes), aplica backoff exponencial de 2s a 1h e, ao atingir
`max_attempts`, fecha o evento como `failed`; `ReapExpired` cobre worker que
morreu depois do último claim. O `Dispatcher` recebe um `Handler`, drena cada
tenant em sua própria transação e nunca registra payload em log. Ele não é
ligado ao `cmd/worker` neste slice porque ainda não existe handler de negócio:
marcar eventos como processados sem efeito seria pior que não consumir.

As permissões são explícitas: `amparius_app` recebe `SELECT/INSERT/UPDATE` em
`outbox.event`, `SELECT/INSERT` em `outbox.event_attempt` e `EXECUTE` apenas em
`outbox.pending_tenants`. O papel não tem `SUPERUSER` nem `BYPASSRLS`; o teste
de integração usa `AMPA_TEST_POSTGRES_DSN` com `amparius_app` e
`AMPA_TEST_POSTGRES_ADMIN_DSN` apenas no `t.Cleanup`. Retenção e expurgo de
eventos processados ficam para um slice próprio, com política de auditoria e
LGPD definida.
