package nfse_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setupNfseStatusTenant cria tenant/empresa/estabelecimento e registra o
// cleanup administrativo. Usa amparius_app como DSN de teste (sem SUPERUSER e
// sem BYPASSRLS) e a conexao admin apenas para limpeza.
func setupNfseStatusTenant(t *testing.T) (*db.DB, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	tenant, company, establishment := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		tx, cleanupErr := admin.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, q := range []string{
				"ALTER TABLE fiscal.emission_attempt DISABLE TRIGGER emission_attempt_append_only",
				"ALTER TABLE fiscal.fiscal_document DISABLE TRIGGER fiscal_document_history_preserve",
			} {
				if _, cleanupErr = tx.Exec(cleanupCtx, q); cleanupErr != nil {
					break
				}
			}
			for _, q := range []string{
				"DELETE FROM fiscal.emission_attempt WHERE tenant_id=$1",
				"DELETE FROM fiscal.fiscal_document WHERE tenant_id=$1",
				"DELETE FROM identity.establishment WHERE tenant_id=$1",
				"DELETE FROM identity.company WHERE tenant_id=$1",
				"DELETE FROM identity.tenant WHERE id=$1",
			} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, q, tenant)
			}
			for _, q := range []string{
				"ALTER TABLE fiscal.emission_attempt ENABLE TRIGGER emission_attempt_append_only",
				"ALTER TABLE fiscal.fiscal_document ENABLE TRIGGER fiscal_document_history_preserve",
			} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, q)
			}
			if cleanupErr == nil {
				cleanupErr = tx.Commit(cleanupCtx)
			}
		}
		if cleanupErr != nil {
			t.Errorf("cleanup nfse status: %v", cleanupErr)
		}
		admin.Close()
		database.Close()
	})
	ctxTenant := db.WithTenant(ctx, tenant)
	if err := database.WithTenantTx(ctxTenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, tenant, tenant.String(), "nfse-st-"+tenant.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity.company (id,tenant_id,document,name,kind,status) VALUES ($1,$2,$3,'Empresa','funeral','active')`, company, tenant, "c-"+company.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.establishment (id,tenant_id,company_id,document,name,status) VALUES ($1,$2,$3,$4,'Est','active')`, establishment, tenant, company, "e-"+establishment.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return database, tenant, company, establishment
}

func entradaNfse(documentID, tenant, company, establishment uuid.UUID, competence time.Time, idDps, key string, number int64) nfse.ServiceInput {
	return nfse.ServiceInput{
		DocumentID: documentID, Competence: competence,
		TenantID: tenant, CompanyID: company, EstablishmentID: establishment,
		IdempotencyKey: key,
		DPS: nfse.DPSInput{
			IDDps: idDps, Competence: competence,
			EmitterDocument: "00000000000191", MunicipalityCode: "2304400",
			Series: "1", Number: number, Environment: nfse.EnvironmentProducaoRestrita,
			Items: []nfse.DPSItem{{ServiceCode: "25.01", Description: "Servico", Quantity: "1", UnitPriceCents: 15000, TotalCents: 15000}},
		},
	}
}

func TestServicoTimeoutReconciliaSemDuplicarEmissao(t *testing.T) {
	database, tenant, company, establishment := setupNfseStatusTenant(t)
	ctx := context.Background()
	ctxTenant := db.WithTenant(ctx, tenant)
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 15000, ContingencyMode: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	fake := nfse.NewFakeAdapter(nfse.FakeConfig{TimeoutOn: []string{"DPS-SVC-RECON"}})
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository, Adapter: fake, Validator: nfse.NewLocalValidator(),
		AttemptStore: nfse.NewRepositoryAttemptStore(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	entrada := entradaNfse(document.ID, tenant, company, establishment, competence, "DPS-SVC-RECON", "svc-key-recon", 1)

	primeiro, err := servico.Emit(ctxTenant, entrada)
	if !errors.Is(err, nfse.ErrAdapterUnavailable) {
		t.Fatalf("timeout deveria propagar indisponibilidade: %v", err)
	}
	if primeiro.Status != "pending" {
		t.Fatalf("documento deveria ficar pendente: %+v", primeiro)
	}
	pendente, err := repository.GetDocument(ctxTenant, document.ID, competence)
	if err != nil || pendente.Status == "authorized" {
		t.Fatalf("timeout nunca autoriza: %+v err=%v", pendente, err)
	}

	segundo, err := servico.Emit(ctxTenant, entrada)
	if err != nil {
		t.Fatalf("reconciliacao deveria fechar a emissao: %v", err)
	}
	if segundo.Status != "authorized" || segundo.AccessKey == "" {
		t.Fatalf("reconciliacao deveria autorizar: %+v", segundo)
	}
	if fake.EmitCount("DPS-SVC-RECON") != 1 {
		t.Fatalf("reconciliacao duplicou emissao: %d", fake.EmitCount("DPS-SVC-RECON"))
	}

	terceiro, err := servico.Emit(ctxTenant, entrada)
	if err != nil || terceiro.AccessKey != segundo.AccessKey {
		t.Fatalf("replay deveria devolver a mesma autorizacao: %+v err=%v", terceiro, err)
	}
	if fake.EmitCount("DPS-SVC-RECON") != 1 {
		t.Fatalf("replay duplicou emissao: %d", fake.EmitCount("DPS-SVC-RECON"))
	}

	historico, err := repository.ListEmissionAttempts(ctxTenant, document.ID, competence)
	if err != nil {
		t.Fatal(err)
	}
	if len(historico) != 2 {
		t.Fatalf("esperava pendencia + reconciliacao, veio %d tentativas", len(historico))
	}
	if historico[0].Result != "pending" || historico[1].Result != "authorized" {
		t.Fatalf("historico inesperado: %q -> %q", historico[0].Result, historico[1].Result)
	}
	fechado, err := repository.GetDocument(ctxTenant, document.ID, competence)
	if err != nil || fechado.Status != "authorized" || len(fechado.XMLRaw) == 0 {
		t.Fatalf("documento nao foi fechado: %+v err=%v", fechado, err)
	}

	status, err := servico.Status(ctxTenant, nfse.StatusInput{DocumentID: document.ID, Competence: competence})
	if err != nil {
		t.Fatal(err)
	}
	if status.DocumentStatus != "authorized" || status.AttemptResult != "authorized" ||
		status.Attempts != 2 || status.AccessKey != segundo.AccessKey {
		t.Fatalf("status consultavel divergente: %+v", status)
	}
}

func TestServicoRejeicaoPersisteStatusConsultavel(t *testing.T) {
	database, tenant, company, establishment := setupNfseStatusTenant(t)
	ctx := context.Background()
	ctxTenant := db.WithTenant(ctx, tenant)
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 15000})
	if err != nil {
		t.Fatal(err)
	}
	fake := nfse.NewFakeAdapter(nfse.FakeConfig{RejectOn: []string{"DPS-SVC-REJ"}})
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository, Adapter: fake, Validator: nfse.NewLocalValidator(),
		AttemptStore: nfse.NewRepositoryAttemptStore(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	entrada := entradaNfse(document.ID, tenant, company, establishment, competence, "DPS-SVC-REJ", "svc-key-rej", 2)

	resultado, err := servico.Emit(ctxTenant, entrada)
	var rejeicao *nfse.ErrDocumentRejected
	if !errors.As(err, &rejeicao) {
		t.Fatalf("rejeicao tipada esperada: %v", err)
	}
	if resultado.Status != "rejected" || resultado.RejectionCode != rejeicao.Code || rejeicao.Code == "" {
		t.Fatalf("resultado de rejeicao divergente: %+v erro=%+v", resultado, rejeicao)
	}

	attempt, err := repository.GetEmissionAttempt(ctxTenant, "svc-key-rej")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Result != "rejected" || attempt.HTTPStatus == nil || *attempt.HTTPStatus != 400 {
		t.Fatalf("tentativa rejeitada nao persistida: %+v", attempt)
	}
	if attempt.ResponseHash == nil || *attempt.ResponseHash != nfse.SHA256Hex(attempt.ResponseRaw) {
		t.Fatal("response_hash da rejeicao nao corresponde ao corpo cru")
	}
	fechado, err := repository.GetDocument(ctxTenant, document.ID, competence)
	if err != nil || fechado.Status != "rejected" {
		t.Fatalf("documento deveria ficar rejected: %+v err=%v", fechado, err)
	}

	status, err := servico.Status(ctxTenant, nfse.StatusInput{DocumentID: document.ID, Competence: competence})
	if err != nil {
		t.Fatal(err)
	}
	if status.DocumentStatus != "rejected" || status.AttemptResult != "rejected" ||
		status.Rejection == nil || status.Rejection.Code != rejeicao.Code {
		t.Fatalf("status de rejeicao divergente: %+v", status)
	}

	replay, err := servico.Emit(ctxTenant, entrada)
	var rejeicaoReplay *nfse.ErrDocumentRejected
	if !errors.As(err, &rejeicaoReplay) || replay.RejectionCode != rejeicao.Code {
		t.Fatalf("replay deveria repetir a rejeicao: %+v err=%v", replay, err)
	}
	historico, err := repository.ListEmissionAttempts(ctxTenant, document.ID, competence)
	if err != nil || len(historico) != 1 {
		t.Fatalf("replay de rejeicao nao deveria criar tentativa: %d err=%v", len(historico), err)
	}
}

func TestServicoDANFSeFakeDeterministico(t *testing.T) {
	database, tenant, company, establishment := setupNfseStatusTenant(t)
	ctx := context.Background()
	ctxTenant := db.WithTenant(ctx, tenant)
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 15000})
	if err != nil {
		t.Fatal(err)
	}
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository, Adapter: nfse.NewFakeAdapter(nfse.FakeConfig{}),
		Validator: nfse.NewLocalValidator(), AttemptStore: nfse.NewRepositoryAttemptStore(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	entrada := entradaNfse(document.ID, tenant, company, establishment, competence, "DPS-SVC-DANFSE", "svc-key-danfse", 3)
	autorizado, err := servico.Emit(ctxTenant, entrada)
	if err != nil || autorizado.Status != "authorized" {
		t.Fatalf("emissao deveria autorizar: %+v err=%v", autorizado, err)
	}

	primeiro, err := servico.RenderDANFSe(ctxTenant, nfse.DANFSeInput{DocumentID: document.ID, Competence: competence})
	if err != nil {
		t.Fatal(err)
	}
	segundo, err := servico.RenderDANFSe(ctxTenant, nfse.DANFSeInput{DocumentID: document.ID, Competence: competence})
	if err != nil {
		t.Fatal(err)
	}
	if primeiro.ContentType != "application/pdf" || len(primeiro.Bytes) == 0 {
		t.Fatalf("PDF fake esperado: %+v", primeiro)
	}
	if primeiro.Hash != nfse.SHA256Hex(primeiro.Bytes) || primeiro.Hash != segundo.Hash || string(primeiro.Bytes) != string(segundo.Bytes) {
		t.Fatal("DANFSe fake deveria ser deterministico")
	}
	if primeiro.AccessKey != autorizado.AccessKey || primeiro.IDDps != "DPS-SVC-DANFSE" {
		t.Fatalf("metadados do DANFSe divergentes: %+v", primeiro)
	}

	semEmissao, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := servico.RenderDANFSe(ctxTenant, nfse.DANFSeInput{DocumentID: semEmissao.ID, Competence: competence}); !errors.Is(err, nfse.ErrDANFSeNotAuthorized) {
		t.Fatalf("DANFSe sem autorizacao deveria falhar fechado: %v", err)
	}
}
