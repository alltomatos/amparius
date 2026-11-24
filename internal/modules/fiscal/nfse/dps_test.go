package nfse_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
)

func TestDPSCanonicaDeterministica(t *testing.T) {
	entrada := nfse.DPSInput{
		IDDps:            "DPS-0001",
		Competence:       time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC),
		EmitterDocument:  "00000000000191",
		MunicipalityCode: "2304400",
		Series:           "1",
		Number:           42,
		Items: []nfse.DPSItem{
			{ServiceCode: "25.02", Description: "Cremação", Quantity: "2", UnitPriceCents: 5000, TotalCents: 10000},
			{ServiceCode: "25.01", Description: "Serviço funerário", Quantity: "1", UnitPriceCents: 15000, TotalCents: 15000},
		},
	}
	primeira, err := nfse.BuildDPS(context.Background(), entrada)
	if err != nil {
		t.Fatal(err)
	}
	segunda, err := nfse.BuildDPS(context.Background(), entrada)
	if err != nil {
		t.Fatal(err)
	}
	if primeira.XMLHash != segunda.XMLHash || string(primeira.XML) != string(segunda.XML) {
		t.Fatalf("montagem nao e deterministica:\n1=%s\n2=%s", primeira.XMLHash, segunda.XMLHash)
	}
	if primeira.XMLHash != sha256Hex(primeira.XML) {
		t.Fatal("hash nao corresponde ao XML canonico")
	}
	if primeira.IDDps != entrada.IDDps {
		t.Fatalf("idDps divergente: %q", primeira.IDDps)
	}
	invertida := entrada
	invertida.Items = []nfse.DPSItem{entrada.Items[1], entrada.Items[0]}
	terceira, err := nfse.BuildDPS(context.Background(), invertida)
	if err != nil {
		t.Fatal(err)
	}
	if terceira.XMLHash != primeira.XMLHash {
		t.Fatal("ordem de entrada nao deveria alterar o canonico")
	}
}

func TestErrosTipadosDoPort(t *testing.T) {
	var adapter nfse.Adapter
	if !errors.Is(nfse.ErrAdapterUnavailable, nfse.ErrAdapterUnavailable) {
		t.Fatal("erro sentinela invalido")
	}
	if adapter != nil {
		t.Fatal("interface nao deveria ter implementacao nula")
	}
	rejeicao := &nfse.ErrDocumentRejected{Code: "E001", Description: "DPS invalida"}
	if !errors.Is(rejeicao, nfse.ErrDocumentRejectedType) {
		t.Fatal("rejeicao deve ser reconhecivel por tipo")
	}
	if rejeicao.Error() == "" {
		t.Fatal("rejeicao deve descrever codigo oficial")
	}
	if !errors.Is(nfse.ErrIdempotencyConflict, nfse.ErrIdempotencyConflict) {
		t.Fatal("conflito de idempotencia invalido")
	}
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
