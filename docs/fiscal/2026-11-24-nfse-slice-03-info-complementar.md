# NFS-e v1.01 — facets textuais de `infoCompl`

Status: 24/11/2026 — sem transmissão e sem rede. Esta fatia continua o
builder oficial da DPS v1.01 e preserva o bloqueio conhecido do facet de
`serie`.

## Regra aplicada

`BuildDPSOficial` agora falha fechado com `ErrInvalidDPS` antes de serializar
valores incompatíveis com os tipos simples oficiais:

| Campo | Tipo XSD | Limite em runas | Regra de texto |
|---|---|---:|---|
| `serv/infoCompl/idDocTec` | `TSDRT` | 1..40 | `xs:string` com `whiteSpace=preserve`; sem pattern |
| `serv/infoCompl/docRef` | `TSDesc255` | 1..255 | `TSString`; sem borda whitespace, tab ou quebra de linha |
| `serv/infoCompl/xInfComp` | `TSDescInfCompl` | 1..2000 | `TSString`; whitespace interno preservado, quebra de linha rejeitada |
| `serv/infoCompl/xPed` | `TSNumeroEndereco` | 1..60 | `TSString`; validado porque já é serializado pelo builder |

A validação conta runas (`len([]rune(...))`), não bytes. `idDocTec` usa
diretamente `xs:string` com `whiteSpace=preserve`: espaços nas bordas, tabs e
quebras de linha são aceitos e preservados, e somente vazio ou mais de 40
runas é rejeitado. Para `TSString`, aplicado aos demais campos indicados na
tabela, o primeiro e o último caractere precisam estar no intervalo `!`..`ÿ`;
somente espaço..`ÿ` é permitido no interior. O builder não faz trim dos
valores válidos: dois espaços internos permanecem no XML. Campos opcionais
compostos apenas por whitespace continuam omitidos pelo `elemIf`; se todos os
campos de `infoCompl` forem assim, o grupo é rejeitado como vazio.

`xDescServ` permanece no tipo `TSDesc2000`, que permite quebra de linha e segue
validado separadamente. Nenhuma regra de RTC, alíquota ou `cTribNac` foi
introduzida.

## Testes

`TestBuildDPSOficialValidaFacetsTextuaisDeInfoCompl` cobre:

- mínimos e máximos em runas para os quatro campos modelados;
- excesso de comprimento;
- bordas com whitespace, tab e quebra de linha em `idDocTec`;
- preservação de dois espaços internos em `xInfComp`;
- validação XSD local dos casos aceitos, neutralizando somente `serie=1` para
  manter explícito o gap já documentado.

O teste existente de `infoCompl` sem campos permanece cobrindo a regra de
grupo não vazio.

## Limites desta fatia

- `serie=1` continua incompatível com o facet literal do XSD v1.01 e não foi
  alterado.
- O validador XSD local continua sendo um subconjunto para desenvolvimento;
  a validação de produção exige um motor XSD 1.0 completo.
- Não foram adicionados códigos fiscais, alíquotas, RTC, transmissão ou
  credenciais.
