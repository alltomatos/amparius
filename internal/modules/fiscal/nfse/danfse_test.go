package nfse_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
	"github.com/google/uuid"
)

func TestFakeDANFSeDeterministico(t *testing.T) {
	renderer := nfse.NewFakeDANFSeRenderer()
	request := nfse.DANFSeRequest{
		TenantID: uuid.New(), CompanyID: uuid.New(), EstablishmentID: uuid.New(),
		AccessKey: "NFS-TESTE", IDDps: "DPS-DANFSE", Environment: nfse.EnvironmentProducaoRestrita,
		Competence: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC),
		IssuedAt:   time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC),
	}
	primeiro, err := renderer.Render(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	segundo, err := renderer.Render(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if primeiro.ContentType != "application/pdf" || !bytes.HasPrefix(primeiro.Bytes, []byte("%PDF-1.4")) {
		t.Fatalf("PDF esperado: %q", primeiro.ContentType)
	}
	if primeiro.Hash != nfse.SHA256Hex(primeiro.Bytes) {
		t.Fatal("hash nao corresponde aos bytes do PDF")
	}
	if primeiro.Hash != segundo.Hash || !bytes.Equal(primeiro.Bytes, segundo.Bytes) {
		t.Fatal("renderer fake deveria ser deterministico")
	}
	if primeiro.AccessKey != request.AccessKey || primeiro.IDDps != request.IDDps {
		t.Fatalf("metadados divergentes: %+v", primeiro)
	}
}

func TestFakeDANFSeRejeitaEntradaIncompleta(t *testing.T) {
	renderer := nfse.NewFakeDANFSeRenderer()
	_, err := renderer.Render(context.Background(), nfse.DANFSeRequest{
		IDDps: "DPS-SEM-CHAVE", Competence: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, nfse.ErrInvalidDANFSeRequest) {
		t.Fatalf("entrada incompleta deveria falhar fechado: %v", err)
	}
}

func TestFakeDANFSeContextoCancelado(t *testing.T) {
	renderer := nfse.NewFakeDANFSeRenderer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := renderer.Render(ctx, nfse.DANFSeRequest{
		AccessKey: "NFS-CTX", IDDps: "DPS-CTX",
		Competence: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("contexto cancelado: %v", err)
	}
}
