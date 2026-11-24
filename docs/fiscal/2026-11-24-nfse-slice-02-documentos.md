# DPS oficial v1.01 — choices de documento (CNPJ, CPF, NIF, cNaoNIF)

Status: 24/11/2026 — sem transmissão, sem rede. Complementa a
fatia 01 (`2026-11-23-nfse-slice-01-oficial.md`) sem alterar migrations,
relay ou client.

## O que foi entregue

O `BuildDPSOficial` já serializava o `xs:choice` de documento, mas só conferia
se o tipo estava no conjunto. Agora ele valida, fail-closed com
`ErrInvalidDPS`, o valor contra o facet do próprio tipo, nos três grupos de
pessoa: `prest` (`TCInfoPrestador`), `toma` e `interm` (`TCInfoPessoa`).

| Tipo | Facet oficial (v1.01) | Regra aplicada no builder |
|---|---|---|
| `CNPJ` | `TSCNPJ` `[0-9]{14}` | exatamente 14 dígitos |
| `CPF` | `TSCPF` `[0-9]{11}` | exatamente 11 dígitos |
| `NIF` | `TSNIF` minLength 1, maxLength 40 | 1 a 40 caracteres |
| `cNaoNIF` | `TSCodNaoNIF` enum `0\|1\|2` | somente 0, 1 ou 2 |

Nenhum dígito verificador é calculado ou conferido: o XSD oficial não define
essa regra e o builder não inventa regra fiscal. Presença de documento e nome
continua obrigatória nos grupos `toma`/`interm`.

O `DocumentoTipo` é normalizado (trim das bordas) uma única vez, antes de
validar e serializar, nos três grupos. Assim o tipo aprovado no `xs:choice` é
exatamente o que vira nome de elemento: um tipo como `" CNPJ "` não pode passar
na validação trimada e gerar `< CNPJ >`, que é XML inválido.

## Fixtures sintéticas

Quatro pares JSON/XML em `internal/modules/fiscal/nfse/testdata/official/`,
cada um exercitando o mesmo tipo em `prest`, `toma` e `interm`:

| Fixture | `prest` | `toma` | `interm` |
|---|---|---|---|
| `doc-cnpj` | 99999999999999 | 88888888888888 | 77777777777777 |
| `doc-cpf` | 99999999999 | 88888888888 | 77777777777 |
| `doc-nif` | SINT-NIF-PREST-0001 | SINT-NIF-TOMA-0002 | SINT-NIF-INTERM-0003 |
| `doc-cnaonif` | 2 | 1 | 0 |

Todos os valores são sequências repetidas ou textos marcados com `SINT`; não
há contribuinte, pessoa ou número fiscal real. O `serie` permanece `1`, o valor
sintético semanticamente correto do caso de teste.

## Testes

| Teste | Cobertura |
|---|---|
| `TestBuildDPSOficialChoiceDeDocumento` | 4 tipos × 3 grupos: golden XML, hash, elemento presente uma vez por grupo, XSD só acusa o facet de `serie` |
| `TestBuildDPSOficialChoiceAceitaBordas` | CNPJ 14, CPF 11, NIF 40 e cada código `cNaoNIF` |
| `TestBuildDPSOficialChoiceRejeitaDocumentoInvalido` | 10 valores inválidos × 3 grupos (`ErrInvalidDPS`) |
| `TestBuildDPSOficialChoiceRejeitaDocumentoVazio` | 4 tipos × 3 grupos sem documento |
| `TestBuildDPSOficialChoiceNormalizaTipoComEspacos` | tipo com espaços/tabs/quebras nos 3 grupos: `<CNPJ>` bem-formado e XSD só acusa `serie` |

Os 8 golden estruturais, o builder estrutural e o bloqueio do facet `serie`
permanecem intactos; nenhum teste antigo foi relaxado.

## Paridade Go/libxml2

`scripts/validate-nfse-xsd-parity.ps1` agora percorre todas as fixtures XML
oficiais e exige que a rejeição venha **exclusivamente** do facet `serie`, sem
erros adicionais de choice/documento. Execução local:

```
lxml=6.1.1.0 libxml2=2.11.9
doc-cnaonif.xml: rejeitada por serie/pattern
doc-cnpj.xml:    rejeitada por serie/pattern
doc-cpf.xml:     rejeitada por serie/pattern
doc-nif.xml:     rejeitada por serie/pattern
dps-minima.xml:  rejeitada por serie/pattern
dps-opcionais.xml: rejeitada por serie/pattern
probe serie=^1$: aceita como literal pelo schema
```

Ou seja: o motor XSD real aceita os quatro choices nos três grupos e só
recusa a série — o mesmo comportamento do validador Go.

## Limitações

- O facet de `serie` continua bloqueando a emissão antes do adapter, como
  documentado em `2026-11-24-nfse-xsd-serie-parity.md`.
- `CAEPF`, `IM`, endereço, `tribFed`, `IBSCBS` e demais ramos opcionais não
  foram adicionados.
- A validação de produção segue exigindo motor XSD 1.0 completo (libxml2/Xerces).
