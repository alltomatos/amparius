# Catálogo operacional v1

A issue [#74](https://github.com/alltomatos/amparius/issues/74) disponibiliza
o catálogo SaaS para a etapa de serviço do mockup [Loja 02](../../mockups/02-atendimento-contrato.svg).
A persistência segue o [PRD §4.1](../prd/PRD.md) e o [DER §1](../data/DER.md)
com as três tabelas de catálogo e os grants já existentes. A fronteira entre módulos consta no
[ADR 0043](../adr/0043-catalogo-operacional-v1.md).

## Rotas

Todas usam Bearer, MFA e os vínculos da sessão conforme o
[ADR 0024](../adr/0024-autenticacao-oidc-mfa-rbac-escopo.md). O catálogo é
compartilhado entre empresas autorizadas do mesmo tenant. O middleware exige
uma empresa válida na sessão. Cabeçalhos de escopo são ignorados e campos de
escopo no JSON são rejeitados. Operadores e auditores têm leitura. Owner,
admin e manager têm leitura e escrita, conforme o RBAC existente.

| Método e caminho | Permissão | Entrada | Sucesso |
| --- | --- | --- | --- |
| POST /v1/catalog/items | catalog:write | kind igual a service, name, unit e active opcional (padrão true) | 201 com o item |
| POST /v1/catalog/price-lists | catalog:write | name e valid_from | 201 com a tabela |
| POST /v1/catalog/prices | catalog:write | item_id, list_id, amount_cents, valid_from e valid_to opcional | 201 com o preço |
| GET /v1/catalog/offers?item_id=…&list_id=…&on=AAAA-MM-DD | catalog:read | Os três parâmetros, cada um exatamente uma vez | 200 com item, price_list e price |

O corpo dos POSTs é um objeto JSON de até 16 KiB, com campos conhecidos,
nomes aparados de até 200 caracteres e IDs UUID não nulos. unit usa o domínio
ativo do catálogo. amount_cents aceita inteiro JSON de 0 a 999999999999999999
(conforme NUMERIC(18,2)). Para consumir o máximo sem arredondar, o cliente
deve usar um leitor JSON com suporte a inteiros de 64 bits. Cada POST
retorna o registro diretamente no corpo com seu id. O preço retornado
tem id, item_id, list_id, amount_cents, valid_from e valid_to,
com null para ausência de data final. A opção de gravar preço para um item
já existente continua disponível. Só serviços ativos e sem exclusão lógica
podem compor uma oferta.

## Vigência e resolução

As datas são civis em formato AAAA-MM-DD, com ano entre 0001 e 9999, sem
hora nem conversão de fuso. Os POSTs normalizam as datas no formato canônico. A data de referência on é explícita e exige esse formato. Uma tabela
passa a valer em valid_from. O preço vale desde valid_from até valid_to,
ambos inclusive, ou por prazo aberto quando valid_to é nulo.
Um período de um dia é válido. O intervalo efetivo começa no maior dos dois
inícios. A seleção exige item e tabela explícitos no tenant da sessão.
Se mais de um preço for elegível para o par na data, a consulta responde
409 OFFER_AMBIGUOUS. A gravação conserva o modelo atual, que permite
sobreposição. A consulta recusa essa situação inclusive para dados legados.

A ausência de oferta, a inelegibilidade e referências de outro tenant
resultam em 404 OFFER_NOT_FOUND. Na criação de preço, item ou tabela
inexistentes ou de outro tenant retornam 404 CATALOG_REFERENCE_NOT_FOUND.
A API responde 400 VALIDATION_ERROR para entradas inválidas, 401 para
credencial ausente ou inválida, 403 para MFA, escopo ou permissão insuficientes
e 503 quando a autenticação ou o catálogo estiver indisponível. Respostas
protegidas usam Cache-Control: no-store.

## Uso pelo contrato e limites

O módulo de catálogo expõe Repository.ResolveServiceOffer com contexto do
tenant, item, tabela e data de referência. A operação busca o valor persistido
e retorna os três registros na mesma consulta sob RLS. O comprador não
fornece o valor unitário a esse resolvedor. A
[#76](https://github.com/alltomatos/amparius/issues/76) precisa conectar o
contrato autenticado ao resolvedor, definir a data comercial no servidor e
persistir o ID do preço e o valor em centavos no snapshot para reutilização
no replay. O aceite registrado na #76 exige recusar oferta vencida, ausente,
ambígua ou de outro tenant e provar que o valor enviado pelo cliente não
substitui o preço persistido. O contrato
legado de atendimento permanece separado dessa resolução.

A fatia é online e consulta por IDs conhecidos. Descoberta e seleção no
cliente operacional seguem na [#75](https://github.com/alltomatos/amparius/issues/75).
 Descoberta de serviços e
tabelas, UI da Loja 02, catálogo offline, assinatura, cobrança e emissão
fiscal ficam fora desta entrega. Com o bootstrap atual da API sem provedor
OIDC/MFA, as rotas registradas falham fechado com 503. Os testes exercitam
OIDC/MFA sintéticos e PostgreSQL real.

## Verificação

Com PostgreSQL descartável, crie o papel de login amparius_app com
NOSUPERUSER/NOBYPASSRLS antes de aplicar as migrações. Configure
AMPA_TEST_POSTGRES_ADMIN_DSN para o administrador desse banco e
AMPA_TEST_POSTGRES_DSN para o papel de aplicação.

```powershell
goose -dir migrations validate
goose -dir migrations postgres $env:AMPA_TEST_POSTGRES_ADMIN_DSN up-to 35
go test ./internal/modules/catalog -count=1 -run 'TestOperationalCatalogPostgres|TestDevelopmentCatalogHTTPIntegration' -v
go test ./cmd/api -count=1 -run 'TestBuildHandlerSobreviveCelulaIndisponivel'
```

O teste operacional verifica RLS ativo nas três tabelas e remove as fixtures
ao terminar. O teste do binário cobre a resposta 503 sem autenticador real.

A validação independente reportada para a #74 usou PostgreSQL 16.16
descartável, Goose até 0035 e `amparius_app` com `rolsuper=f` e
`rolbypassrls=f`.

```powershell
go test -p 1 ./cmd/api ./internal/modules/catalog -run 'TestBuildHandlerSobrevive|TestOperationalCatalogPostgres|TestDevelopmentCatalogHTTPIntegration' -count=1 -v
```

Passaram seis cenários operacionais e as verificações dev e de bootstrap.
O pacote catálogo levou 2,001 s. As quatro contagens de limpeza retornaram
zero e o contêiner e o volume foram removidos. Também passaram `goose validate`,
`go test ./... -count=1` sem DSNs, `go vet ./...`, `go build ./...` e
`git diff --check`. A evidência cobre backend com autenticação sintética,
sem execução em produção ou na interface.
