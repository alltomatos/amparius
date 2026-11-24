# NFS-e v1.01 — formato estrutural de `cTribMun` (bloqueio resolvido)

Status: 24/11/2026 — análise estrutural concluída no contrato Go, sem
transmissão, consulta a tabela fiscal ou inclusão de código municipal oficial.

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

## Bloqueio do builder — resolvido

`OfficialServico` agora possui `CTribMun *string` opcional e
`BuildDPSOficial` serializa `<cTribMun>` somente quando o ponteiro está presente,
entre `<cTribNac>` e `<xDescServ>`. O valor é validado fail-closed como exatamente
três dígitos ASCII, sem trim, padding, normalização ou inferência.

O teste positivo usa `123` em fixture SINTÉTICA e NÃO OFICIAL. A validade fiscal
continua sendo resolvida por fonte/tabela datada fora do builder; `123` não é
afirmado como código de Fortaleza ou de qualquer município.

Os testes negativos cobrem ponteiro presente vazio, tamanho incorreto, bordas
com whitespace, tabs, quebra de linha, letras e dígitos Unicode. O caminho
público também bloqueia a entrada antes de adapter, persistência e contingência
quando a integração PostgreSQL está disponível.

O bloqueio conhecido de `serie=1` permanece inalterado e continua coberto por
`scripts/validate-nfse-xsd-parity.ps1`. O detalhamento da implementação está em
`docs/fiscal/2026-11-24-nfse-slice-04-ctribmun.md`.
