package nfse_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
)

func TestFakeAdapter201EReplaySemDuplicar(t *testing.T) {
	fake := nfse.NewFakeAdapter(nfse.FakeConfig{})
	req := emitRequest("DPS-201", "key-201")
	primeiro, err := fake.EmitDPS(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if primeiro.HTTPStatus != 201 || primeiro.AccessKey == "" || len(primeiro.ResponseBody) == 0 {
		t.Fatalf("201 esperado: %+v", primeiro)
	}
	if primeiro.ResponseHash != nfse.SHA256Hex(primeiro.ResponseBody) {
		t.Fatal("response_hash divergente do corpo cru")
	}
	replay, err := fake.EmitDPS(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if replay.AccessKey != primeiro.AccessKey || string(replay.ResponseBody) != string(primeiro.ResponseBody) {
		t.Fatalf("replay duplicou NFS-e: %+v vs %+v", replay, primeiro)
	}
	if fake.EmitCount("DPS-201") != 1 {
		t.Fatal("replay nao deveria contar como nova emissao")
	}
	status, err := fake.GetDPSStatus(context.Background(), "DPS-201")
	if err != nil || !status.Exists || status.AccessKey != primeiro.AccessKey {
		t.Fatalf("GetDPSStatus: %+v err=%v", status, err)
	}
	porChave, err := fake.GetNFSByKey(context.Background(), primeiro.AccessKey)
	if err != nil || porChave.HTTPStatus != 201 {
		t.Fatalf("GetNFSByKey: %+v err=%v", porChave, err)
	}
}

func TestFakeAdapter400ComListaDeErros(t *testing.T) {
	fake := nfse.NewFakeAdapter(nfse.FakeConfig{RejectOn: []string{"DPS-400"}})
	_, err := fake.EmitDPS(context.Background(), emitRequest("DPS-400", "key-400"))
	if err == nil {
		t.Fatal("400 deveria rejeitar")
	}
	var rejeicao *nfse.ErrDocumentRejected
	if !errors.As(err, &rejeicao) {
		t.Fatalf("erro tipado esperado: %v", err)
	}
	if rejeicao.Code == "" || rejeicao.Description == "" {
		t.Fatal("rejeicao deve trazer codigo/descricao oficiais")
	}
	if !errors.Is(err, nfse.ErrDocumentRejectedType) {
		t.Fatal("rejeicao deve ser reconhecivel por tipo")
	}
}

func TestFakeAdapterTimeoutReconciliadoPorGetDPSStatus(t *testing.T) {
	fake := nfse.NewFakeAdapter(nfse.FakeConfig{TimeoutOn: []string{"DPS-TIMEOUT"}})
	req := emitRequest("DPS-TIMEOUT", "key-timeout")
	_, err := fake.EmitDPS(context.Background(), req)
	if !errors.Is(err, nfse.ErrAdapterUnavailable) {
		t.Fatalf("timeout deveria ser indisponibilidade: %v", err)
	}
	status, err := fake.GetDPSStatus(context.Background(), "DPS-TIMEOUT")
	if err != nil || !status.Exists {
		t.Fatalf("apos timeout a DPS ja gerou NFS-e: %+v err=%v", status, err)
	}
	replay, err := fake.EmitDPS(context.Background(), req)
	if err != nil || replay.HTTPStatus != 201 || replay.AccessKey != status.AccessKey {
		t.Fatalf("reenvio deveria reconciliar sem duplicar: %+v err=%v", replay, err)
	}
}

func TestFakeAdapterValidacaoEParametrosLocais(t *testing.T) {
	fake := nfse.NewFakeAdapter(nfse.FakeConfig{})
	dps, err := nfse.BuildDPS(context.Background(), entradaBasica())
	if err != nil {
		t.Fatal(err)
	}
	resultado, err := fake.ValidateDPS(context.Background(), dps)
	if err != nil || !resultado.Valid {
		t.Fatalf("ValidateDPS local: %+v err=%v", resultado, err)
	}
	parametro, err := fake.MunicipalParameters(context.Background(), "2304400", "25.01", time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC))
	if err != nil || parametro.MunicipalityCode != "2304400" || len(parametro.Raw) == 0 {
		t.Fatalf("MunicipalParameters: %+v err=%v", parametro, err)
	}
}

func TestFakeAdapterContextoCancelado(t *testing.T) {
	fake := nfse.NewFakeAdapter(nfse.FakeConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fake.EmitDPS(ctx, emitRequest("DPS-CTX", "key-ctx")); !errors.Is(err, context.Canceled) {
		t.Fatalf("contexto cancelado: %v", err)
	}
}

func emitRequest(idDps, key string) nfse.EmitRequest {
	return nfse.EmitRequest{
		TenantID: "tenant-teste", CompanyID: "company-teste", EstablishmentID: "est-teste",
		Competence: time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC),
		IDDps:      idDps, IdempotencyKey: key, Environment: nfse.EnvironmentProducaoRestrita,
		SignedXML: []byte("<DPS/>"),
	}
}
