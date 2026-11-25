package nfse_test

import (
	"bytes"
	"context"
	"log/slog"
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

func TestServicoEmissaoPersisteEIdempotente(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	tenant, company, establishment := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		defer admin.Close()
		cleanupCtx := context.Background()
		tx, cleanupErr := admin.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, q := range []string{"ALTER TABLE fiscal.emission_attempt DISABLE TRIGGER emission_attempt_append_only", "ALTER TABLE fiscal.fiscal_document DISABLE TRIGGER fiscal_document_history_preserve"} {
				if _, cleanupErr = tx.Exec(cleanupCtx, q); cleanupErr != nil {
					break
				}
			}
			for _, q := range []string{"DELETE FROM fiscal.emission_attempt WHERE tenant_id=$1", "DELETE FROM fiscal.fiscal_document WHERE tenant_id=$1", "DELETE FROM identity.establishment WHERE tenant_id=$1", "DELETE FROM identity.company WHERE tenant_id=$1", "DELETE FROM identity.tenant WHERE id=$1"} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, q, tenant)
			}
			for _, q := range []string{"ALTER TABLE fiscal.emission_attempt ENABLE TRIGGER emission_attempt_append_only", "ALTER TABLE fiscal.fiscal_document ENABLE TRIGGER fiscal_document_history_preserve"} {
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
			t.Errorf("cleanup servico: %v", cleanupErr)
		}
	})
	ctxTenant := db.WithTenant(ctx, tenant)
	if err := database.WithTenantTx(ctxTenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, tenant, tenant.String(), "nfse-svc-"+tenant.String()); err != nil {
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
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 15000})
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository, Adapter: nfse.NewFakeAdapter(nfse.FakeConfig{}),
		Validator: nfse.NewLocalValidator(), Logger: logger,
		AttemptStore: nfse.NewRepositoryAttemptStore(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	entrada := nfse.ServiceInput{
		DocumentID: document.ID, Competence: competence,
		TenantID: tenant, CompanyID: company, EstablishmentID: establishment,
		IdempotencyKey: "svc-key-201",
		DPS: nfse.DPSInput{
			IDDps: "DPS-SVC-201", Competence: competence,
			EmitterDocument: "00000000000191", MunicipalityCode: "2304400",
			Series: "1", Number: 1, Environment: nfse.EnvironmentProducaoRestrita,
			Items: []nfse.DPSItem{{ServiceCode: "25.01", Description: "Serviço", Quantity: "1", UnitPriceCents: 15000, TotalCents: 15000}},
		},
	}
	resultado, err := servico.Emit(ctxTenant, entrada)
	if err != nil {
		t.Fatal(err)
	}
	if resultado.Status != "authorized" || resultado.AccessKey == "" {
		t.Fatalf("201 deveria autorizar: %+v", resultado)
	}
	attempt, err := repository.GetEmissionAttempt(ctxTenant, "svc-key-201")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.HTTPStatus == nil || *attempt.HTTPStatus != 201 || attempt.AccessKey == nil || *attempt.AccessKey != resultado.AccessKey {
		t.Fatalf("metadados nao persistidos: %+v", attempt)
	}
	if attempt.DPSID == nil || *attempt.DPSID != "DPS-SVC-201" || attempt.RequestHash == nil {
		t.Fatalf("idDps/request_hash ausentes: %+v", attempt)
	}
	if attempt.ResponseHash == nil || *attempt.ResponseHash != nfse.SHA256Hex([]byte(*attempt.ResponseBody)) {
		t.Fatal("response_hash nao corresponde ao corpo cru persistido")
	}
	storedDocument, err := repository.GetDocument(ctxTenant, document.ID, competence)
	if err != nil || storedDocument.Status != "authorized" || len(storedDocument.XMLRaw) == 0 || string(storedDocument.XMLRaw) == "" {
		t.Fatalf("documento nao foi fechado com XML bruto: %+v err=%v", storedDocument, err)
	}
	if string(attempt.RequestXML) != string(storedDocument.XMLRaw) || string(attempt.ResponseRaw) != *attempt.ResponseBody {
		t.Fatal("bytes brutos da requisicao/resposta nao foram preservados")
	}
	replay, err := servico.Emit(ctxTenant, entrada)
	if err != nil || replay.AccessKey != resultado.AccessKey {
		t.Fatalf("replay: %+v err=%v", replay, err)
	}
	historico, err := repository.ListEmissionAttempts(ctxTenant, document.ID, competence)
	if err != nil || len(historico) != 1 {
		t.Fatalf("replay nao deveria criar nova tentativa: %d err=%v", len(historico), err)
	}
	for _, proibido := range []string{"Serviço", "00000000000191", "<DPS", entrada.DPS.Items[0].Description, resultado.AccessKey} {
		if bytes.Contains(logBuf.Bytes(), []byte(proibido)) {
			t.Fatalf("log contem dado proibido (%q): %s", proibido, logBuf.String())
		}
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("nfse emissao autorizada")) {
		t.Fatal("log estruturado minimo ausente")
	}
}

func TestServicoTimeoutFicaPendente(t *testing.T) {
	dsn, adminDSN := os.Getenv("AMPA_TEST_POSTGRES_DSN"), os.Getenv("AMPA_TEST_POSTGRES_ADMIN_DSN")
	if dsn == "" || adminDSN == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN e AMPA_TEST_POSTGRES_ADMIN_DSN sao necessarios")
	}
	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	tenant, company, establishment := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		defer admin.Close()
		cleanupCtx := context.Background()
		tx, cleanupErr := admin.Begin(cleanupCtx)
		if cleanupErr == nil {
			defer func() { _ = tx.Rollback(cleanupCtx) }()
			for _, q := range []string{"ALTER TABLE fiscal.emission_attempt DISABLE TRIGGER emission_attempt_append_only", "ALTER TABLE fiscal.fiscal_document DISABLE TRIGGER fiscal_document_history_preserve"} {
				if _, cleanupErr = tx.Exec(cleanupCtx, q); cleanupErr != nil {
					break
				}
			}
			for _, q := range []string{"DELETE FROM fiscal.emission_attempt WHERE tenant_id=$1", "DELETE FROM fiscal.fiscal_document WHERE tenant_id=$1", "DELETE FROM identity.establishment WHERE tenant_id=$1", "DELETE FROM identity.company WHERE tenant_id=$1", "DELETE FROM identity.tenant WHERE id=$1"} {
				if cleanupErr != nil {
					break
				}
				_, cleanupErr = tx.Exec(cleanupCtx, q, tenant)
			}
			for _, q := range []string{"ALTER TABLE fiscal.emission_attempt ENABLE TRIGGER emission_attempt_append_only", "ALTER TABLE fiscal.fiscal_document ENABLE TRIGGER fiscal_document_history_preserve"} {
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
			t.Errorf("cleanup timeout: %v", cleanupErr)
		}
	})
	ctxTenant := db.WithTenant(ctx, tenant)
	if err := database.WithTenantTx(ctxTenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, tenant, tenant.String(), "nfse-to-"+tenant.String()); err != nil {
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
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 15000, ContingencyMode: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository,
		Adapter:    nfse.NewFakeAdapter(nfse.FakeConfig{TimeoutOn: []string{"DPS-SVC-TIMEOUT"}}),
		Validator:  nfse.NewLocalValidator(), AttemptStore: nfse.NewRepositoryAttemptStore(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	entrada := nfse.ServiceInput{
		DocumentID: document.ID, Competence: competence,
		TenantID: tenant, CompanyID: company, EstablishmentID: establishment,
		IdempotencyKey: "svc-key-timeout",
		DPS: nfse.DPSInput{
			IDDps: "DPS-SVC-TIMEOUT", Competence: competence,
			EmitterDocument: "00000000000191", MunicipalityCode: "2304400",
			Series: "1", Number: 2, Environment: nfse.EnvironmentProducaoRestrita,
			Items: []nfse.DPSItem{{ServiceCode: "25.01", Description: "Serviço", Quantity: "1", UnitPriceCents: 15000, TotalCents: 15000}},
		},
	}
	resultado, err := servico.Emit(ctxTenant, entrada)
	if err == nil {
		t.Fatal("timeout deveria propagar indisponibilidade")
	}
	if resultado.Status == "authorized" {
		t.Fatal("contingencia offline nunca pode marcar como autorizado")
	}
	if resultado.Status != "pending" {
		t.Fatalf("documento deveria ficar pendente: %+v", resultado)
	}
	attempt, err := repository.GetEmissionAttempt(ctxTenant, "svc-key-timeout")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Result != "pending" || attempt.HTTPStatus != nil {
		t.Fatalf("tentativa deveria ser pendente sem resposta 201: %+v", attempt)
	}
}
