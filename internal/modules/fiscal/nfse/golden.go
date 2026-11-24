package nfse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GoldenCase e um caso versionado do brief §5: entrada canonica, hash
// esperado e expectativa de validacao. Os valores de RTC nao sao copiados
// para o codigo; o oraculo oficial e gate da fatia 02.
type GoldenCase struct {
	Name          string   `json:"name"`
	Input         DPSInput `json:"input"`
	ExpectedHash  string   `json:"expected_hash"`
	ExpectInvalid bool     `json:"expect_invalid"`
}

type goldenFile struct {
	Name          string      `json:"name"`
	Input         goldenInput `json:"input"`
	ExpectedHash  string      `json:"expected_hash"`
	ExpectInvalid bool        `json:"expect_invalid"`
}

type goldenInput struct {
	IDDps            string                `json:"id_dps"`
	Competence       string                `json:"competence"`
	EmitterDocument  string                `json:"emitter_document"`
	MunicipalityCode string                `json:"municipality_code"`
	Series           string                `json:"series"`
	Number           int64                 `json:"number"`
	Environment      string                `json:"environment"`
	Items            []DPSItem             `json:"items"`
	TaxCalculations  []TaxCalculationInput `json:"tax_calculations"`
}

// LoadGoldenCases le todos os .json do diretorio em ordem estavel.
func LoadGoldenCases(dir string) ([]GoldenCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("ler golden cases: %w", err)
	}
	var casos []GoldenCase
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("ler %s: %w", entry.Name(), err)
		}
		var gf goldenFile
		if err := json.Unmarshal(raw, &gf); err != nil {
			return nil, fmt.Errorf("decodificar %s: %w", entry.Name(), err)
		}
		competence, err := time.Parse("2006-01-02", gf.Input.Competence)
		if err != nil {
			return nil, fmt.Errorf("competencia de %s: %w", entry.Name(), err)
		}
		casos = append(casos, GoldenCase{
			Name: gf.Name,
			Input: DPSInput{
				IDDps: gf.Input.IDDps, Competence: competence,
				EmitterDocument: gf.Input.EmitterDocument, MunicipalityCode: gf.Input.MunicipalityCode,
				Series: gf.Input.Series, Number: gf.Input.Number, Environment: gf.Input.Environment,
				Items: gf.Input.Items, TaxCalculations: gf.Input.TaxCalculations,
			},
			ExpectedHash: gf.ExpectedHash, ExpectInvalid: gf.ExpectInvalid,
		})
	}
	sort.Slice(casos, func(i, j int) bool { return casos[i].Name < casos[j].Name })
	return casos, nil
}
