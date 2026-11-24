# Gap — DPS da fatia 01 x XSD oficial NFS-e v1.01

Status: registrado em 23/11/2026 · sem código novo além do validador, sem commit.
Referências: ADR 0010, `docs/fiscal/2026-11-23-nfse-slice-01-fortaleza.md`,
`docs/fiscal/xsd-nfse-v1.01/MANIFEST.md`.

## Resultado honesto

O pacote oficial XSD v1.01 foi integrado ao validador local, sem rede:

- `internal/modules/fiscal/nfse/validator.go` carrega
  `Schemas/1.01/DPS_v1.01.xsd` e resolve recursivamente `xs:include` e
  `xs:import` apenas em disco (`tiposComplexos_v1.01.xsd`,
  `tiposSimples_v1.01.xsd`, `xmldsig-core-schema.xsd`). URLs são recusadas.
- O compilador Go embutido aplica o subconjunto XSD 1.0 usado pelo pacote:
  elementos globais, `complexType`/`sequence`/`choice`, `minOccurs`/`maxOccurs`,
  atributos `use="required"`, `ref` e restrições de `simpleType`
  (`pattern`, `enumeration`, `minLength`/`maxLength`, `base` encadeada).
- Testes provam que o schema **realmente compilou**: `TVerNFSe` rejeita
  `versao="9.99"`, `TSIdDPS` rejeita `Id` fora de `DPS[0-9]{42}`, `TCInfDPS`
  lista os 10 elementos obrigatórios ausentes (`dhEmi`, `verAplic`, `serie`,
  `nDPS`, `dCompet`, `tpEmit`, `cLocEmi`, `prest`, `serv`, `valores`).

**Golden contra o XSD oficial: 0/8 válidos (8 incompatíveis).**
`TestGoldenContraXSDOficial` registra esse número e classifica cada caso com
`nfse.ErrXSDIncompatible` + diagnóstico. Não houve falso verde: o teste falha
se algum golden passar a validar sem revisão do builder/fixtures.

## Por que 0/8 (gap de construção, não bug do validador)

O `BuildDPS` da fatia 01 produz uma **DPS estrutural de trabalho**:

```
<DPS versao="1.01"><idDps>…</idDps><competencia>…</competencia>
<prestador>…</prestador><municipio>…</municipio><serie/><numero/>
<itens><item>…</item></itens><tributos>…</tributos></DPS>
```

O leiaute oficial v1.01 (`TCDPS` em `tiposComplexos_v1.01.xsd`) exige:

```
<DPS versao="1.01"><infDPS Id="DPS[0-9]{42}">
  <tpAmb/><dhEmi/><verAplic/><serie/><nDPS/><dCompet/><tpEmit/><cLocEmi/>
  <prest>…</prest><serv>…</serv><valores>…</valores>
</infDPS></DPS>
```

Diferenças irredutíveis nesta fatia:

1. **Grupo `infDPS` obrigatório** com atributo `Id` no padrão de 45 posições;
   o builder usa `idDps` como elemento solto e `IDDps` arbitrário.
2. **Campos de emissor/serviço/valores oficiais**: `prest` (`TCInfoPrestador`
   com `CNPJ`/`CPF` + `regTrib`), `serv` (`TCServ` com `cTribNac`,
   `xDescServ`, `cLocPrestacao`), `valores` (`TCInfoValores` com `trib` →
   `tribMun`/`totTrib`). O builder usa descrição livre, quantidade, centavos e
   snapshots `tax_calc` em `<tributos>`.
3. **Códigos oficiais e RTC**: `cTribNac`, `cTribMun`, `tribISSQN`,
   `tpRetISSQN`, `IBSCBS` e alíquotas vêm de fonte oficial datada. São
   **gate da fatia 02** (ADR 0010 §6, brief §8). Inseri-los agora seria
   hardcode fiscal — proibido por AGENTS.md §2.1 e pelo brief desta tarefa.
4. **Assinatura XMLDSig**: `TCDPS` admite `ds:Signature` opcional; a fatia 01
   mantém apenas a interface, sem certificado (gate da fatia 02).
5. **`tpAmb`**: ambiente oficial é `1`/`2` (produção/homologação); o slice usa
   `producao_restrita` como domínio de `emission_attempt` (migration 0008).
   Mapeamento é decisão de configuração da fatia 02, não conversão implícita.

## Decisão tomada (sem forçar verde)

- **Não** reescrevemos o builder nem os fixtures para imitar o leiaute oficial
  com valores inventados.
- **Não** marcamos os golden como "validados por XSD".
- O `LocalValidator` continua sendo o validador usado pelo `Service` e pelos
  golden determinísticos (hash/estrutura canônica interna).
- O `XSDValidator` fica disponível pela mesma interface `Validator`, pronto
  para a fatia 02, quando o builder oficial e as fontes fechadas existirem.

## Escopo do compilador XSD (limitação explícita)

O validador é Go puro, sem dependência externa, sem CGO e sem rede — necessário
porque `go.mod` não tem lib XSD e o brief proíbe download. Ele cobre o
subconjunto acima; **não** implementa `xs:group`, `xs:attributeGroup`,
`xs:union`, `xs:list`, `xs:any`, `xs:substitutionGroup`, facets numéricos
(`minInclusive`/`totalDigits`) nem validação de `xs:key`/`xs:keyref`. Para a
fatia 02, a validação de produção deve usar um motor XSD 1.0 completo
(libxml2/Xerces) com o mesmo pacote oficial, mantendo estes testes como
contrato de resolução local de includes/imports.

## Gaps que permanecem (fatia 02)

- Builder oficial `<infDPS>` + `prest`/`serv`/`valores` com fontes datadas.
- Importação de `tax_rule` de Fortaleza (LC 159/2013 art. 245 e alterações),
  `cTribNac`/`cTribMun`, RTC IBS/CBS com oráculo da RFB.
- Certificado ICP-Brasil de teste + assinatura XMLDSig verificável.
- Mapeamento `producao_restrita` → `tpAmb=2`.
- p95 < 100 ms por item medido em benchmark.
- Transmissão real em produção restrita (ainda **zero** nesta fatia).

## Reprodução

```powershell
$env:AMPA_TEST_POSTGRES_DSN="postgres://amparius_app:amparius@127.0.0.1:5432/amparius?sslmode=disable"
$env:AMPA_TEST_POSTGRES_ADMIN_DSN="postgres://amparius:amparius@127.0.0.1:5432/amparius?sslmode=disable"
$env:AMPA_CONTROL_POSTGRES_DSN="postgres://amparius_control_app:amparius@127.0.0.1:5432/amparius_control?sslmode=disable"
go test -count=1 -v ./internal/modules/fiscal/nfse/... -run "TestXSD|TestGolden"
```

Saída esperada: `golden validos contra XSD oficial v1.01: 0/8 (incompativeis: 8)`.
