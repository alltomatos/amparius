# NFS-e v1.01 - formato estrutural de `cTribNac`

Status: 24/11/2026 - fatia de forma do builder oficial, sem transmissao e sem
consulta a tabela fiscal.

## Fonte e regra aplicada

O XSD oficial v1.01 declara `serv/cServ/cTribNac` como `TSCodTribNac` em
`docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/tiposSimples_v1.01.xsd:1358-1367`:

```xml
<xs:restriction base="xs:string">
  <xs:whiteSpace value="preserve"/>
  <xs:pattern value="[0-9]{6}"/>
</xs:restriction>
```

`BuildDPSOficial` agora falha fechado com `ErrInvalidDPS` quando o valor nao
tem exatamente seis bytes ASCII entre `0` e `9`. O valor aceito e serializado
sem trim ou transformacao.

Esta verificacao valida somente a forma exigida pelo XSD. Ela **nao** confirma
que o codigo existe na lista nacional, esta vigente, pertence ao municipio,
e aplicavel ao servico ou e uma escolha fiscal correta. Nenhuma tabela,
aliquota, CST, NCM, CFOP, RTC ou vigencia foi adicionada.

## Testes

`TestBuildDPSOficialValidaFormatoDeCTribNac` cobre:

- seis digitos, incluindo zero a esquerda;
- comprimentos menor e maior que seis;
- espacos, tab e quebra de linha;
- letra ASCII e digitos Unicode/fullwidth;
- preservacao do valor aceito no XML e validacao contra o XSD local.

Os casos aceitos neutralizam apenas `serie=1`, bloqueio conhecido do facet
literal `TSSerieDPS`; esse bloqueio permanece inalterado e documentado em
`docs/fiscal/2026-11-24-nfse-xsd-serie-parity.md`.
