package nfse_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
)

// Cobre o xs:choice de documento nos tres grupos de pessoa da DPS v1.01.
// Cada fixture sintetica usa o mesmo tipo em prest, toma e interm, de modo
// que um caso feliz prova a geracao do elemento correto uma vez por grupo.
// O facet de serie continua bloqueando o XSD: os testes nao mascaram esse
// gap nem exigem que a DPS inteira seja aceita.
func TestBuildDPSOficialChoiceDeDocumento(t *testing.T) {
	validador := novoValidadorOficial(t)
	casos := []struct {
		nome, fixture, tipo, prest, toma, interm string
	}{
		{"cnpj", "doc-cnpj.json", "CNPJ", "99999999999999", "88888888888888", "77777777777777"},
		{"cpf", "doc-cpf.json", "CPF", "99999999999", "88888888888", "77777777777"},
		{"nif", "doc-nif.json", "NIF", "SINT-NIF-PREST-0001", "SINT-NIF-TOMA-0002", "SINT-NIF-INTERM-0003"},
		{"cnaonif", "doc-cnaonif.json", "cNaoNIF", "2", "1", "0"},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			input := carregarFixtureOficialArquivo(t, caso.fixture)
			golden, err := os.ReadFile(filepath.Join("testdata", "official", caso.fixture[:len(caso.fixture)-len(".json")]+".xml"))
			if err != nil {
				t.Fatal(err)
			}
			dps, err := nfse.BuildDPSOficial(context.Background(), input)
			if err != nil {
				t.Fatalf("choice %s deveria ser aceito: %v", caso.tipo, err)
			}
			if string(dps.XML) != strings.TrimSpace(string(golden)) {
				t.Fatalf("XML divergiu do golden:\ngot:  %s\nwant: %s", dps.XML, golden)
			}
			if dps.IDDps != input.ID {
				t.Fatalf("Id divergente: %q != %q", dps.IDDps, input.ID)
			}
			if dps.XMLHash != nfse.SHA256Hex(dps.XML) {
				t.Fatal("hash nao corresponde ao XML")
			}

			xml := string(dps.XML)
			for _, grupo := range []struct{ tag, valor string }{
				{"prest", caso.prest},
				{"toma", caso.toma},
				{"interm", caso.interm},
			} {
				aberto := "<" + grupo.tag + "><" + caso.tipo + ">" + grupo.valor + "</" + caso.tipo + ">"
				if !strings.Contains(xml, aberto) {
					t.Fatalf("grupo %s deveria conter %s=%q: %s", grupo.tag, caso.tipo, grupo.valor, aberto)
				}
			}
			if ocorrencias := strings.Count(xml, "<"+caso.tipo+">"); ocorrencias != 3 {
				t.Fatalf("choice %s deveria aparecer uma vez por grupo (3), apareceu %d", caso.tipo, ocorrencias)
			}

			resultado, err := validador.ValidateDPS(context.Background(), dps)
			if err != nil {
				t.Fatal(err)
			}
			if resultado.Valid {
				t.Fatal("fixture nao deveria passar inteira: facet de serie continua bloqueando")
			}
			if len(resultado.Errors) == 0 {
				t.Fatal("diagnostico do XSD ausente")
			}
			for _, problema := range resultado.Errors {
				if !strings.Contains(problema, "serie") {
					t.Fatalf("choice %s gerou erro alem do bloqueio de serie: %v", caso.tipo, resultado.Errors)
				}
			}
		})
	}
}

// Bordas exatas dos facets: CNPJ 14, CPF 11, NIF 40 aceitos; NIF 41 e
// cNaoNIF fora de 0|1|2 falham fechado. Nenhuma regra de digito verificador
// e aplicada: o XSD oficial nao a define.
func TestBuildDPSOficialChoiceAceitaBordas(t *testing.T) {
	base := carregarFixtureOficialArquivo(t, "doc-cnpj.json")
	casos := []struct {
		nome, tipo, valor string
	}{
		{"CNPJ com 14 digitos", "CNPJ", "00000000000000"},
		{"CPF com 11 digitos", "CPF", "00000000000"},
		{"NIF com 40 caracteres", "NIF", strings.Repeat("N", 40)},
		{"cNaoNIF codigo 0", "cNaoNIF", "0"},
		{"cNaoNIF codigo 1", "cNaoNIF", "1"},
		{"cNaoNIF codigo 2", "cNaoNIF", "2"},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			copia := clonarOfficialDPSInput(t, base)
			copia.Prestador.DocumentoTipo = caso.tipo
			copia.Prestador.Documento = caso.valor
			if _, err := nfse.BuildDPSOficial(context.Background(), copia); err != nil {
				t.Fatalf("borda de %s deveria ser aceita: %v", caso.tipo, err)
			}
		})
	}
}

// Cada grupo (prest, toma, interm) rejeita, fail-closed, um valor que viola o
// facet do tipo escolhido ou um tipo fora do xs:choice.
func TestBuildDPSOficialChoiceRejeitaDocumentoInvalido(t *testing.T) {
	valores := []struct {
		nome, tipo, valor string
	}{
		{"CNPJ com 13 digitos", "CNPJ", "1234567890123"},
		{"CNPJ com 15 digitos", "CNPJ", "123456789012345"},
		{"CNPJ alfanumerico", "CNPJ", "1234567890123A"},
		{"CPF com 10 digitos", "CPF", "1234567890"},
		{"CPF com 12 digitos", "CPF", "123456789012"},
		{"CPF alfanumerico", "CPF", "1234567890A"},
		{"NIF com 41 caracteres", "NIF", strings.Repeat("N", 41)},
		{"cNaoNIF fora da enumeracao", "cNaoNIF", "3"},
		{"cNaoNIF alfabetico", "cNaoNIF", "A"},
		{"tipo fora do choice", "RG", "sintetico"},
	}
	grupos := []struct {
		nome, fixture string
		aplicar       func(*nfse.OfficialDPSInput, string, string)
	}{
		{"prest", "doc-cnpj.json", func(i *nfse.OfficialDPSInput, tipo, valor string) {
			i.Prestador.DocumentoTipo = tipo
			i.Prestador.Documento = valor
		}},
		{"toma", "doc-cpf.json", func(i *nfse.OfficialDPSInput, tipo, valor string) {
			i.Tomador = &nfse.OfficialPessoa{DocumentoTipo: tipo, Documento: valor, Nome: "Tomador sintetico"}
		}},
		{"interm", "doc-nif.json", func(i *nfse.OfficialDPSInput, tipo, valor string) {
			i.Intermediario = &nfse.OfficialPessoa{DocumentoTipo: tipo, Documento: valor, Nome: "Intermediario sintetico"}
		}},
	}
	for _, grupo := range grupos {
		for _, caso := range valores {
			t.Run(grupo.nome+"/"+caso.nome, func(t *testing.T) {
				input := carregarFixtureOficialArquivo(t, grupo.fixture)
				grupo.aplicar(&input, caso.tipo, caso.valor)
				if _, err := nfse.BuildDPSOficial(context.Background(), input); !errors.Is(err, nfse.ErrInvalidDPS) {
					t.Fatalf("valor invalido em %s deveria falhar com ErrInvalidDPS: %v", grupo.nome, err)
				}
			})
		}
	}
}

// Ramo opcional continua exigindo documento: uma pessoa com tipo valido mas
// sem valor nao pode passar como se o choice estivesse preenchido.
func TestBuildDPSOficialChoiceRejeitaDocumentoVazio(t *testing.T) {
	for _, grupo := range []struct {
		nome, fixture string
		aplicar       func(*nfse.OfficialDPSInput, string)
	}{
		{"prest", "doc-cnpj.json", func(i *nfse.OfficialDPSInput, tipo string) {
			i.Prestador.DocumentoTipo = tipo
			i.Prestador.Documento = ""
		}},
		{"toma", "doc-cnpj.json", func(i *nfse.OfficialDPSInput, tipo string) {
			i.Tomador = &nfse.OfficialPessoa{DocumentoTipo: tipo, Nome: "Tomador sintetico"}
		}},
		{"interm", "doc-cnpj.json", func(i *nfse.OfficialDPSInput, tipo string) {
			i.Intermediario = &nfse.OfficialPessoa{DocumentoTipo: tipo, Nome: "Intermediario sintetico"}
		}},
	} {
		for _, tipo := range []string{"CNPJ", "CPF", "NIF", "cNaoNIF"} {
			t.Run(grupo.nome+"/"+tipo, func(t *testing.T) {
				input := carregarFixtureOficialArquivo(t, grupo.fixture)
				grupo.aplicar(&input, tipo)
				if _, err := nfse.BuildDPSOficial(context.Background(), input); !errors.Is(err, nfse.ErrInvalidDPS) {
					t.Fatalf("documento vazio em %s deveria falhar com ErrInvalidDPS: %v", grupo.nome, err)
				}
			})
		}
	}
}
