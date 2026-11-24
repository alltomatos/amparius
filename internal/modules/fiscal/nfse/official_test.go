package nfse_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
)

func carregarFixtureOficial(t *testing.T) nfse.OfficialDPSInput {
	return carregarFixtureOficialArquivo(t, "dps-minima.json")
}

func carregarFixtureOficialArquivo(t *testing.T, nome string) nfse.OfficialDPSInput {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "official", nome))
	if err != nil {
		t.Fatal(err)
	}
	var input nfse.OfficialDPSInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	return input
}

func TestBuildDPSOficialComRamosOpcionaisPreservaGapDeSerie(t *testing.T) {
	validador := novoValidadorOficial(t)
	input := carregarFixtureOficialArquivo(t, "dps-opcionais.json")
	esperado, err := os.ReadFile(filepath.Join("testdata", "official", "dps-opcionais.xml"))
	if err != nil {
		t.Fatal(err)
	}
	dps, err := nfse.BuildDPSOficial(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if string(dps.XML) != strings.TrimSpace(string(esperado)) {
		t.Fatalf("XML oficial opcional divergiu do golden:\ngot:  %s\nwant: %s", dps.XML, esperado)
	}
	resultado, err := validador.ValidateDPS(context.Background(), dps)
	if err != nil {
		t.Fatal(err)
	}
	if resultado.Valid || !strings.Contains(strings.Join(resultado.Errors, " "), "serie") {
		t.Fatalf("gap de serie do XSD deveria ser diagnosticado sem mascarar os ramos opcionais: %v", resultado.Errors)
	}
	for _, elemento := range []string{"<toma>", "<interm>", "<infoCompl>", "<idDocTec>", "<docRef>", "<xPed>", "<xInfComp>"} {
		if !strings.Contains(string(dps.XML), elemento) {
			t.Fatalf("ramo opcional ausente: %s", elemento)
		}
	}
}

func TestBuildDPSOficialRejeitaRamosOpcionaisIncompletos(t *testing.T) {
	input := carregarFixtureOficialArquivo(t, "dps-opcionais.json")
	testes := []struct {
		nome  string
		mutar func(*nfse.OfficialDPSInput)
	}{
		{"tomador sem documento", func(i *nfse.OfficialDPSInput) {
			i.Tomador = &nfse.OfficialPessoa{DocumentoTipo: "CNPJ", Nome: "Tomador sintetico"}
		}},
		{"tomador sem nome", func(i *nfse.OfficialDPSInput) {
			i.Tomador = &nfse.OfficialPessoa{DocumentoTipo: "CNPJ", Documento: "00000000000272"}
		}},
		{"intermediario com choice invalido", func(i *nfse.OfficialDPSInput) {
			i.Intermediario = &nfse.OfficialPessoa{DocumentoTipo: "RG", Documento: "sintetico", Nome: "Intermediario sintetico"}
		}},
		{"infoCompl vazio", func(i *nfse.OfficialDPSInput) {
			i.Servico.InfoCompl = &nfse.OfficialInfoComplementar{}
		}},
	}
	for _, teste := range testes {
		t.Run(teste.nome, func(t *testing.T) {
			copia := clonarOfficialDPSInput(t, input)
			teste.mutar(&copia)
			_, err := nfse.BuildDPSOficial(context.Background(), copia)
			if !errors.Is(err, nfse.ErrInvalidDPS) {
				t.Fatalf("erro fail-closed esperado: %v", err)
			}
		})
	}
}

func TestBuildDPSOficialValidaNomeDeTomaEIntermNosLimitesXSD(t *testing.T) {
	validador := novoValidadorOficial(t)
	casos := []struct {
		grupo  string
		nome   string
		aceita bool
	}{
		{"toma", strings.Repeat("A", 1), true},
		{"toma", strings.Repeat("A", 300), true},
		{"toma", strings.Repeat("A", 301), false},
		{"toma", "", false},
		{"interm", strings.Repeat("B", 1), true},
		{"interm", strings.Repeat("B", 300), true},
		{"interm", strings.Repeat("B", 301), false},
		{"interm", "", false},
	}
	for _, caso := range casos {
		t.Run(caso.grupo+"/"+nomeLimite(caso.nome), func(t *testing.T) {
			input := carregarFixtureOficial(t)
			pessoa := &nfse.OfficialPessoa{
				DocumentoTipo: "CNPJ",
				Documento:     "00000000000272",
				Nome:          caso.nome,
			}
			if caso.grupo == "toma" {
				input.Tomador = pessoa
			} else {
				input.Intermediario = pessoa
			}

			dps, err := nfse.BuildDPSOficial(context.Background(), input)
			if caso.aceita {
				if err != nil {
					t.Fatalf("nome de %d caracteres deveria ser aceito: %v", len([]rune(caso.nome)), err)
				}
				// Neutraliza somente o bloqueio de serie documentado: assim o
				// caso de borda tambem prova a aceitacao pelo XSD v1.01 local.
				xml := strings.Replace(string(dps.XML), "<serie>1</serie>", "<serie>^1$</serie>", 1)
				resultado, err := validador.ValidateDPS(context.Background(), nfse.DPS{XML: []byte(xml), IDDps: dps.IDDps})
				if err != nil || !resultado.Valid {
					t.Fatalf("nome de %d caracteres deveria passar no XSD: %+v err=%v", len([]rune(caso.nome)), resultado, err)
				}
				return
			}
			if !errors.Is(err, nfse.ErrInvalidDPS) {
				t.Fatalf("nome de %d caracteres deveria falhar com ErrInvalidDPS: %v", len([]rune(caso.nome)), err)
			}
		})
	}
}

func TestBuildDPSOficialValidaXDescServNosLimitesTSDesc2000(t *testing.T) {
	validador := novoValidadorOficial(t)
	casos := []struct {
		nome      string
		descricao string
		aceita    bool
	}{
		{"um rune", "A", true},
		{"2000 runas", strings.Repeat("A", 2000), true},
		{"2001 runas", strings.Repeat("A", 2001), false},
		{"2000 runas unicode", strings.Repeat("á", 2000), true},
		{"2001 runas unicode", strings.Repeat("á", 2001), false},
		{"quebra de linha", "linha 1\nlinha 2", true},
		{"vazio", "", false},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			input := carregarFixtureOficial(t)
			input.Servico.XDescServ = caso.descricao
			dps, err := nfse.BuildDPSOficial(context.Background(), input)
			if !caso.aceita {
				if !errors.Is(err, nfse.ErrInvalidDPS) {
					t.Fatalf("xDescServ com %d runas deveria falhar com ErrInvalidDPS: %v", len([]rune(caso.descricao)), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("xDescServ com %d runas deveria ser aceito: %v", len([]rune(caso.descricao)), err)
			}

			// Neutraliza somente o bloqueio conhecido de serie; o caso continua
			// provando o facet TSDesc2000 no XSD v1.01 local.
			xml := strings.Replace(string(dps.XML), "<serie>1</serie>", "<serie>^1$</serie>", 1)
			resultado, err := validador.ValidateDPS(context.Background(), nfse.DPS{XML: []byte(xml), IDDps: dps.IDDps})
			if err != nil || !resultado.Valid {
				t.Fatalf("xDescServ com %d runas deveria passar no XSD: %+v err=%v", len([]rune(caso.descricao)), resultado, err)
			}
		})
	}
}

func TestBuildDPSOficialValidaFormatoDeCTribNac(t *testing.T) {
	validador := novoValidadorOficial(t)
	cases := []struct {
		nome   string
		valor  string
		aceita bool
	}{
		{"seis digitos", "123456", true},
		{"zeros a esquerda", "000001", true},
		{"cinco digitos", "12345", false},
		{"sete digitos", "1234567", false},
		{"espaco na borda esquerda", " 12345", false},
		{"espaco na borda direita", "12345 ", false},
		{"tab interno", "123\t56", false},
		{"quebra de linha", "12345\n", false},
		{"letra ASCII", "12345A", false},
		{"digito unicode", "12345٦", false},
		{"digito fullwidth", "12345６", false},
	}

	for _, caso := range cases {
		t.Run(caso.nome, func(t *testing.T) {
			input := carregarFixtureOficial(t)
			input.Servico.CTribNac = caso.valor
			dps, err := nfse.BuildDPSOficial(context.Background(), input)
			if !caso.aceita {
				if !errors.Is(err, nfse.ErrInvalidDPS) {
					t.Fatalf("cTribNac %q deveria falhar fechado: %v", caso.valor, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("cTribNac %q deveria ser aceito: %v", caso.valor, err)
			}
			if !strings.Contains(string(dps.XML), "<cTribNac>"+caso.valor+"</cTribNac>") {
				t.Fatalf("cTribNac %q nao foi preservado no XML", caso.valor)
			}

			// Neutraliza somente o bloqueio conhecido de serie; o caso continua
			// provando o pattern TSCodTribNac no XSD v1.01 local.
			xml := strings.Replace(string(dps.XML), "<serie>1</serie>", "<serie>^1$</serie>", 1)
			resultado, err := validador.ValidateDPS(context.Background(), nfse.DPS{XML: []byte(xml), IDDps: dps.IDDps})
			if err != nil || !resultado.Valid {
				t.Fatalf("cTribNac %q deveria passar no XSD: %+v err=%v", caso.valor, resultado, err)
			}
		})
	}
}

func TestBuildDPSOficialValidaFacetsTextuaisDeInfoCompl(t *testing.T) {
	validador := novoValidadorOficial(t)
	cases := []struct {
		nome     string
		campo    string
		valor    string
		aceita   bool
		preserva bool
	}{
		{"idDocTec minimo", "idDocTec", "A", true, false},
		{"idDocTec maximo em runas", "idDocTec", strings.Repeat("\u00e1", 40), true, false},
		{"idDocTec acima do maximo", "idDocTec", strings.Repeat("A", 41), false, false},
		{"idDocTec borda esquerda", "idDocTec", " A", true, false},
		{"idDocTec borda direita", "idDocTec", "A ", true, false},
		{"idDocTec tab e quebra de linha", "idDocTec", "A\tB\nC", true, false},
		{"docRef minimo", "docRef", "A", true, false},
		{"docRef maximo em runas", "docRef", strings.Repeat("\u00e1", 255), true, false},
		{"docRef acima do maximo", "docRef", strings.Repeat("A", 256), false, false},
		{"docRef tab interno", "docRef", "A\tB", false, false},
		{"xInfComp minimo", "xInfComp", "A", true, false},
		{"xInfComp maximo em runas", "xInfComp", strings.Repeat("\u00e1", 2000), true, false},
		{"xInfComp acima do maximo", "xInfComp", strings.Repeat("A", 2001), false, false},
		{"xInfComp espacos internos preservados", "xInfComp", "A  B", true, true},
		{"xInfComp quebra de linha proibida", "xInfComp", "A\nB", false, false},
		{"xInfComp borda esquerda", "xInfComp", " A", false, false},
		{"xInfComp borda direita", "xInfComp", "A ", false, false},
		{"xPed maximo em runas", "xPed", strings.Repeat("\u00e1", 60), true, false},
		{"xPed acima do maximo", "xPed", strings.Repeat("A", 61), false, false},
	}

	for _, caso := range cases {
		t.Run(caso.nome, func(t *testing.T) {
			input := carregarFixtureOficial(t)
			input.Servico.InfoCompl = &nfse.OfficialInfoComplementar{}
			switch caso.campo {
			case "idDocTec":
				input.Servico.InfoCompl.IDDocTec = caso.valor
			case "docRef":
				input.Servico.InfoCompl.DocRef = caso.valor
			case "xPed":
				input.Servico.InfoCompl.XPed = caso.valor
			case "xInfComp":
				input.Servico.InfoCompl.XInfComp = caso.valor
			default:
				t.Fatalf("campo de teste desconhecido: %s", caso.campo)
			}

			dps, err := nfse.BuildDPSOficial(context.Background(), input)
			if !caso.aceita {
				if !errors.Is(err, nfse.ErrInvalidDPS) {
					t.Fatalf("%s com %d runas deveria falhar fechado: %v", caso.campo, len([]rune(caso.valor)), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s com %d runas deveria ser aceito: %v", caso.campo, len([]rune(caso.valor)), err)
			}
			var valorXML strings.Builder
			if err := xml.EscapeText(&valorXML, []byte(caso.valor)); err != nil {
				t.Fatalf("falha ao escapar valor de %s: %v", caso.campo, err)
			}
			marcador := "<" + caso.campo + ">" + valorXML.String() + "</" + caso.campo + ">"
			if !strings.Contains(string(dps.XML), marcador) {
				t.Fatalf("valor de %s nao foi preservado no XML", caso.campo)
			}
			if caso.preserva && !strings.Contains(string(dps.XML), "<xInfComp>A  B</xInfComp>") {
				t.Fatal("whitespace interno de xInfComp foi normalizado")
			}

			// Neutraliza somente o bloqueio conhecido de serie; o caso continua
			// provando o facet textual correspondente no XSD v1.01 local.
			xml := strings.Replace(string(dps.XML), "<serie>1</serie>", "<serie>^1$</serie>", 1)
			resultado, err := validador.ValidateDPS(context.Background(), nfse.DPS{XML: []byte(xml), IDDps: dps.IDDps})
			if err != nil || !resultado.Valid {
				t.Fatalf("%s com %d runas deveria passar no XSD: %+v err=%v", caso.campo, len([]rune(caso.valor)), resultado, err)
			}
		})
	}
}

func nomeLimite(nome string) string {
	switch len([]rune(nome)) {
	case 0:
		return "vazio"
	case 301:
		return "301"
	default:
		return fmt.Sprintf("%d", len([]rune(nome)))
	}
}

func clonarOfficialDPSInput(t *testing.T, input nfse.OfficialDPSInput) nfse.OfficialDPSInput {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var copia nfse.OfficialDPSInput
	if err := json.Unmarshal(raw, &copia); err != nil {
		t.Fatal(err)
	}
	return copia
}

func TestBuildDPSOficialDetectaSerieIncompativelComXSD(t *testing.T) {
	validador := novoValidadorOficial(t)
	input := carregarFixtureOficial(t)
	dps, err := nfse.BuildDPSOficial(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if dps.IDDps != input.ID {
		t.Fatalf("Id divergente: %q != %q", dps.IDDps, input.ID)
	}
	if dps.XMLHash != nfse.SHA256Hex(dps.XML) {
		t.Fatal("hash nao corresponde ao XML")
	}
	resultado, err := validador.ValidateDPS(context.Background(), dps)
	if err != nil {
		t.Fatal(err)
	}
	if resultado.Valid || !strings.Contains(strings.Join(resultado.Errors, " "), "serie") {
		t.Fatalf("serie=1 deveria ser rejeitada pelo facet oficial: %v", resultado.Errors)
	}
	if !strings.Contains(string(dps.XML), `<infDPS Id="`) {
		t.Fatal("XML oficial deve usar infDPS com Id")
	}
}

func TestGoldenOficialContraXSD(t *testing.T) {
	validador := novoValidadorOficial(t)
	input := carregarFixtureOficial(t)
	esperado, err := os.ReadFile(filepath.Join("testdata", "official", "dps-minima.xml"))
	if err != nil {
		t.Fatal(err)
	}
	esperado = []byte(strings.TrimSpace(string(esperado)))
	dps, err := nfse.BuildDPSOficial(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if string(dps.XML) != string(esperado) {
		t.Fatalf("XML oficial divergiu do golden:\ngot:  %s\nwant: %s", dps.XML, esperado)
	}
	if dps.XMLHash != nfse.SHA256Hex(esperado) {
		t.Fatal("hash do golden oficial divergente")
	}
	resultado, err := validador.ValidateDPS(context.Background(), dps)
	if err != nil {
		t.Fatal(err)
	}
	if resultado.Valid || !strings.Contains(strings.Join(resultado.Errors, " "), "serie") {
		t.Fatalf("golden oficial deveria manter o bloqueio do facet de serie: %+v", resultado)
	}
	t.Logf("casos oficiais aceitos pelo XSD v1.01: 0/1; bloqueio documentado no facet TSSerieDPS")
}

func TestBuildDPSOficialDeterministico(t *testing.T) {
	input := carregarFixtureOficial(t)
	a, err := nfse.BuildDPSOficial(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	b, err := nfse.BuildDPSOficial(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if a.XMLHash != b.XMLHash || string(a.XML) != string(b.XML) {
		t.Fatal("builder oficial nao e deterministico")
	}
}

func TestBuildDPSOficialRejeitaEntradaIncompleta(t *testing.T) {
	input := carregarFixtureOficial(t)
	casos := []struct {
		nome  string
		mutar func(*nfse.OfficialDPSInput)
	}{
		{"sem Id", func(i *nfse.OfficialDPSInput) { i.ID = "" }},
		{"sem versao", func(i *nfse.OfficialDPSInput) { i.Versao = "" }},
		{"sem tpAmb", func(i *nfse.OfficialDPSInput) { i.TpAmb = "" }},
		{"sem dhEmi", func(i *nfse.OfficialDPSInput) { i.DhEmi = "" }},
		{"sem verAplic", func(i *nfse.OfficialDPSInput) { i.VerAplic = "" }},
		{"sem serie", func(i *nfse.OfficialDPSInput) { i.Serie = "" }},
		{"sem nDPS", func(i *nfse.OfficialDPSInput) { i.NDPS = "" }},
		{"sem dCompet", func(i *nfse.OfficialDPSInput) { i.DCompet = "" }},
		{"sem tpEmit", func(i *nfse.OfficialDPSInput) { i.TpEmit = "" }},
		{"sem cLocEmi", func(i *nfse.OfficialDPSInput) { i.CLocEmi = "" }},
		{"sem documento prestador", func(i *nfse.OfficialDPSInput) { i.Prestador.Documento = "" }},
		{"sem tipo documento", func(i *nfse.OfficialDPSInput) { i.Prestador.DocumentoTipo = "" }},
		{"sem opSimpNac", func(i *nfse.OfficialDPSInput) { i.Prestador.OpSimpNac = "" }},
		{"sem regEspTrib", func(i *nfse.OfficialDPSInput) { i.Prestador.RegEspTrib = "" }},
		{"sem cLocPrestacao", func(i *nfse.OfficialDPSInput) { i.Servico.CLocPrestacao = "" }},
		{"sem cTribNac", func(i *nfse.OfficialDPSInput) { i.Servico.CTribNac = "" }},
		{"sem xDescServ", func(i *nfse.OfficialDPSInput) { i.Servico.XDescServ = "" }},
		{"sem vServ", func(i *nfse.OfficialDPSInput) { i.Valores.VServ = "" }},
		{"sem tribISSQN", func(i *nfse.OfficialDPSInput) { i.Valores.TribISSQN = "" }},
		{"sem tpRetISSQN", func(i *nfse.OfficialDPSInput) { i.Valores.TpRetISSQN = "" }},
		{"sem indTotTrib", func(i *nfse.OfficialDPSInput) { i.Valores.IndTotTrib = "" }},
		{"tipo documento invalido", func(i *nfse.OfficialDPSInput) { i.Prestador.DocumentoTipo = "RG" }},
		{"contexto cancelado", nil},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			copia := clonarOfficialDPSInput(t, input)
			if c.mutar != nil {
				c.mutar(&copia)
			}
			ctx := context.Background()
			if c.mutar == nil {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := nfse.BuildDPSOficial(ctx, copia)
			if err == nil {
				t.Fatal("entrada invalida deveria falhar fechado")
			}
			if c.mutar == nil {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("contexto cancelado: %v", err)
				}
				return
			}
			if !errors.Is(err, nfse.ErrInvalidDPS) {
				t.Fatalf("erro tipado esperado: %v", err)
			}
		})
	}
}

func TestBuildDPSOficialNaoMascaraBuilderEstrutural(t *testing.T) {
	// O builder antigo continua produzindo XML incompativel; a API nova nao
	// deve alterar esse resultado nem os 8 golden antigos.
	validador := novoValidadorOficial(t)
	casos, err := nfse.LoadGoldenCases(filepath.Join("testdata", "golden"))
	if err != nil {
		t.Fatal(err)
	}
	incompativeis := 0
	for _, caso := range casos {
		estrutural, err := nfse.BuildDPS(context.Background(), caso.Input)
		if err != nil {
			t.Fatal(err)
		}
		resultado, err := validador.ValidateDPS(context.Background(), estrutural)
		if err != nil {
			t.Fatal(err)
		}
		if !resultado.Valid {
			incompativeis++
		}
	}
	if incompativeis != len(casos) {
		t.Fatalf("incompatibilidades antigas foram mascaradas: %d/%d continuam incompativeis", incompativeis, len(casos))
	}
}
