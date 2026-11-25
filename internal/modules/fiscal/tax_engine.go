package fiscal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// O motor de tributos nao define aliquota, codigo ou vigencia. Ele apenas
// consulta fiscal.tax_rule pela competencia (tenant > global) e aplica a
// formula declarada pela propria regra. Regra ausente, ambigua ou fora da
// faixa falha fechado: nenhuma linha parcial e produzida.

var (
	// ErrTaxRuleRequired indica que o item exige um tributo sem regra vigente.
	ErrTaxRuleRequired = errors.New("regra tributaria obrigatoria ausente para o item")
	// ErrTaxBasisUnsupported indica uma base de calculo que esta fatia nao
	// implementa; a operacao falha em vez de aproximar.
	ErrTaxBasisUnsupported = errors.New("base de calculo tributario nao suportada")
	// ErrTaxComputationInvalid indica entrada incoerente para o calculo.
	ErrTaxComputationInvalid = errors.New("calculo tributario invalido")
	// ErrTaxSimpleRBT12Required indica faixa do Simples sem RBT12 informado.
	ErrTaxSimpleRBT12Required = errors.New("regime simples exige RBT12 para calcular a aliquota efetiva")
)

const taxComputationVersion = 1

// TaxRuleResolver e a porta de leitura de regras datadas. *Checker a
// implementa consultando fiscal.tax_rule com RLS e precedencia tenant > global.
type TaxRuleResolver interface {
	LookupTaxRule(ctx context.Context, criteria TaxRuleCriteria, competence time.Time) (TaxRule, error)
}

// TaxItemRequest descreve um tributo a calcular para um item. Todos os
// criterios vem do caso de uso (sessao, configuracao e cadastro); o motor nao
// preenche municipio, servico, NCM nem codigo tributario por conta propria.
type TaxItemRequest struct {
	Criteria   TaxRuleCriteria
	BaseCents  int64
	RBT12Cents int64
}

// TaxLine e o resultado reproduzivel de um tributo: snapshot canonico,
// valores em centavos e hash do snapshot.
type TaxLine struct {
	TaxType          string
	BasisKind        string
	BaseCents        int64
	Rate             *string
	AmountCents      int64
	RuleID           uuid.UUID
	RuleSourceHash   string
	RuleVersion      int
	CalculationInput json.RawMessage
	CalculationHash  string
}

// TaxComputation agrega as linhas de um item com hash reproduzivel.
type TaxComputation struct {
	Competence time.Time
	Lines      []TaxLine
	Hash       string
}

// TaxEngine aplica regras oficiais datadas por competencia.
type TaxEngine struct {
	resolver TaxRuleResolver
}

func NewTaxEngine(resolver TaxRuleResolver) (*TaxEngine, error) {
	if resolver == nil {
		return nil, errors.New("resolvedor de regras fiscais nulo")
	}
	return &TaxEngine{resolver: resolver}, nil
}

// ComputeItem calcula todos os tributos de um item em uma unica operacao
// all-or-nothing. A ordem canonica e por tax_type e pelos criterios, de modo
// que a mesma entrada produza sempre as mesmas linhas e o mesmo hash.
func (e *TaxEngine) ComputeItem(ctx context.Context, competence time.Time, requests []TaxItemRequest) (TaxComputation, error) {
	if err := ctx.Err(); err != nil {
		return TaxComputation{}, err
	}
	if competence.IsZero() {
		return TaxComputation{}, fmt.Errorf("%w: competencia ausente", ErrTaxComputationInvalid)
	}
	if len(requests) == 0 {
		return TaxComputation{}, fmt.Errorf("%w: nenhum tributo solicitado", ErrTaxComputationInvalid)
	}
	ordered := append([]TaxItemRequest(nil), requests...)
	sort.SliceStable(ordered, func(i, j int) bool { return lessTaxRequest(ordered[i], ordered[j]) })

	lines := make([]TaxLine, 0, len(ordered))
	seen := make(map[string]bool, len(ordered))
	for _, request := range ordered {
		line, err := e.computeOne(ctx, competence, request)
		if err != nil {
			return TaxComputation{}, err
		}
		if seen[line.TaxType] {
			return TaxComputation{}, fmt.Errorf("%w: tributo duplicado: %s", ErrTaxComputationInvalid, line.TaxType)
		}
		seen[line.TaxType] = true
		lines = append(lines, line)
	}
	hash, err := hashComputation(competence, lines)
	if err != nil {
		return TaxComputation{}, err
	}
	return TaxComputation{Competence: dateOnly(competence), Lines: lines, Hash: hash}, nil
}

func (e *TaxEngine) computeOne(ctx context.Context, competence time.Time, request TaxItemRequest) (TaxLine, error) {
	criteria := request.Criteria
	if strings.TrimSpace(criteria.TaxType) == "" || strings.TrimSpace(criteria.Regime) == "" ||
		strings.TrimSpace(criteria.ScopeKind) == "" {
		return TaxLine{}, fmt.Errorf("%w: tax_type, regime e scope_kind sao obrigatorios", ErrTaxComputationInvalid)
	}
	if request.BaseCents < 0 {
		return TaxLine{}, fmt.Errorf("%w: base negativa", ErrTaxComputationInvalid)
	}
	if criteria.TaxType == "iss" && strings.TrimSpace(criteria.MunicipalityCode) == "" {
		return TaxLine{}, fmt.Errorf("%w: ISS exige municipality_code como parametro explicito", ErrTaxComputationInvalid)
	}
	if request.RBT12Cents < 0 {
		return TaxLine{}, fmt.Errorf("%w: RBT12 negativo", ErrTaxComputationInvalid)
	}
	if request.RBT12Cents > 0 {
		rbt12 := request.RBT12Cents
		criteria.RBT12ActualCents = &rbt12
	} else if criteria.BasisKind == "faixa_simples" || strings.TrimSpace(criteria.SimplesAnnex) != "" {
		return TaxLine{}, fmt.Errorf("%w: %s", ErrTaxSimpleRBT12Required, criteria.TaxType)
	}

	rule, err := e.resolver.LookupTaxRule(ctx, criteria, competence)
	if err != nil {
		if errors.Is(err, ErrTaxRuleNotFound) {
			return TaxLine{}, fmt.Errorf("%w: %s: %w", ErrTaxRuleRequired, criteria.TaxType, err)
		}
		return TaxLine{}, err
	}
	return calculateTaxLine(criteria, request, rule, competence)
}

type taxCalculationSnapshot struct {
	Version              int    `json:"version"`
	Competence           string `json:"competence"`
	TaxType              string `json:"tax_type"`
	Regime               string `json:"regime"`
	ScopeKind            string `json:"scope_kind"`
	ServiceCode          string `json:"service_code"`
	MunicipalityCode     string `json:"municipality_code"`
	NCM                  string `json:"ncm"`
	ClassTrib            string `json:"class_trib"`
	ItemBaseCents        int64  `json:"item_base_cents"`
	RBT12Cents           int64  `json:"rbt12_cents"`
	BasisKind            string `json:"basis_kind"`
	NominalRate          string `json:"nominal_rate,omitempty"`
	EffectiveRate        string `json:"effective_rate,omitempty"`
	BaseReductionPercent string `json:"base_reduction_percent,omitempty"`
	DeductionCents       int64  `json:"deduction_cents,omitempty"`
	RuleID               string `json:"rule_id"`
	RuleSourceHash       string `json:"rule_source_hash"`
	RuleVersion          int    `json:"rule_version"`
	BaseCents            int64  `json:"base_cents"`
	AmountCents          int64  `json:"amount_cents"`
}

func calculateTaxLine(criteria TaxRuleCriteria, request TaxItemRequest, rule TaxRule, competence time.Time) (TaxLine, error) {
	snapshot := taxCalculationSnapshot{
		Version:          taxComputationVersion,
		Competence:       dateOnly(competence).Format("2006-01-02"),
		TaxType:          rule.TaxType,
		Regime:           rule.Regime,
		ScopeKind:        rule.ScopeKind,
		ServiceCode:      rule.ServiceCode,
		MunicipalityCode: rule.MunicipalityCode,
		NCM:              rule.NCM,
		ClassTrib:        rule.ClassTrib,
		ItemBaseCents:    request.BaseCents,
		RBT12Cents:       request.RBT12Cents,
		BasisKind:        rule.BasisKind,
		RuleID:           rule.ID.String(),
		RuleSourceHash:   rule.SourceHash,
		RuleVersion:      rule.Version,
	}
	var rate *string
	base := request.BaseCents

	switch rule.BasisKind {
	case "isencao":
		snapshot.BaseCents = base
		snapshot.AmountCents = 0
	case "percentual":
		nominal, nominalScaled, err := ruleRate(rule.Rate)
		if err != nil {
			return TaxLine{}, err
		}
		base, err = applyBaseReduction(base, rule.BaseReductionPercent)
		if err != nil {
			return TaxLine{}, err
		}
		amount, err := multiplyCents(base, nominal)
		if err != nil {
			return TaxLine{}, err
		}
		effective := formatScaled(nominalScaled, 6)
		snapshot.NominalRate = formatScaled(nominalScaled, 6)
		snapshot.EffectiveRate = effective
		snapshot.BaseReductionPercent = formatReduction(rule.BaseReductionPercent)
		snapshot.BaseCents = base
		snapshot.AmountCents = amount
		rate = &effective
	case "faixa_simples":
		if request.RBT12Cents <= 0 {
			return TaxLine{}, fmt.Errorf("%w: %s", ErrTaxSimpleRBT12Required, rule.TaxType)
		}
		if err := checkRBT12Range(rule, request.RBT12Cents); err != nil {
			return TaxLine{}, err
		}
		nominal, nominalScaled, err := ruleRate(rule.Rate)
		if err != nil {
			return TaxLine{}, err
		}
		deduction, err := parseOptionalCents(rule.DeductionAmount, "deduction_amount")
		if err != nil {
			return TaxLine{}, err
		}
		if deduction < 0 {
			return TaxLine{}, fmt.Errorf("%w: deduction_amount negativo", ErrTaxComputationInvalid)
		}
		effective, err := simplesEffectiveRate(request.RBT12Cents, nominal, deduction)
		if err != nil {
			return TaxLine{}, err
		}
		base, err = applyBaseReduction(base, rule.BaseReductionPercent)
		if err != nil {
			return TaxLine{}, err
		}
		amount, err := multiplyCents(base, effective)
		if err != nil {
			return TaxLine{}, err
		}
		effectiveRate := formatScaled(ratToScaledPercent(effective, 6), 6)
		snapshot.NominalRate = formatScaled(nominalScaled, 6)
		snapshot.EffectiveRate = effectiveRate
		snapshot.BaseReductionPercent = formatReduction(rule.BaseReductionPercent)
		snapshot.DeductionCents = deduction
		snapshot.BaseCents = base
		snapshot.AmountCents = amount
		rate = &effectiveRate
	default:
		return TaxLine{}, fmt.Errorf("%w: %s", ErrTaxBasisUnsupported, rule.BasisKind)
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		return TaxLine{}, fmt.Errorf("serializar snapshot tributario: %w", err)
	}
	return TaxLine{
		TaxType:          snapshot.TaxType,
		BasisKind:        snapshot.BasisKind,
		BaseCents:        snapshot.BaseCents,
		Rate:             rate,
		AmountCents:      snapshot.AmountCents,
		RuleID:           rule.ID,
		RuleSourceHash:   rule.SourceHash,
		RuleVersion:      rule.Version,
		CalculationInput: raw,
		CalculationHash:  sha256Hex(raw),
	}, nil
}

func ruleRate(value *string) (*big.Rat, *big.Int, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil, fmt.Errorf("%w: regra percentual sem rate", ErrTaxComputationInvalid)
	}
	scaled, err := parseScaledDecimal(*value, 6)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: rate invalido: %v", ErrTaxComputationInvalid, err)
	}
	if scaled.Sign() < 0 {
		return nil, nil, fmt.Errorf("%w: rate negativo", ErrTaxComputationInvalid)
	}
	// Percentual: rateScaled / (100 * 10^6) = rateScaled / 10^8.
	rate := new(big.Rat).SetFrac(scaled, pow10(8))
	return rate, scaled, nil
}

func applyBaseReduction(baseCents int64, reduction *string) (int64, error) {
	if reduction == nil || strings.TrimSpace(*reduction) == "" {
		return baseCents, nil
	}
	scaled, err := parseScaledDecimal(*reduction, 4)
	if err != nil {
		return 0, fmt.Errorf("%w: base_reduction_percent invalido: %v", ErrTaxComputationInvalid, err)
	}
	if scaled.Sign() < 0 || scaled.Cmp(pow10(6)) > 0 {
		return 0, fmt.Errorf("%w: base_reduction_percent fora de 0..100", ErrTaxComputationInvalid)
	}
	fraction := new(big.Rat).SetFrac(scaled, pow10(6))
	factor := new(big.Rat).Sub(big.NewRat(1, 1), fraction)
	value := new(big.Rat).Mul(big.NewRat(baseCents, 1), factor)
	reduced, err := roundHalfUp(value)
	if err != nil {
		return 0, err
	}
	return reduced, nil
}

func multiplyCents(baseCents int64, rate *big.Rat) (int64, error) {
	value := new(big.Rat).Mul(big.NewRat(baseCents, 1), rate)
	return roundHalfUp(value)
}

func simplesEffectiveRate(rbt12Cents int64, nominalRate *big.Rat, deductionCents int64) (*big.Rat, error) {
	if rbt12Cents <= 0 {
		return nil, fmt.Errorf("%w: RBT12 ausente", ErrTaxSimpleRBT12Required)
	}
	gross := new(big.Rat).Mul(big.NewRat(rbt12Cents, 1), nominalRate)
	effective := new(big.Rat).Sub(gross, big.NewRat(deductionCents, 1))
	if effective.Sign() < 0 {
		return nil, fmt.Errorf("%w: deducao maior que o imposto nominal", ErrTaxComputationInvalid)
	}
	return effective.Quo(effective, big.NewRat(rbt12Cents, 1)), nil
}

func checkRBT12Range(rule TaxRule, rbt12Cents int64) error {
	from, err := parseOptionalCents(rule.RBT12From, "rbt12_from")
	if err != nil {
		return err
	}
	to, err := parseOptionalCents(rule.RBT12To, "rbt12_to")
	if err != nil {
		return err
	}
	if from >= 0 && rbt12Cents < from {
		return fmt.Errorf("%w: RBT12 abaixo da faixa da regra %s", ErrTaxRuleRequired, rule.ID)
	}
	if rule.RBT12To != nil && rbt12Cents >= to {
		return fmt.Errorf("%w: RBT12 acima da faixa da regra %s", ErrTaxRuleRequired, rule.ID)
	}
	return nil
}

func parseOptionalCents(value *string, field string) (int64, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return -1, nil
	}
	scaled, err := parseScaledDecimal(*value, 2)
	if err != nil {
		return 0, fmt.Errorf("%w: %s invalido: %v", ErrTaxComputationInvalid, field, err)
	}
	if !scaled.IsInt64() {
		return 0, fmt.Errorf("%w: %s fora do intervalo", ErrTaxComputationInvalid, field)
	}
	return scaled.Int64(), nil
}

// parseScaledDecimal converte um decimal nao negativo em inteiro escalado.
// Aceita ate `scale` casas; nao arredonda nem normaliza o valor recebido.
func parseScaledDecimal(value string, scale int) (*big.Int, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil, errors.New("valor vazio")
	}
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		return nil, errors.New("sinal nao permitido")
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || parts[0] == "" {
		return nil, errors.New("formato decimal invalido")
	}
	integer, fraction := parts[0], ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" {
			return nil, errors.New("formato decimal invalido")
		}
	}
	if len(fraction) > scale {
		return nil, fmt.Errorf("mais de %d casas decimais", scale)
	}
	if !allDigits(integer) || (fraction != "" && !allDigits(fraction)) {
		return nil, errors.New("digitos invalidos")
	}
	digits := integer + fraction + strings.Repeat("0", scale-len(fraction))
	scaled, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, errors.New("valor decimal invalido")
	}
	return scaled, nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func roundHalfUp(value *big.Rat) (int64, error) {
	if value.Sign() < 0 {
		return 0, fmt.Errorf("%w: resultado negativo", ErrTaxComputationInvalid)
	}
	numerator := new(big.Int).Mul(value.Num(), big.NewInt(2))
	numerator.Add(numerator, value.Denom())
	denominator := new(big.Int).Mul(value.Denom(), big.NewInt(2))
	rounded := new(big.Int).Quo(numerator, denominator)
	if !rounded.IsInt64() {
		return 0, fmt.Errorf("%w: resultado fora do intervalo", ErrTaxComputationInvalid)
	}
	return rounded.Int64(), nil
}

func ratToScaledPercent(rate *big.Rat, scale int) *big.Int {
	percent := new(big.Rat).Mul(rate, big.NewRat(100, 1))
	scaled := new(big.Rat).Mul(percent, new(big.Rat).SetInt(pow10(scale)))
	rounded, _ := roundHalfUp(scaled)
	return big.NewInt(rounded)
}

func formatScaled(scaled *big.Int, scale int) string {
	text := scaled.String()
	if scale == 0 {
		return text
	}
	if len(text) <= scale {
		text = strings.Repeat("0", scale-len(text)+1) + text
	}
	return text[:len(text)-scale] + "." + text[len(text)-scale:]
}

func formatReduction(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return ""
	}
	scaled, err := parseScaledDecimal(*value, 4)
	if err != nil {
		return *value
	}
	return formatScaled(scaled, 4)
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

type computationSnapshot struct {
	Version    int               `json:"version"`
	Competence string            `json:"competence"`
	Lines      []json.RawMessage `json:"lines"`
}

func hashComputation(competence time.Time, lines []TaxLine) (string, error) {
	snapshot := computationSnapshot{Version: taxComputationVersion, Competence: dateOnly(competence).Format("2006-01-02")}
	for _, line := range lines {
		snapshot.Lines = append(snapshot.Lines, line.CalculationInput)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("serializar hash do calculo: %w", err)
	}
	return sha256Hex(raw), nil
}

func lessTaxRequest(a, b TaxItemRequest) bool {
	if a.Criteria.TaxType != b.Criteria.TaxType {
		return a.Criteria.TaxType < b.Criteria.TaxType
	}
	if a.Criteria.ServiceCode != b.Criteria.ServiceCode {
		return a.Criteria.ServiceCode < b.Criteria.ServiceCode
	}
	if a.Criteria.MunicipalityCode != b.Criteria.MunicipalityCode {
		return a.Criteria.MunicipalityCode < b.Criteria.MunicipalityCode
	}
	if a.Criteria.NCM != b.Criteria.NCM {
		return a.Criteria.NCM < b.Criteria.NCM
	}
	if a.Criteria.ClassTrib != b.Criteria.ClassTrib {
		return a.Criteria.ClassTrib < b.Criteria.ClassTrib
	}
	if a.BaseCents != b.BaseCents {
		return a.BaseCents < b.BaseCents
	}
	return a.RBT12Cents < b.RBT12Cents
}

// ToCalculations converte o resultado em snapshots prontos para
// fiscal.tax_calc. Cada linha usa o proprio snapshot canonico como
// calculation_input e o hash da regra como rule_source_hash.
func (c TaxComputation) ToCalculations(tenantID, itemID uuid.UUID) []TaxCalculation {
	calculations := make([]TaxCalculation, 0, len(c.Lines))
	for _, line := range c.Lines {
		ruleID := line.RuleID
		sourceHash := line.RuleSourceHash
		var rate *string
		if line.Rate != nil {
			value := *line.Rate
			rate = &value
		}
		calculationHash := line.CalculationHash
		calculations = append(calculations, TaxCalculation{
			TenantID:         tenantID,
			ItemID:           itemID,
			Competence:       c.Competence,
			TaxType:          line.TaxType,
			BaseCents:        line.BaseCents,
			AmountCents:      line.AmountCents,
			Rate:             rate,
			RuleID:           &ruleID,
			RuleSourceHash:   &sourceHash,
			CalculationInput: line.CalculationInput,
			CalculationHash:  &calculationHash,
		})
	}
	return calculations
}

// FormatBRL formata centavos como decimal com ponto e duas casas, sem
// separador de milhar. E o formato dos tipos monetarios do XSD v1.01.
func FormatBRL(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}
