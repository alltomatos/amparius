package nfse_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
	"github.com/google/uuid"
)

type staticTaxResolver struct{ rule fiscal.TaxRule }

func (s staticTaxResolver) LookupTaxRule(ctx context.Context, criteria fiscal.TaxRuleCriteria, competence time.Time) (fiscal.TaxRule, error) {
	if err := ctx.Err(); err != nil {
		return fiscal.TaxRule{}, err
	}
	return s.rule, nil
}

// TestDPSOficialAceitaValorMonetarioDoMotor prova que o formato monetario
// produzido pelo motor e aceito pelo XSD oficial v1.01 na DPS. A aliquota do
// resolver e SINTETICA e NAO OFICIAL; o teste nao afirma regra fiscal real.
func TestDPSOficialAceitaValorMonetarioDoMotor(t *testing.T) {
	rate := "5.000000"
	rule := fiscal.TaxRule{
		ID:               uuid.MustParse("11111111-1111-7111-8111-111111111111"),
		TaxType:          "iss",
		Regime:           "geral",
		ScopeKind:        "nfse",
		ServiceCode:      "99.99",
		MunicipalityCode: "9999999",
		BasisKind:        "percentual",
		Rate:             &rate,
		SourceHash:       strings.Repeat("a", 64),
		Version:          1,
	}
	engine, err := fiscal.NewTaxEngine(staticTaxResolver{rule: rule})
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	computation, err := engine.ComputeItem(context.Background(), competence, []fiscal.TaxItemRequest{{
		Criteria: fiscal.TaxRuleCriteria{
			TaxType: "iss", Regime: "geral", ScopeKind: "nfse",
			ServiceCode: "99.99", MunicipalityCode: "9999999",
		},
		BaseCents: 10000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if computation.Lines[0].AmountCents != 500 {
		t.Fatalf("valor do motor inesperado: %d", computation.Lines[0].AmountCents)
	}

	input := carregarFixtureOficial(t)
	input.Valores.VServ = fiscal.FormatBRL(10000)
	dps, err := nfse.BuildDPSOficial(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dps.XML), "<vServ>100.00</vServ>") {
		t.Fatalf("valor monetario do motor nao foi serializado: %s", dps.XML)
	}
	resultado, err := novoValidadorOficial(t).ValidateDPS(context.Background(), dps)
	if err != nil {
		t.Fatal(err)
	}
	if !resultado.Valid {
		t.Fatalf("DPS com valor do motor deveria ser aceita pelo XSD vigente: %v", resultado.Errors)
	}
	if err := nfse.ClassifyXSDIncompatibility(resultado); err != nil {
		t.Fatalf("DPS valida nao deveria ser classificada como incompativel: %v", err)
	}
}
