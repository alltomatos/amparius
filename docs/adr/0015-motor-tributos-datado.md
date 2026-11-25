# ADR 0015 — Motor de tributos datado e snapshot com hash

Status: aceito e implementado em 24/11/2026 — referência à issue #25 (FIS-02A), aos ADRs 0004, 0010, 0011 e 0014 e ao DER.

Escopo desta decisão: **apenas o motor de tributos**. A DPS oficial v1.01
continua **BLOQUEADA** pelo facet `TSSerieDPS` do XSD versionado e a issue #25
permanece **PARCIAL**, não concluída. Ver
`docs/fiscal/2026-11-24-fis-02a-motor-tributos-dps.md`.

## Contexto

O ADR 0004 fixou o motor fiscal por item, com tabelas datadas e resultado
reproduzível, mas a fatia FIS-01 entregou apenas configuração, certificado,
consulta de regra (`Checker.LookupTaxRule`) e bloqueio de emissão. Não existia
um componente que transformasse uma regra oficial datada em valores de tributo
(base, alíquota efetiva e valor) com trilha reproduzível. Sem ele, a DPS não
tem de onde receber valores calculados e qualquer preenchimento no adapter
seria estimativa.

## Decisão

### 1. Motor separado do adapter

`fiscal.TaxEngine` recebe uma porta `TaxRuleResolver` e calcula tributos por
competência. `*Checker` é a implementação de produção: consulta
`fiscal.tax_rule` com RLS e precedência tenant > global. O adapter NFS-e
continua sem calcular tributo (ADR 0010, decisão 4).

### 2. Nenhum valor fiscal no código

O motor não contém alíquota, NCM, CST, cClassTrib, CFOP, código de serviço,
município ou faixa. Todos vêm de `TaxRuleCriteria` e da linha de `tax_rule`
vigente. Município é parâmetro explícito e obrigatório para ISS; o motor não
preenche nem infere município, serviço ou anexo.

### 3. Falha fechada e all-or-nothing

- Regra ausente, ambígua ou indisponível interrompe o item inteiro.
- Nenhuma linha parcial é produzida nem persistida quando qualquer tributo
  solicitado falha.
- `tax_type`, `regime` e `scope_kind` são obrigatórios.
- Faixa do Simples exige RBT12 positivo e dentro da faixa da regra.
- Base `por_unidade` falha com `ErrTaxBasisUnsupported` nesta fatia, em vez de
  aproximar.

### 4. Consulta por faixa de RBT12

`TaxRuleCriteria` ganhou `RBT12ActualCents`. Quando presente, o `Checker`
restringe regras `faixa_simples` àquelas cuja faixa `[rbt12_from, rbt12_to)`
contém a receita bruta dos últimos 12 meses. Regras sem faixa continuam
elegíveis. A exclusão GiST existente impede sobreposição para a mesma chave,
portanto a faixa resolvida é única (ou o lookup falha).

### 5. Fórmulas implementadas

| Base | Fórmula | Falha fechada |
| --- | --- | --- |
| `percentual` | valor = base reduzida × alíquota / 100 | alíquota ausente/negativa |
| `faixa_simples` | alíquota efetiva = (RBT12 × alíquota nominal − dedução) / RBT12; valor = base reduzida × alíquota efetiva | RBT12 ausente, fora da faixa ou alíquota efetiva negativa |
| `isencao` | valor = 0, sem alíquota | — |
| `por_unidade` | não implementada nesta fatia | `ErrTaxBasisUnsupported` |

A redução de base (`base_reduction_percent`) é aplicada antes do tributo. O
arredondamento é half-up para o centavo, determinístico e documentado.

### 6. Hash do snapshot

Cada linha produz um snapshot canônico (versão, competência, critérios
relevantes, regra, alíquota nominal/efetiva, base e valor) serializado em JSON.
O SHA-256 desse snapshot é a identidade reproduzível do cálculo. O agregado do
item concatena os snapshots em ordem canônica e recebe seu próprio hash.

A migration forward-only `0022_tax_calc_hash.sql` adiciona
`fiscal.tax_calc.calculation_hash` porque `jsonb` não preserva a forma textual
exata do snapshot. O `calculation_input` continua guardando o conteúdo
semântico; o hash fica em coluna própria, com CHECK de 64 hex minúsculos.

### 7. Persistência

`TaxComputation.ToCalculations` converte o resultado em `fiscal.TaxCalculation`
prontos para `fiscal.tax_calc`, com `rule_id`, `rule_source_hash`,
`calculation_input` e `calculation_hash`. A tabela continua append-only.

## Consequências

- O motor depende apenas de regras oficiais datadas; sem importação oficial,
  toda emissão de teste falha fechada — comportamento desejado.
- Regras de teste são sintéticas e marcadas; os goldens do motor não afirmam
  alíquota real.
- A DPS oficial existente permanece a única camada de transporte; esta ADR não
  cria mapeamento novo de campos fiscais da DPS.
- `tax_calc` passa a ter hash auditável sem editar a migration 0005.

## Não-escopo e pendências explícitas

- Nenhuma alíquota, código ou vigência oficial é semeada por esta ADR.
- Fortaleza continua sem `tax_rule` oficial importada (gate do ADR 0010 §6).
- `por_unidade` exige decisão de contrato (quantidade/unidade) antes de
  implementação.
- O bloqueio conhecido do facet `serie=1` do XSD v1.01 continua valendo para a
  DPS oficial.
- Mapear resultado do motor para `pAliq`/valores do ISSQN na DPS exige decisão
  própria: a semântica para Simples e a fonte de `tribISSQN` não estão fechadas.
- Nenhuma transmissão, certificado ou contingência é alterada.

## Critérios de aceite

1. Regra ausente, ambígua, fora da vigência ou fora da faixa falha fechado sem
   produzir linha parcial.
2. Município é parâmetro explícito; ISS sem município falha antes do resolver.
3. Mesma entrada + mesma regra produzem o mesmo snapshot e o mesmo hash.
4. `go test`, `go vet` e `go build` passam com PostgreSQL real.
5. `tax_calc.calculation_hash` é persistido e confere com o snapshot do motor.
6. Goldens sintéticos do motor são reproduzíveis e marcados como não oficiais.
