# NFS-e v1.01 — formato estrutural de `cTribMun`

Status: 24/11/2026 — análise estrutural bloqueada no contrato Go, sem
transmissão, consulta a tabela fiscal ou inclusão de código municipal.

## Regra comprovada no XSD

O XSD oficial v1.01 declara `serv/cServ/cTribMun` como elemento opcional
(`minOccurs="0"`) e associa-o ao tipo `TCCodTribMun` em
`docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/tiposComplexos_v1.01.xsd:1339-1343`.

O tipo simples está em
`docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/tiposSimples_v1.01.xsd:568-576` e
declara somente:

```xml
<xs:restriction base="xs:string">
  <xs:whiteSpace value="preserve"/>
  <xs:pattern value="[0-9]{3}"/>
</xs:restriction>
```

Não há `minLength` ou `maxLength` explícitos nesse tipo. A análise confirma a
forma declarada pelo schema, mas não confirma existência, vigência,
pertencimento ao município, aplicabilidade, alíquota, RTC ou qualquer outra
validade fiscal.

## Bloqueio do builder

`OfficialServico` não possui `CTribMun` e `BuildDPSOficial` não serializa
`<cTribMun>`. Portanto esta fatia não adiciona validação, normalização,
valor sintético ou teste do campo: fazer isso agora inventaria um contrato
que não existe no builder.

Quando o campo for modelado no contrato correto, a próxima alteração deverá
validar somente a forma comprovada acima, preservar whitespace e cobrir
bordas, whitespace e Unicode com fixtures sintéticas. A validade fiscal deverá
continuar sendo resolvida por fonte/tabela datada fora do builder.

O bloqueio conhecido de `serie=1` permanece inalterado e continua coberto por
`scripts/validate-nfse-xsd-parity.ps1`.
