# NFS-e slice 04 — contrato opcional de `cTribMun`

Status: implementado em 24/11/2026, sem transmissão real e sem código fiscal
municipal oficial.

## Entrega

`OfficialServico` agora expõe:

```go
CTribMun *string `json:"c_trib_mun,omitempty"`
```

O builder oficial serializa o elemento somente quando o ponteiro está presente,
na ordem do XSD: `cTribNac`, `cTribMun`, `xDescServ`. Ponteiro nulo mantém a saída
anterior, incluindo os bytes e o SHA-256 dos golden existentes.

## Validação

Quando presente, o valor é aceito somente se tiver exatamente três dígitos ASCII
(`0`–`9`). O valor não é aparado, preenchido, normalizado ou inferido. Ponteiro
presente com string vazia, whitespace, letras, tamanho diferente ou dígitos
Unicode falha fechado com `ErrInvalidDPS`.

Essa é apenas validação estrutural do XSD v1.01 (`TCCodTribMun`). Ela não prova
existência, vigência, pertencimento ao município, aplicabilidade, alíquota,
retenção ou qualquer outra regra fiscal. A resolução continua fora do builder,
em tabelas oficiais datadas e no caso de uso fiscal.

## Evidência sintética

`internal/modules/fiscal/nfse/testdata/official/dps-ctribmun-sintetico.json`
usa `123` exclusivamente como valor SINTÉTICO e NÃO OFICIAL para exercitar o
contrato. O valor não representa código municipal de Fortaleza nem de qualquer
outro município. Nenhum código `001` ou `003` foi usado.

Os testes cobrem presença, ausência, ordem, limites, whitespace, letras e
dígitos Unicode. O teste do golden existente cobre bytes e SHA-256 quando o
campo está ausente; a fixture sintética também é validada pelo validador XSD
local. O caminho público `Service.Emit` também prova, com PostgreSQL real
quando os DSNs de integração estão disponíveis, que um valor inválido falha
antes do adapter, da persistência e da contingência.

O script de paridade com lxml/libxml2 continua cobrindo as fixtures XML
versionadas e o bloqueio independente de `serie`; a fixture desta fatia é JSON
e não é incluída nesse script.

## Limites que permanecem

- O bloqueio independente de `serie=1` no pacote XSD v1.01 permanece documentado.
- A validação de produção exige motor XSD 1.0 completo e fontes fiscais oficiais.
- Não houve alteração de DER, migrations, tabelas fiscais, alíquotas,
  existência/vigência/aplicabilidade ou transmissão.
