package nfse_test

import (
	"context"
	"encoding/json"
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
