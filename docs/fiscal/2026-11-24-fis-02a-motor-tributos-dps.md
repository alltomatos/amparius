# FIS-02A — motor de tributos e DPS oficial

Data: 2026-11-24
Issue: #25 (parent #5 MVP-FIS-02)
Estado: **PARCIAL**. O motor tributário e a migration 0022 têm evidência
(PASS), mas a DPS oficial v1.01 permanece **BLOQUEADA** pelo facet
`TSSerieDPS` e os goldens do motor são sintéticos. A issue #25 **não** está
concluída. Sem commit/push nesta etapa.

## Objetivo

Calcular tributos por competência a partir de `fiscal.tax_rule` datada, falhar
fechado quando a regra não existe e manter a DPS oficial v1.01 como camada de
transporte já existente. Nenhuma alíquota, código ou vigência oficial foi
inventada ou semeada.

## Status da issue #25

| Item | Status | Evidência |
| --- | --- | --- |
| Motor de tributos datado | **PASS** | `tax_engine.go`, unitários, goldens sintéticos e integração PostgreSQL real |
| Migration 0022 / hash | **PASS** | `calculation_hash` persistido e conferido |
| Cobertura da issue #25 | **PARCIAL** | motor entregue; DPS oficial não concluída; sem `tax_rule` oficial |
| DPS oficial v1.01 | **BLOQUEADA** | facet `TSSerieDPS` rejeita séries válidas; 6/6 fixtures rejeitadas |

A issue #25 **não** deve ser declarada concluída nesta etapa.

## Entregue

### 1. Motor (`internal/modules/fiscal/tax_engine.go`)

`fiscal.TaxEngine` recebe uma porta `TaxRuleResolver` e oferece:

```go
func NewTaxEngine(resolver TaxRuleResolver) (*TaxEngine, error)
func (e *TaxEngine) ComputeItem(ctx context.Context, competence time.Time, requests []TaxItemRequest) (TaxComputation, error)
func (c TaxComputation) ToCalculations(tenantID, itemID uuid.UUID) []TaxCalculation
func FormatBRL(cents int64) string
```

- `TaxItemRequest`: critérios de regra + base do item em centavos + RBT12 em
  centavos (obrigatório só para faixa do Simples).
- `TaxLine`: tipo, base, alíquota efetiva, valor, `rule_id`,
  `rule_source_hash`, `rule_version`, snapshot JSON e hash SHA-256.
- `TaxComputation`: linhas em ordem canônica + hash agregado.

### 2. Consulta datada e fail-closed

- `*fiscal.Checker` é o resolvedor de produção: RLS por tenant, precedência
  tenant > global, intervalo half-open por competência.
- `TaxRuleCriteria.RBT12ActualCents` foi adicionado ao `Checker`: quando
  definido, restringe `faixa_simples` à faixa `[rbt12_from, rbt12_to)` que
  contém a receita. Regras sem faixa continuam elegíveis.
- Regra ausente → `ErrTaxRuleNotFound` embrulhado em `ErrTaxRuleRequired`.
- Regra ambígua ou banco indisponível → propagados como erro.
- Nenhuma linha parcial: a operação é all-or-nothing por item.
- ISS sem município explícito falha antes de consultar o banco.
- `por_unidade` falha com `ErrTaxBasisUnsupported`.

### 3. Fórmulas

| Base | Cálculo |
| --- | --- |
| `percentual` | base reduzida × alíquota / 100 |
| `faixa_simples` | alíquota efetiva = (RBT12 × nominal − dedução) / RBT12; valor = base reduzida × efetiva |
| `isencao` | valor 0, sem alíquota |

A redução de base é aplicada antes do tributo. O arredondamento é half-up para
o centavo. Alíquota efetiva é persistida com 6 casas; o snapshot guarda também
a alíquota nominal, a dedução e o percentual de redução.

### 4. Hash e persistência

- Snapshot canônico em JSON (`calculation_input`) e SHA-256
  (`calculation_hash`).
- Mesma entrada + mesma regra → mesmo hash; base diferente muda o hash.
- Migration forward-only `0022_tax_calc_hash.sql` adiciona
  `fiscal.tax_calc.calculation_hash char(64)` com CHECK de hexadecimal
  minúsculo. A migration 0005 não foi editada.
- `fiscal.TaxCalculation` e o repositório passaram a ler/escrever a nova coluna;
  `tax_calc` continua append-only.

### 5. DPS oficial v1.01 — BLOQUEADA

A DPS continua usando `nfse.BuildDPSOficial`, já entregue nas fatias
anteriores. Esta fatia **não** alterou o builder, o XSD nem os goldens
oficiais. A geração produz o XML canônico, mas a validação contra o XSD oficial
v1.01 falha por um defeito do próprio schema versionado:

- `docs/fiscal/xsd-nfse-v1.01/Schemas/1.01/tiposSimples_v1.01.xsd:158-164`
  declara `TSSerieDPS` com `pattern value="^0{0,4}\d{1,5}$"`;
- em XML Schema, `^` e `$` são **literais**, não âncoras;
- por isso qualquer série semanticamente válida (`1`, `00001`) é rejeitada
  pelo facet, e apenas o literal `^1$` passaria — o que não é uma série válida.

Reprodução estrita, que sai com código **1**:

```powershell
.\scripts\validate-nfse-dps-official.ps1
```

Saída atual (lxml 6.1.3 / libxml2 2.11.9):

```
REJEITADA  internal\modules\fiscal\nfse\testdata\official\dps-minima.xml
  linha 1: Element '{http://www.sped.fazenda.gov.br/nfse}serie':
  [facet 'pattern'] The value '1' is not accepted by the pattern '^0{0,4}\d{1,5}$'.
...
rejeitadas=6/6
```

O teste `TestGoldenOficialContraXSD` **passa** apenas porque afirma o bloqueio
conhecido (`0/1`); ele não é evidência de aceitação oficial. O script
`validate-nfse-xsd-parity.ps1` também passa por exigir a rejeição. Nenhum dos
dois autoriza declarar a DPS concluída.

Não foi usado o XSD 1.00 (que não tem esse `pattern`): o ADR 0010 exige v1.01,
e trocar de versão para fugir do facet seria mudar o contrato oficial, não
corrigi-lo. Da mesma forma, o builder não foi alterado: não existe fonte
oficial no repositório que autorize outra forma para `serie`.

Artefatos/fontes oficiais necessários para desbloquear:

1. **Correção ou errata oficial do XSD v1.01** para `TSSerieDPS`, ou Nota
   Técnica da SEFIN que confirme o facet e o valor aceito. É o gate primário.
2. **Anexo I — DPS/NFS-e v1.01** oficial, confirmando a semântica de `serie`.
3. **Manual de API/Swagger do Emissor Nacional** (produção restrita), para a
   transmissão futura.
4. **Fontes tributárias datadas**: LC 214/2025 + NT SE/CGNFS-e 007/2026 (RTC);
   LC 159/2013 art. 245 e alterações (ISS Fortaleza); Anexo B NBS2; Anexo C
   IndOp; planilha nacional de alíquotas como evidência de ausência.
5. **Certificado ICP-Brasil A1/A3** e procuração eletrônica para emissão real.

O motor fornece valores em centavos e `FormatBRL` produz o formato monetário do
XSD (`1234.56`). `TestDPSOficialAceitaValorMonetarioDoMotor` usa uma regra
sintética de 5%, calcula o valor pelo motor, injeta `vServ` na DPS e confirma
que o XSD local só acusa o gap de `serie`. O mapeamento para `pAliq`/ISSQN da
DPS não foi aberto nesta fatia (ver pendências). Corrigir o builder para
mascarar o facet seria inventar regra; não foi feito.

## Testes

### Unitários (sem banco)

`go test ./internal/modules/fiscal -run 'TestTaxEngine|TestFormatBRL' -count=1`

Cobrem: regra ausente, determinismo de hash, arredondamento half-up, redução de
base, Simples por faixa, RBT12 ausente/fora da faixa, isenção, base não
suportada, município como parâmetro, tributo duplicado, contexto cancelado,
conversão para `tax_calc` e formatação BRL.

### Goldens sintéticos

`internal/modules/fiscal/testdata/tax/*.json` — cinco casos marcados
`SINTETICO E NAO OFICIAL`:

| Caso | Resultado |
| --- | --- |
| iss-percentual | base 10000, valor 500, alíquota 5.000000 |
| cbs-percentual | base 10000, valor 90, alíquota 0.900000 |
| iss-simples-faixa | base 10000, valor 550, alíquota efetiva 5.500000 |
| iss-isencao | base 10000, valor 0, sem alíquota |
| iss-reducao-base | base 8000, valor 800, alíquota 10.000000 |

`TestTaxEngineGoldenFiles` compara base, valor, alíquota e hash. Nenhum valor
é apresentado como regra fiscal real.

### Integração PostgreSQL real

`TestTaxEnginePostgresDatadoPrecedenciaEFailClosed` usa `amparius_app`
(`NOSUPERUSER NOBYPASSRLS`) e cleanup administrativo conferido. Prova:

- precedência tenant (4,5%) sobre global (5%);
- fallback global para outro tenant;
- competência fora da vigência bloqueia;
- serviço sem regra bloqueia antes de persistir `tax_calc`;
- ordem canônica CBS antes de ISS;
- persistência de 2 linhas com `calculation_hash` conferido;
- `tax_calc` append-only;
- Simples por faixa com RBT12 dentro/fora da faixa.

Resultado local:

```
go build ./...        OK
go vet ./...          OK
go test ./... -count=1 OK (todos os pacotes, com DSNs de integração)
cobertura fiscal      80,2% das statements
```

## Pendências e bloqueios explícitos

1. **Sem `tax_rule` oficial importada.** O repositório não contém alíquota real
   e não foi criada nenhuma. Emissão real continua bloqueada até a importação
   datada e o gate do ADR 0010 §6.
2. **Fortaleza/ISS.** A fonte municipal (LC 159/2013 art. 245 e alterações)
   continua fora; nada foi assumido a partir da planilha nacional.
3. **`por_unidade`.** Exige contrato de quantidade/unidade antes de qualquer
   fórmula; falha fechado por enquanto.
4. **DPS × motor.** `pAliq`/valores de ISSQN na DPS não foram mapeados: a
   semântica para Simples e a origem de `tribISSQN`/`tpRetISSQN` ainda não têm
   fonte datada fechada. O builder continua recebendo esses campos como entrada.
5. **Facet `serie=1` (BLOQUEADOR da DPS oficial).** O XSD v1.01 rejeita séries
   válidas; `.\scripts\validate-nfse-dps-official.ps1` sai com código 1 (6/6
   fixtures). Sem errata/correção oficial do `TSSerieDPS`, a DPS oficial não
   pode ser aceita e a issue #25 permanece PARCIAL/BLOQUEADA.
6. **Goldens oficiais ausentes.** Os 5 goldens do motor são sintéticos; não há
   golden oficial de cálculo porque não há `tax_rule` oficial no repositório.
7. **Transmissão, certificado e contingência.** Fora desta fatia.

## Arquivos alterados

- `internal/modules/fiscal/tax_engine.go` (novo)
- `internal/modules/fiscal/tax_engine_test.go` (novo)
- `internal/modules/fiscal/tax_engine_integration_test.go` (novo)
- `internal/modules/fiscal/testdata/tax/*.json` (novos)
- `internal/modules/fiscal/nfse/dps_tax_engine_test.go` (novo)
- `internal/modules/fiscal/checker.go` (RBT12 por faixa)
- `internal/modules/fiscal/documents.go` (calculation_hash)
- `migrations/0022_tax_calc_hash.sql` (nova)
- `docs/adr/0015-motor-tributos-datado.md` (novo)
- `docs/data/DER.md` (TAX_CALC e nota 20)
- `docs/ENGINEERING.md` (status PASS/PARTIAL/BLOCKED da FIS-02A)
- `scripts/validate-nfse-dps-official.ps1` (novo; falha enquanto o facet existir)
- `docs/fiscal/2026-11-24-fis-02a-motor-tributos-dps.md` (este relatório)

As alterações pré-existentes em `skills/nfse-documentacao-tecnica` não foram
tocadas.
