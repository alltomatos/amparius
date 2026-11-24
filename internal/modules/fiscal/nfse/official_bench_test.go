package nfse_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
)

func carregarFixtureOficialBench(b *testing.B) nfse.OfficialDPSInput {
	b.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "official", "dps-minima.json"))
	if err != nil {
		b.Fatal(err)
	}
	var input nfse.OfficialDPSInput
	if err := json.Unmarshal(raw, &input); err != nil {
		b.Fatal(err)
	}
	return input
}

// BenchmarkBuildDPSOficialDiagnostica mede, por item, BuildDPSOficial +
// XSDValidator.ValidateDPS sobre a fixture minima oficial. A fixture e
// deliberadamente bloqueada pelo facet oficial de serie. Reproduzivel com:
//
//	go test -run '^$' -bench 'BenchmarkBuildDPSOficialDiagnostica' -benchmem ./internal/modules/fiscal/nfse/...
func BenchmarkBuildDPSOficialDiagnostica(b *testing.B) {
	validador, err := nfse.NewXSDValidator(xsdRootRel, xsdEntry)
	if err != nil {
		b.Fatalf("carregar DPS_v1.01.xsd: %v", err)
	}
	input := carregarFixtureOficialBench(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dps, err := nfse.BuildDPSOficial(ctx, input)
		if err != nil {
			b.Fatalf("BuildDPSOficial: %v", err)
		}
		res, err := validador.ValidateDPS(ctx, dps)
		if err != nil {
			b.Fatalf("ValidateDPS: %v", err)
		}
		if res.Valid {
			b.Fatal("DPS com serie=1 foi aceita apesar do facet oficial")
		}
	}
}
