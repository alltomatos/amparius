package nfse_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
)

func TestValidadorLocalRejeitaXMLInvalido(t *testing.T) {
	validador := nfse.NewLocalValidator()
	dps, err := nfse.BuildDPS(context.Background(), entradaBasica())
	if err != nil {
		t.Fatal(err)
	}
	resultado, err := validador.ValidateDPS(context.Background(), dps)
	if err != nil || !resultado.Valid {
		t.Fatalf("DPS canonica deveria validar: %+v err=%v", resultado, err)
	}
	dpsQuebrada := dps
	dpsQuebrada.XML = []byte("<DPS>quebrado")
	resultado, err = validador.ValidateDPS(context.Background(), dpsQuebrada)
	if err != nil {
		t.Fatalf("falha de XSD e erro de construcao, nao erro de transporte: %v", err)
	}
	if resultado.Valid {
		t.Fatal("XML malformado deveria ser invalido")
	}
	if len(resultado.Errors) == 0 {
		t.Fatal("resultado invalido deve trazer diagnostico")
	}
}

func TestValidadorXSDPluggable(t *testing.T) {
	if _, err := nfse.NewXSDValidator(nil); err == nil {
		t.Fatal("schema ausente deve falhar fechado")
	}
	validador, err := nfse.NewXSDValidator(strings.NewReader("<xs:schema/>"))
	if err != nil {
		t.Fatal(err)
	}
	var comoValidador nfse.Validator = validador
	dps, err := nfse.BuildDPS(context.Background(), entradaBasica())
	if err != nil {
		t.Fatal(err)
	}
	resultado, err := comoValidador.ValidateDPS(context.Background(), dps)
	if err != nil || !resultado.Valid {
		t.Fatalf("validador XSD deve validar DPS canonica: %+v err=%v", resultado, err)
	}
}

func TestGoldenOitoCasos(t *testing.T) {
	casos, err := nfse.LoadGoldenCases(filepath.Join("testdata", "golden"))
	if err != nil {
		t.Fatal(err)
	}
	if len(casos) != 8 {
		t.Fatalf("esperados 8 golden cases, encontrados %d", len(casos))
	}
	validador := nfse.NewLocalValidator()
	for _, caso := range casos {
		t.Run(caso.Name, func(t *testing.T) {
			dps, err := nfse.BuildDPS(context.Background(), caso.Input)
			if err != nil {
				t.Fatal(err)
			}
			if dps.XMLHash != caso.ExpectedHash {
				t.Fatalf("hash divergente: got=%s want=%s", dps.XMLHash, caso.ExpectedHash)
			}
			resultado, err := validador.ValidateDPS(context.Background(), dps)
			if err != nil || !resultado.Valid != caso.ExpectInvalid {
				t.Fatalf("validacao divergente: %+v err=%v", resultado, err)
			}
			if !caso.ExpectInvalid {
				if err := os.WriteFile(filepath.Join(t.TempDir(), caso.Name+".xml"), dps.XML, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func entradaBasica() nfse.DPSInput {
	return nfse.DPSInput{
		IDDps: "DPS-BASE", Competence: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC),
		EmitterDocument: "00000000000191", MunicipalityCode: "2304400", Series: "1", Number: 1,
		Items: []nfse.DPSItem{{ServiceCode: "25.01", Description: "Serviço", Quantity: "1", UnitPriceCents: 100, TotalCents: 100}},
	}
}
