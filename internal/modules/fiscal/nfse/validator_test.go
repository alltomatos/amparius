package nfse_test

import (
	"context"
	"errors"
	"fmt"
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

// Caminho do pacote oficial relativo a raiz do repositorio. Nenhum download:
// os arquivos ja estao versionados e o MANIFEST confere os 10 hashes + ZIP.
var xsdRootRel = filepath.Join("..", "..", "..", "..", "docs", "fiscal", "xsd-nfse-v1.01", "Schemas", "1.01")

const xsdEntry = "DPS_v1.01.xsd"

func novoValidadorOficial(t *testing.T) *nfse.XSDValidator {
	t.Helper()
	validador, err := nfse.NewXSDValidator(xsdRootRel, xsdEntry)
	if err != nil {
		t.Fatalf("carregar DPS_v1.01.xsd: %v", err)
	}
	return validador
}

func TestXSDOficialCarregaIncludesEImportsSemRede(t *testing.T) {
	validador := novoValidadorOficial(t)
	if !validador.HasElement("DPS") {
		t.Fatal("elemento global DPS nao foi compilado do schema oficial")
	}
	arquivos := validador.SchemaFiles()
	if len(arquivos) < 3 {
		t.Fatalf("includes/imports nao resolvidos: %d arquivo(s): %v", len(arquivos), arquivos)
	}
	var temComplexos, temSimples, temDSig bool
	for _, a := range arquivos {
		base := filepath.Base(a)
		switch base {
		case "tiposComplexos_v1.01.xsd":
			temComplexos = true
		case "tiposSimples_v1.01.xsd":
			temSimples = true
		case "xmldsig-core-schema.xsd":
			temDSig = true
		}
		if strings.Contains(a, "http") || strings.Contains(a, "://") {
			t.Fatalf("schema remoto carregado: %s", a)
		}
	}
	if !temComplexos || !temSimples || !temDSig {
		t.Fatalf("grafo incompleto: complexos=%v simples=%v dsig=%v arquivos=%v", temComplexos, temSimples, temDSig, arquivos)
	}
	for _, a := range arquivos {
		info, err := os.Stat(a)
		if err != nil || info.IsDir() {
			t.Fatalf("schema local inexistente: %s (%v)", a, err)
		}
	}
}

func TestXSDOficialDetectaEstruturaInvalida(t *testing.T) {
	validador := novoValidadorOficial(t)
	casos := []struct {
		nome string
		xml  string
	}{
		{"sem versao", `<DPS xmlns="http://www.sped.fazenda.gov.br/nfse"><infDPS Id="DPS2304400100000000000191000010000000001"/></DPS>`},
		{"sem infDPS", `<DPS xmlns="http://www.sped.fazenda.gov.br/nfse" versao="1.01"><idDps>x</idDps></DPS>`},
		{"namespace errado", `<DPS versao="1.01"><infDPS Id="x"/></DPS>`},
		{"elemento nao previsto", `<DPS xmlns="http://www.sped.fazenda.gov.br/nfse" versao="1.01"><infDPS Id="x"/><extra/></DPS>`},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			resultado, err := validador.ValidateDPS(context.Background(), nfse.DPS{XML: []byte(c.xml), IDDps: "x"})
			if err != nil {
				t.Fatalf("validacao nao deve falhar por erro de transporte: %v", err)
			}
			if resultado.Valid {
				t.Fatalf("validador aceitou XML invalido (%s): %+v", c.nome, resultado)
			}
			if len(resultado.Errors) == 0 {
				t.Fatal("diagnostico ausente")
			}
		})
	}
}

func TestXSDOficialVersaoDosSchemas(t *testing.T) {
	validador := novoValidadorOficial(t)
	resultado, err := validador.ValidateDPS(context.Background(), nfse.DPS{
		XML: []byte(`<DPS xmlns="http://www.sped.fazenda.gov.br/nfse" versao="9.99"><infDPS Id="DPS2304400100000000000191000010000000001"/></DPS>`),
	})
	if err != nil || resultado.Valid {
		t.Fatalf("versao 9.99 deveria ser rejeitada pelo TVerNFSe: %+v err=%v", resultado, err)
	}
	if !strings.Contains(strings.Join(resultado.Errors, " "), "pattern") {
		t.Fatalf("erro deveria citar restricao de pattern/enumeration: %v", resultado.Errors)
	}
}

// Prova que o compilador XSD realmente desce em TCDPS -> TCInfDPS e aplica
// as restricoes de TSIdDPS/TSData, em vez de rejeitar tudo cegamente. Nao
// montamos uma DPS completa: tributos/codigos oficiais sao gate da fatia 02.
func TestXSDOficialCompilaTiposAninhados(t *testing.T) {
	validador := novoValidadorOficial(t)
	base := `<DPS xmlns="http://www.sped.fazenda.gov.br/nfse" versao="1.01"><infDPS Id="%s"><tpAmb>2</tpAmb></infDPS></DPS>`
	comIdValido, err := validador.ValidateDPS(context.Background(), nfse.DPS{
		XML: []byte(fmt.Sprintf(base, "DPS000000000000000000000000000000000000000000")),
	})
	if err != nil {
		t.Fatal(err)
	}
	comIdInvalido, err := validador.ValidateDPS(context.Background(), nfse.DPS{
		XML: []byte(fmt.Sprintf(base, "ID-INVALIDO")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if comIdValido.Valid || comIdInvalido.Valid {
		t.Fatal("instancias parciais nao devem ser consideradas DPS completa")
	}
	errosValidos := strings.Join(comIdValido.Errors, " ")
	errosInvalidos := strings.Join(comIdInvalido.Errors, " ")
	if !strings.Contains(errosValidos, "infDPS") {
		t.Fatalf("TCInfDPS nao foi compilado (sem diagnostico dos obrigatorios): %v", comIdValido.Errors)
	}
	if strings.Contains(errosValidos, "pattern") {
		t.Fatalf("Id valido nao deveria falhar por pattern: %v", comIdValido.Errors)
	}
	if !strings.Contains(errosInvalidos, "pattern") {
		t.Fatalf("TSIdDPS nao foi aplicado ao Id invalido: %v", comIdInvalido.Errors)
	}
}

// TestGoldenContraXSDOficial reporta honestamente quantos dos 8 golden
// validam contra o schema oficial v1.01. O builder da fatia 01 gera DPS
// estrutural (idDps/itens/tributos em centavos), que NAO e o leiaute oficial
// (infDPS/serv/valores). Nao forcamos verde: cada caso incompativel recebe
// erro testavel ErrXSDIncompatible e o gap fica documentado.
func TestGoldenContraXSDOficial(t *testing.T) {
	validador := novoValidadorOficial(t)
	casos, err := nfse.LoadGoldenCases(filepath.Join("testdata", "golden"))
	if err != nil {
		t.Fatal(err)
	}
	if len(casos) != 8 {
		t.Fatalf("esperados 8 golden, encontrados %d", len(casos))
	}
	validos := 0
	for _, caso := range casos {
		dps, err := nfse.BuildDPS(context.Background(), caso.Input)
		if err != nil {
			t.Fatal(err)
		}
		resultado, err := validador.ValidateDPS(context.Background(), dps)
		if err != nil {
			t.Fatalf("%s: %v", caso.Name, err)
		}
		if resultado.Valid {
			validos++
			continue
		}
		if classificado := nfse.ClassifyXSDIncompatibility(resultado); !errors.Is(classificado, nfse.ErrXSDIncompatible) {
			t.Fatalf("%s: incompatibilidade nao classificada: %v", caso.Name, classificado)
		}
		if len(resultado.Errors) == 0 {
			t.Fatalf("%s: incompatibilidade sem diagnostico", caso.Name)
		}
	}
	t.Logf("golden validos contra XSD oficial v1.01: %d/%d (incompativeis: %d)", validos, len(casos), len(casos)-validos)
	if validos != 0 {
		t.Fatalf("resultado inesperado: %d golden validos; revisar builder/fixtures antes de afirmar conformidade", validos)
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
