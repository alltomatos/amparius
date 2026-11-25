package fiscal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRuleResolver struct {
	rules map[string]TaxRule
	err   error
	calls []TaxRuleCriteria
}

func (f *fakeRuleResolver) LookupTaxRule(ctx context.Context, criteria TaxRuleCriteria, competence time.Time) (TaxRule, error) {
	if err := ctx.Err(); err != nil {
		return TaxRule{}, err
	}
	f.calls = append(f.calls, criteria)
	if f.err != nil {
		return TaxRule{}, f.err
	}
	rule, ok := f.rules[criteria.TaxType]
	if !ok {
		return TaxRule{}, ErrTaxRuleNotFound
	}
	return rule, nil
}

func testRule(taxType, basis string) TaxRule {
	return TaxRule{
		ID:               uuid.MustParse("11111111-1111-7111-8111-111111111111"),
		TaxType:          taxType,
		Regime:           "geral",
		ScopeKind:        "nfse",
		ServiceCode:      "99.99",
		MunicipalityCode: "9999999",
		BasisKind:        basis,
		SourceHash:       strings.Repeat("a", 64),
		Version:          1,
	}
}

func strPtr(value string) *string { return &value }

func mustEngine(t *testing.T, resolver TaxRuleResolver) *TaxEngine {
	t.Helper()
	engine, err := NewTaxEngine(resolver)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func testCompetence() time.Time {
	return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
}

func TestTaxEngineFalhaSemRegra(t *testing.T) {
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{}})
	_, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999"},
		BaseCents: 10000,
	}})
	if !errors.Is(err, ErrTaxRuleRequired) || !errors.Is(err, ErrTaxRuleNotFound) {
		t.Fatalf("regra ausente deveria falhar fechado: %v", err)
	}
}

func TestTaxEnginePercentualDeterministico(t *testing.T) {
	rule := testRule("iss", "percentual")
	rule.Rate = strPtr("5.000000")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	requests := []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999"},
		BaseCents: 10000,
	}}
	first, err := engine.ComputeItem(context.Background(), testCompetence(), requests)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.ComputeItem(context.Background(), testCompetence(), requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Lines) != 1 || first.Lines[0].AmountCents != 500 || first.Lines[0].Rate == nil || *first.Lines[0].Rate != "5.000000" {
		t.Fatalf("calculo percentual inesperado: %+v", first.Lines)
	}
	if first.Hash == "" || first.Hash != second.Hash || first.Lines[0].CalculationHash != second.Lines[0].CalculationHash {
		t.Fatalf("hash nao reproduzivel: %s / %s", first.Hash, second.Hash)
	}
	other, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  requests[0].Criteria,
		BaseCents: 10001,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if other.Hash == first.Hash {
		t.Fatal("base diferente deveria mudar o hash")
	}
}

func TestTaxEngineArredondamentoHalfUp(t *testing.T) {
	rule := testRule("iss", "percentual")
	rule.Rate = strPtr("5.000000")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	cases := []struct {
		base, want int64
	}{
		{333, 17},
		{10, 1},
		{9, 0},
	}
	for _, c := range cases {
		computation, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
			Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999"},
			BaseCents: c.base,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if computation.Lines[0].AmountCents != c.want {
			t.Fatalf("base %d: esperado %d, obtido %d", c.base, c.want, computation.Lines[0].AmountCents)
		}
	}
}

func TestTaxEngineReducaoDeBase(t *testing.T) {
	rule := testRule("iss", "percentual")
	rule.Rate = strPtr("10.000000")
	rule.BaseReductionPercent = strPtr("20.0000")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	computation, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999"},
		BaseCents: 10000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	line := computation.Lines[0]
	if line.BaseCents != 8000 || line.AmountCents != 800 {
		t.Fatalf("reducao de base: base=%d amount=%d", line.BaseCents, line.AmountCents)
	}
}

func TestTaxEngineSimplesFaixa(t *testing.T) {
	rule := testRule("iss", "faixa_simples")
	rule.Regime = "simples_nacional"
	rule.SimplesAnnex = "III"
	rule.Rate = strPtr("6.000000")
	rule.DeductionAmount = strPtr("5000.00")
	rule.RBT12From = strPtr("0.00")
	rule.RBT12To = strPtr("2000000.00")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	computation, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:   TaxRuleCriteria{TaxType: "iss", Regime: "simples_nacional", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999", BasisKind: "faixa_simples", SimplesAnnex: "III"},
		BaseCents:  10000,
		RBT12Cents: 100000000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	line := computation.Lines[0]
	if line.AmountCents != 550 || line.Rate == nil || *line.Rate != "5.500000" {
		t.Fatalf("simples: amount=%d rate=%v", line.AmountCents, line.Rate)
	}
}

func TestTaxEngineSimplesExigeRBT12(t *testing.T) {
	rule := testRule("iss", "faixa_simples")
	rule.Rate = strPtr("6.000000")
	rule.DeductionAmount = strPtr("5000.00")
	rule.RBT12From = strPtr("0.00")
	rule.RBT12To = strPtr("2000000.00")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	_, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "simples_nacional", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999", BasisKind: "faixa_simples"},
		BaseCents: 10000,
	}})
	if !errors.Is(err, ErrTaxSimpleRBT12Required) {
		t.Fatalf("RBT12 ausente deveria falhar: %v", err)
	}
}

func TestTaxEngineSimplesForaDaFaixa(t *testing.T) {
	rule := testRule("iss", "faixa_simples")
	rule.Rate = strPtr("6.000000")
	rule.DeductionAmount = strPtr("5000.00")
	rule.RBT12From = strPtr("0.00")
	rule.RBT12To = strPtr("2000000.00")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	_, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:   TaxRuleCriteria{TaxType: "iss", Regime: "simples_nacional", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999", BasisKind: "faixa_simples"},
		BaseCents:  10000,
		RBT12Cents: 300000000,
	}})
	if !errors.Is(err, ErrTaxRuleRequired) {
		t.Fatalf("RBT12 fora da faixa deveria falhar: %v", err)
	}
}

func TestTaxEngineIsencao(t *testing.T) {
	rule := testRule("iss", "isencao")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	computation, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999"},
		BaseCents: 10000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	line := computation.Lines[0]
	if line.AmountCents != 0 || line.Rate != nil || line.BaseCents != 10000 {
		t.Fatalf("isencao inesperada: %+v", line)
	}
}

func TestTaxEngineBaseNaoSuportada(t *testing.T) {
	rule := testRule("iss", "por_unidade")
	rule.Rate = strPtr("1.000000")
	rule.RateUnit = "brl_por_unidade"
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	_, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999"},
		BaseCents: 10000,
	}})
	if !errors.Is(err, ErrTaxBasisUnsupported) {
		t.Fatalf("base nao suportada deveria falhar: %v", err)
	}
}

func TestTaxEngineMunicipioEParametro(t *testing.T) {
	rule := testRule("iss", "percentual")
	rule.Rate = strPtr("5.000000")
	resolver := &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}}
	engine := mustEngine(t, resolver)
	if _, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99"},
		BaseCents: 10000,
	}}); !errors.Is(err, ErrTaxComputationInvalid) {
		t.Fatalf("ISS sem municipio deveria falhar: %v", err)
	}
	if len(resolver.calls) != 0 {
		t.Fatal("resolvedor nao deveria ser chamado sem municipio")
	}
	computation, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "2304400"},
		BaseCents: 10000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(computation.Lines) != 1 {
		t.Fatalf("linhas: %d", len(computation.Lines))
	}
	if len(resolver.calls) != 1 || resolver.calls[0].MunicipalityCode != "2304400" {
		t.Fatalf("municipio deveria ser repassado como parametro: %+v", resolver.calls)
	}
}

func TestTaxEngineRejeitaTributoDuplicado(t *testing.T) {
	rule := testRule("iss", "percentual")
	rule.Rate = strPtr("5.000000")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	criteria := TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999"}
	_, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{
		{Criteria: criteria, BaseCents: 10000},
		{Criteria: criteria, BaseCents: 20000},
	})
	if !errors.Is(err, ErrTaxComputationInvalid) {
		t.Fatalf("duplicidade deveria falhar: %v", err)
	}
}

func TestTaxEngineContextoCancelado(t *testing.T) {
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.ComputeItem(ctx, testCompetence(), []TaxItemRequest{{
		Criteria: TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", MunicipalityCode: "9999999"},
	}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("contexto cancelado: %v", err)
	}
}

func TestTaxEngineToCalculations(t *testing.T) {
	rule := testRule("iss", "percentual")
	rule.Rate = strPtr("5.000000")
	engine := mustEngine(t, &fakeRuleResolver{rules: map[string]TaxRule{"iss": rule}})
	computation, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
		Criteria:  TaxRuleCriteria{TaxType: "iss", Regime: "geral", ScopeKind: "nfse", ServiceCode: "99.99", MunicipalityCode: "9999999"},
		BaseCents: 10000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	tenantID, itemID := uuid.New(), uuid.New()
	calculations := computation.ToCalculations(tenantID, itemID)
	if len(calculations) != 1 {
		t.Fatalf("calculos: %d", len(calculations))
	}
	calc := calculations[0]
	if calc.TenantID != tenantID || calc.ItemID != itemID || calc.AmountCents != 500 ||
		calc.Rate == nil || *calc.Rate != "5.000000" || calc.RuleSourceHash == nil ||
		*calc.RuleSourceHash != rule.SourceHash || !json.Valid(calc.CalculationInput) {
		t.Fatalf("snapshot tax_calc invalido: %+v", calc)
	}
	if sha256Hex(calc.CalculationInput) != computation.Lines[0].CalculationHash {
		t.Fatal("hash do calculation_input diverge")
	}
}

func TestFormatBRL(t *testing.T) {
	cases := map[int64]string{0: "0.00", 1: "0.01", 100: "1.00", 123456: "1234.56", -5: "-0.05"}
	for cents, want := range cases {
		if got := FormatBRL(cents); got != want {
			t.Fatalf("%d: got %s want %s", cents, got, want)
		}
	}
}

type goldenTaxRule struct {
	ID                   string  `json:"id"`
	TaxType              string  `json:"tax_type"`
	Regime               string  `json:"regime"`
	ScopeKind            string  `json:"scope_kind"`
	ServiceCode          string  `json:"service_code"`
	MunicipalityCode     string  `json:"municipality_code"`
	NCM                  string  `json:"ncm"`
	ClassTrib            string  `json:"class_trib"`
	BasisKind            string  `json:"basis_kind"`
	Rate                 *string `json:"rate,omitempty"`
	DeductionAmount      *string `json:"deduction_amount,omitempty"`
	BaseReductionPercent *string `json:"base_reduction_percent,omitempty"`
	RBT12From            *string `json:"rbt12_from,omitempty"`
	RBT12To              *string `json:"rbt12_to,omitempty"`
	SimplesAnnex         string  `json:"simples_annex,omitempty"`
	SourceHash           string  `json:"source_hash"`
	Version              int     `json:"version"`
}

type goldenTaxCase struct {
	Name    string        `json:"name"`
	Comment string        `json:"_comment"`
	Rule    goldenTaxRule `json:"rule"`
	Request struct {
		BaseCents  int64 `json:"base_cents"`
		RBT12Cents int64 `json:"rbt12_cents"`
	} `json:"request"`
	Expected struct {
		BaseCents   int64   `json:"base_cents"`
		AmountCents int64   `json:"amount_cents"`
		Rate        *string `json:"rate"`
		Hash        string  `json:"hash"`
	} `json:"expected"`
}

func TestTaxEngineGoldenFiles(t *testing.T) {
	dir := filepath.Join("testdata", "tax")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	update := os.Getenv("UPDATE_TAX_GOLDEN") == "1"
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var testCase goldenTaxCase
		if err := json.Unmarshal(raw, &testCase); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if !strings.Contains(testCase.Comment, "SINTETICO") {
			t.Fatalf("%s: golden deve declarar valores SINTETICOS e NAO OFICIAIS", entry.Name())
		}
		rule := TaxRule{
			ID:                   uuid.MustParse(testCase.Rule.ID),
			TaxType:              testCase.Rule.TaxType,
			Regime:               testCase.Rule.Regime,
			ScopeKind:            testCase.Rule.ScopeKind,
			ServiceCode:          testCase.Rule.ServiceCode,
			MunicipalityCode:     testCase.Rule.MunicipalityCode,
			NCM:                  testCase.Rule.NCM,
			ClassTrib:            testCase.Rule.ClassTrib,
			BasisKind:            testCase.Rule.BasisKind,
			Rate:                 testCase.Rule.Rate,
			DeductionAmount:      testCase.Rule.DeductionAmount,
			BaseReductionPercent: testCase.Rule.BaseReductionPercent,
			RBT12From:            testCase.Rule.RBT12From,
			RBT12To:              testCase.Rule.RBT12To,
			SimplesAnnex:         testCase.Rule.SimplesAnnex,
			SourceHash:           testCase.Rule.SourceHash,
			Version:              testCase.Rule.Version,
		}
		resolver := &fakeRuleResolver{rules: map[string]TaxRule{rule.TaxType: rule}}
		engine := mustEngine(t, resolver)
		criteria := TaxRuleCriteria{
			TaxType: rule.TaxType, Regime: rule.Regime, ScopeKind: rule.ScopeKind,
			ServiceCode: rule.ServiceCode, MunicipalityCode: rule.MunicipalityCode,
			NCM: rule.NCM, ClassTrib: rule.ClassTrib, SimplesAnnex: rule.SimplesAnnex,
		}
		computation, err := engine.ComputeItem(context.Background(), testCompetence(), []TaxItemRequest{{
			Criteria: criteria, BaseCents: testCase.Request.BaseCents, RBT12Cents: testCase.Request.RBT12Cents,
		}})
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		line := computation.Lines[0]
		if update {
			testCase.Expected.BaseCents = line.BaseCents
			testCase.Expected.AmountCents = line.AmountCents
			testCase.Expected.Rate = line.Rate
			testCase.Expected.Hash = computation.Hash
			encoded, err := json.MarshalIndent(testCase, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if line.BaseCents != testCase.Expected.BaseCents || line.AmountCents != testCase.Expected.AmountCents {
			t.Fatalf("%s: base/valor divergem: got base=%d amount=%d", entry.Name(), line.BaseCents, line.AmountCents)
		}
		if !sameOptionalString(line.Rate, testCase.Expected.Rate) {
			t.Fatalf("%s: rate divergiu: got %v", entry.Name(), line.Rate)
		}
		if computation.Hash != testCase.Expected.Hash || testCase.Expected.Hash == "" {
			t.Fatalf("%s: hash divergiu: got %s want %s", entry.Name(), computation.Hash, testCase.Expected.Hash)
		}
		checked++
	}
	if !update && checked == 0 {
		t.Fatal("nenhum golden tributario verificado")
	}
}

func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
