# Builder DPS oficial v1.01 — fatia NFS-e 01 (adicional)

Status: 23/11/2026 · sem transmissão, sem rede, sem alíquota/segredo, sem commit.
Referências: ADR 0010, `docs/fiscal/2026-11-23-nfse-slice-01-xsd-gap.md`,
`docs/fiscal/xsd-nfse-v1.01/MANIFEST.md`, `Schemas/1.01/DPS_v1.01.xsd`.

## O que foi entregue

`BuildDPSOficial` (`internal/modules/fiscal/nfse/official.go`) é uma **API
separada** do `BuildDPS` estrutural. Ela produz XML no leiaute oficial
`TCDPS/TCInfDPS`:

```
<DPS versao="1.01">
  <infDPS Id="DPS[0-9]{42}">
    tpAmb, dhEmi, verAplic, serie, nDPS, dCompet, tpEmit, cLocEmi
    <prest>  CNPJ|CPF|NIF|cNaoNIF + regTrib(opSimpNac, regEspTrib)  </prest>
    <serv>   locPrest(cLocPrestacao) + cServ(cTribNac, xDescServ)     </serv>
    <valores> vServPrest(vServ) + trib(tribMun(tribISSQN, tpRetISSQN),
                                      totTrib(indTotTrib))            </valores>
  </infDPS>
</DPS>
```

Todos os códigos, regimes, documentos, datas e valores monetários são **campos
de entrada** (`OfficialDPSInput`), já resolvidos por `tax_calc`/`fiscal_config`
e pela sessão autenticada em produção. O builder só serializa, valida presença
(fail-closed com `ErrInvalidDPS`) e calcula SHA-256. **Não há constante de
alíquota, NCM, CST, CFOP, código de serviço, CNPJ ou valor monetário no código.**
`pAliq` e demais campos opcionais de tributo não são preenchidos: entrariam por
novos campos de entrada quando a fonte oficial datada existir (gate fatia 02).

## Fixture mínima documental

`internal/modules/fiscal/nfse/testdata/official/dps-minima.json` (entrada) e
`dps-minima.xml` (golden do XML) usam **dados sintéticos de teste**, sem PII
real e sem dado fiscal oficial: `CNPJ` de teste, `cTribNac` `000000`,
`vServ` `1.00`, `indTotTrib` `0`, `tpAmb` `2`. O comentário `_comment` no JSON
registra essa natureza. São valores de *forma*, não de *regra*.

## Validador XSD — correção incluída

Para validar a DPS oficial, `validator.go` passou a tratar corretamente
`xs:choice` aninhado em `xs:sequence` (casos `prest`, `locPrest`, `totTrib`)
e a casar grupos de alternativas com `minOccurs`/`maxOccurs`. Antes, as
alternativas eram tratadas como partículas sequenciais isoladas e a DPS oficial
falhava por erro do compilador, não por erro de XML. Os testes antigos de
detecção de estrutura inválida continuam verdes.

## Resultado dos testes

| Caso | Resultado |
|---|---|
| `TestGoldenOficialContraXSD` | **1/1 caso oficial passa** no XSD v1.01 (fixture mínima) |
| `TestBuildDPSOficialValidaContraXSD` | positivo, com `infDPS@Id` e namespace |
| `TestBuildDPSOficialDeterministico` | mesmo XML + mesmo hash |
| `TestBuildDPSOficialRejeitaEntradaIncompleta` | 22 negativos (21 campos obrigatórios + contexto cancelado) |
| `TestDPSOficialNegativosDeXSD` | 8 mutações do XML oficial rejeitadas (versao, Id, infDPS, tpAmb, CNPJ, elemento extra, vServ, namespace) |
| `TestServicoComDPSOficialMantemFluxo` | integração real: XSDValidator + FakeAdapter + 201 + persistência 0008 + replay sem duplicar + log sem PII |
| `TestServicoComDPSOficialTimeoutPendente` | timeout → `pending`, nunca `authorized` |
| `TestBuildDPSOficialNaoMascaraBuilderEstrutural` | 8 golden antigos **continuam** incompatíveis (0/8) |
| `TestGoldenContraXSDOficial` | 0/8 mantido, com `ErrXSDIncompatible` |

**Casos oficiais que passam: 1/1.** Golden estruturais antigos: **0/8**, sem
mascaramento — `ServiceInput.Official` seleciona o caminho novo; sem ele, o
`Service` continua usando `BuildDPS` + `LocalValidator` exatamente como antes.

## Integração com o Service

`ServiceInput` ganhou o campo opcional `Official *OfficialDPSInput`. Quando
presente, `Emit` usa `BuildDPSOficial` e o `Validator` injetado (o teste usa
`XSDValidator`). `NewService` não mudou de assinatura; `validateServiceInput`
dispensa `DPS.IDDps`/`DPS.Items` apenas nesse caminho. Fake adapter,
idempotência por `(tenant, idempotency_key)`/`idDps`, `GetDPSStatus` antes de
reenviar, contingência pendente e persistência em `emission_attempt` (colunas
da migration 0008) permanecem intactos. Nenhuma migration foi alterada.

## Limitações e gaps (fatia 02)

- O compilador XSD é Go puro e cobre subconjunto XSD 1.0 (sem `xs:group`,
  `union`, facets numéricos, `key/keyref`). Validação de produção deve usar
  motor XSD 1.0 completo (libxml2/Xerces) com o mesmo pacote oficial.
- A fixture é **mínima**: não cobre `toma`, `interm`, `IBSCBS`, `pAliq`,
  `tribFed`, `comExt`, `obra`, `atvEvento`, `infoCompl`, `vDescCondIncond`,
  `vDedRed`, `subst`, `ds:Signature`. Cada um entra por campo de entrada
  quando a fonte oficial datada existir.
- Fontes de Fortaleza (LC 159/2013 art. 245 e alterações), `cTribNac`
  real, `cTribMun`, RTC IBS/CBS com oráculo da RFB e certificado ICP-Brasil
  continuam sendo gates da fatia 02 (ADR 0010 §6). Nada foi importado.
- Mapeamento `producao_restrita` → `tpAmb=2` é decisão de config futura;
  o builder recebe `tp_amb` como entrada e não converte.
- p95 < 100 ms e transmissão real em produção restrita: fora desta fatia.

## Reprodução

```powershell
$env:AMPA_TEST_POSTGRES_DSN="postgres://amparius_app:amparius@127.0.0.1:5432/amparius?sslmode=disable"
$env:AMPA_TEST_POSTGRES_ADMIN_DSN="postgres://amparius:amparius@127.0.0.1:5432/amparius?sslmode=disable"
$env:AMPA_CONTROL_POSTGRES_DSN="postgres://amparius_control_app:amparius@127.0.0.1:5432/amparius_control?sslmode=disable"
go test -count=1 -v ./internal/modules/fiscal/nfse/... -run "TestGoldenOficial|TestBuildDPSOficial|TestDPSOficial|TestServicoComDPSOficial"
```

Saída esperada: `casos oficiais que passam no XSD v1.01: 1/1` e
`golden validos contra XSD oficial v1.01: 0/8 (incompativeis: 8)`.
