package nfse_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
)

// O TSIdDPS do leiaute NT 009 tem 45 posicoes:
// "DPS" + Cod.Mun(7) + Tipo de Inscricao(1) + Inscricao(14) + Serie(5) + Num.DPS(15).
func TestBuildDPSIDFormacaoOficial(t *testing.T) {
	casos := []struct {
		nome                             string
		cLocEmi, tpInsc, insc, serie, nD string
		esperado                         string
	}{
		{
			"CNPJ", "2304400", "2", "00000000000191", "1", "1",
			"DPS230440020000000000019100001000000000000001",
		},
		{
			"CPF com 000 a esquerda", "2304400", "1", "12345678901", "7", "12",
			"DPS230440010001234567890100007000000000000012",
		},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			id, err := nfse.BuildDPSID(caso.cLocEmi, caso.tpInsc, caso.insc, caso.serie, caso.nD)
			if err != nil {
				t.Fatal(err)
			}
			if id != caso.esperado {
				t.Fatalf("Id divergente: %s != %s", id, caso.esperado)
			}
			if len(id) != 45 {
				t.Fatalf("Id deveria ter 45 posicoes: %d", len(id))
			}
		})
	}
}

func TestBuildDPSIDRejeitaFormaInvalida(t *testing.T) {
	casos := []struct {
		nome                             string
		cLocEmi, tpInsc, insc, serie, nD string
	}{
		{"municipio com 6 digitos", "230440", "2", "00000000000191", "1", "1"},
		{"tipo de inscricao invalido", "2304400", "3", "00000000000191", "1", "1"},
		{"CPF com 10 digitos", "2304400", "1", "1234567890", "1", "1"},
		{"CNPJ com 13 caracteres", "2304400", "2", "0000000000019", "1", "1"},
		{"serie de 5 digitos iniciada em 9", "2304400", "2", "00000000000191", "99999", "1"},
		{"serie com 6 digitos", "2304400", "2", "00000000000191", "000001", "1"},
		{"nDPS zero", "2304400", "2", "00000000000191", "1", "0"},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			if _, err := nfse.BuildDPSID(caso.cLocEmi, caso.tpInsc, caso.insc, caso.serie, caso.nD); !errors.Is(err, nfse.ErrInvalidDPS) {
				t.Fatalf("esperado ErrInvalidDPS, veio %v", err)
			}
		})
	}
}

func TestBuildDPSOficialRejeitaIDIncoerenteComPrestador(t *testing.T) {
	input := carregarFixtureOficial(t)
	input.ID = "DPS230440020000000000019100001000000000000009"
	if _, err := nfse.BuildDPSOficial(context.Background(), input); !errors.Is(err, nfse.ErrInvalidDPS) {
		t.Fatalf("Id incoerente deveria falhar: %v", err)
	}
}

func TestBuildDPSOficialExigeFinNFSeDaEnumeracao(t *testing.T) {
	for _, valor := range []string{"", "1", "9", "00"} {
		input := carregarFixtureOficial(t)
		input.FinNFSe = valor
		if _, err := nfse.BuildDPSOficial(context.Background(), input); !errors.Is(err, nfse.ErrInvalidDPS) {
			t.Fatalf("finNFSe %q deveria falhar fechado: %v", valor, err)
		}
	}
}

// Serie alfanumerica nao passa no TSSerieDPS e o builder falha antes de montar
// o XML; o XSD continua sendo a autoridade final.
func TestBuildDPSOficialRejeitaSerieForaDoTSSerieDPS(t *testing.T) {
	input := carregarFixtureOficial(t)
	input.Serie = "AB"
	if _, err := nfse.BuildDPSOficial(context.Background(), input); !errors.Is(err, nfse.ErrInvalidDPS) {
		t.Fatalf("serie alfanumerica deveria falhar: %v", err)
	}
	if strings.Contains(input.ID, "AB") {
		t.Fatal("fixture nao deveria conter serie alfanumerica no Id")
	}
}
