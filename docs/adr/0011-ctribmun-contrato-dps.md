# ADR 0011 — `cTribMun` opcional no contrato da DPS oficial

Status: proposto · 24/11/2026 · referencia os ADRs 0002, 0004, 0006 e 0010, o DER v0.3 e o builder `internal/modules/fiscal/nfse/official.go`

## Contexto

O XSD oficial NFS-e v1.01 declara `serv/cServ/cTribMun` como elemento opcional
(`minOccurs="0"`) no tipo `TCCServ`, usando o tipo simples `TCCodTribMun`:

- `docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/tiposComplexos_v1.01.xsd:1339-1343`
- `docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/tiposSimples_v1.01.xsd:568-576`

O tipo declara `whiteSpace="preserve"` e o pattern `[0-9]{3}`. Não há
`minLength` ou `maxLength` explícitos. Essa evidência define somente a forma
estrutural do valor no XML; não prova existência, vigência, pertencimento ao
município, aplicabilidade, alíquota ou qualquer outra regra fiscal.

O contrato atual do builder oficial não possui o campo:

- `OfficialServico` está em `internal/modules/fiscal/nfse/official.go:56-62`;
- `BuildDPSOficial` serializa `cTribNac` e `xDescServ`, mas não serializa
  `cTribMun`, em `internal/modules/fiscal/nfse/official.go:150-164`;
- a validação atual de serviço cobre os campos obrigatórios e a forma de
  `cTribNac`, em `internal/modules/fiscal/nfse/official.go:201-253`.

Há três tensões que precisam ficar explícitas antes de qualquer implementação:

1. O ADR 0010 define que o adapter não calcula tributo, que o builder recebe
   valores já resolvidos por `tax_calc`/`fiscal_config` e que `cTribMun` é gate
   da fatia 02. Modelar o campo não pode fechar esse gate por conta própria.
2. O DER registra `TAX_RULE.service_code` e `TAX_RULE.municipality_code`, mas
   não define uma propriedade chamada `cTribMun` nem o contrato de sua
   resolução (`docs/data/DER.md:219-246`). Adicionar um campo ao builder não
   autoriza presumir que uma dessas colunas seja sua origem.
3. O ADR 0004 proíbe estimativa fiscal e exige tabelas datadas; os ADRs 0002,
   0006 e 0010 exigem que a operação offline seja explícita e que NFS-e
   nacional não seja autorizada offline.

## Decisão

### 1. Representação opcional no contrato Go

Quando a implementação for autorizada, o contrato proposto para
`OfficialServico` é:

```go
CTribMun *string `json:"c_trib_mun,omitempty"`
```

O ponteiro distingue ausência legítima de presença explícita com valor vazio:

- `nil` significa que o elemento opcional não será emitido;
- ponteiro não nulo exige validação estrutural antes da serialização;
- ponteiro não nulo e vazio falha fechado; não é convertido em ausência.

A ordem XML deve permanecer a ordem do XSD: `cTribNac`, `cTribMun`,
`xDescServ`.

### 2. Limite do builder

O builder deve validar somente a forma declarada pelo XSD para um valor
presente: `[0-9]{3}` com dígitos ASCII. O valor aceito deve ser preservado como
recebido. Não se deve:

- aplicar `trim`, padding, substituição ou normalização Unicode;
- aceitar dígitos Unicode como equivalentes aos dígitos ASCII;
- criar valor default, inclusive `000`;
- derivar o valor de `cTribNac`, `cLocPrestacao`, município, serviço ou
  alíquota;
- consultar rede, tabela fiscal ou fonte municipal dentro do builder.

Whitespace nas bordas, tabs, quebras de linha e outros valores que não casem
com o pattern devem falhar com `ErrInvalidDPS`. A validação do builder é de
forma, não de semântica fiscal.

### 3. Responsabilidade da resolução fiscal

Existência, vigência, município aplicável, vínculo ao serviço, alíquota,
retenção e demais regras continuam pertencendo ao resolvedor fiscal e às
tabelas oficiais datadas. Se uma regra oficial futura exigir `cTribMun` para
uma situação concreta, a ausência deve ser rejeitada pelo caso de uso/resolvedor
com erro fiscal apropriado, antes da emissão; o builder não deve inventar essa
obrigatoriedade a partir de `minOccurs="0"`.

O desenho da origem persistida de `cTribMun`, inclusive eventual alteração de
`TAX_RULE`, exige decisão própria do DER e migração forward-only. Esta ADR não
escolhe coluna, tabela, município, código ou fonte legal.

## Consequências

- O contrato de entrada do builder ganha uma extensão opcional sem alterar a
  saída das entradas que não informam o campo.
- Fixtures e golden existentes continuam válidos como contratos atuais e não
  devem receber um código municipal inventado apenas para exercitar o campo.
- Uma futura fixture de forma poderá usar valor sintético claramente marcado
  como não oficial, sem afirmar existência ou vigência fiscal.
- O serviço deve continuar falhando antes do adapter quando o valor presente
  for estruturalmente inválido; isso não transforma a DPS em `pending` ou
  `authorized`.
- O ADR 0010 permanece responsável pela decisão de protocolo, contingência,
  fontes fiscais e transmissão. Esta ADR só detalha a fronteira do contrato
  do builder.
- O DER, o PRD e as tabelas fiscais não são alterados por esta ADR.

## Não-escopo e bloqueios

Ficam fora desta decisão:

- alteração de Go, Rust, migrations, DER, PRD ou tabelas fiscais;
- inclusão de código municipal real ou sintético em fixture oficial existente;
- prova de existência, vigência, município, alíquota, retenção ou aplicabilidade;
- definição de `cTribMun` como obrigatório para qualquer município ou serviço;
- importação de `tax_rule`, transmissão, certificado ou credencial;
- resolução do bloqueio conhecido de `serie=1` no XSD v1.01;
- declaração de conformidade oficial completa ou de prontidão para produção.

Os gates para implementação futura são:

1. contrato de resolução fiscal definido, com fonte oficial datada e metadados
   de origem, sem regra literal no código;
2. decisão do Engenheiro de Dados sobre a representação persistida, caso a
   resolução exija novo campo ou tabela;
3. testes Go e XSD real cobrindo presença/ausência, pattern, preservação,
   Unicode e rejeição fail-closed;
4. prova de que o caminho inválido bloqueia antes do adapter, persistência e
   qualquer estado de autorização;
5. revisão das fixtures e golden sem promover dados sintéticos a dados fiscais;
6. manutenção dos gates de assinatura, motor XSD completo, fontes municipais,
   certificado e transmissão já registrados no ADR 0010.

## Critérios de aceite

1. Com `CTribMun == nil`, o XML não contém `<cTribMun>` e os golden existentes
   permanecem byte-a-byte e hash-a-hash inalterados.
2. Com um valor sintético de forma, explicitamente não oficial, o XML contém
   exatamente um `<cTribMun>` entre `cTribNac` e `xDescServ`.
3. Valores com menos ou mais de três dígitos, whitespace, tabs, quebras de
   linha, letras e dígitos Unicode falham com `ErrInvalidDPS`; nenhum é
   corrigido automaticamente.
4. Ponteiro presente com string vazia falha; ponteiro nulo omite o elemento.
5. A mesma entrada produz XML e SHA-256 determinísticos.
6. O teste de serviço comprova que a rejeição ocorre antes de chamada ao
   adapter, persistência de tentativa ou transição para `pending`/`authorized`.
7. A documentação do slice registra a forma XSD e o limite semântico do
   builder, sem código, alíquota ou regra fiscal inventada.

## Alternativas rejeitadas

- **`string` com `omitempty`**: não distingue campo ausente de campo presente
  vazio e enfraquece o fail-closed do contrato.
- **Valor default ou derivação automática**: inventaria código municipal e
  violaria os ADRs 0004 e 0010.
- **Validação de existência no builder**: mistura transporte com resolução
  fiscal, além de não haver fonte oficial fechada nesta decisão.
- **Alterar o DER agora**: a origem persistida ainda não foi decidida; a
  mudança deve ser consequência de ADR de dados e migração forward-only, se
  necessária.

## Mudanças decorrentes

Nenhuma mudança de código, DER, PRD, migration, fixture ou tabela fiscal é
autorizada por esta ADR. A implementação futura deverá atualizar, no mesmo
slice, o contrato Go, os testes, a documentação do builder e os critérios de
aceite correspondentes, mantendo os gates fiscais do ADR 0010.
