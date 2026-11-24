package fiscal_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmissionAttemptMetadata0008(t *testing.T) {
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
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	tenant := uuid.New()
	company, establishment := uuid.New(), uuid.New()
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
			t.Errorf("cleanup metadados 0008: %v", cleanupErr)
		}
	})
	ctxTenant := db.WithTenant(ctx, tenant)
	if err := database.WithTenantTx(ctxTenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, tenant, tenant.String(), "nfse-"+tenant.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity.company (id,tenant_id,document,name,kind,status) VALUES ($1,$2,$3,'Empresa NFS-e','funeral','active')`, company, tenant, "company-"+company.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.establishment (id,tenant_id,company_id,document,name,status) VALUES ($1,$2,$3,$4,'Estabelecimento NFS-e','active')`, establishment, tenant, company, "est-"+establishment.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 25000})
	if err != nil {
		t.Fatal(err)
	}
	xmlEnviado := []byte("<DPS>conteudo-canônico</DPS>")
	requestHash := sha256Hex(xmlEnviado)
	corpo := []byte("corpo-cru-201")
	responseHash := sha256Hex(corpo)
	status := 201
	ambiente := "producao_restrita"
	chave := "CHAVE-TESTE-2304400"
	dpsID := "DPS-TESTE-0001"
	protocolo := "PROTO-0001"
	contentType := "application/xml"
	attempt, err := repository.RegisterEmissionAttempt(ctxTenant, fiscal.EmissionAttempt{
		DocumentID: document.ID, Competence: competence, Attempt: 1,
		IdempotencyKey: "nfse-metadata-key", Mode: "normal", Result: "authorized",
		Protocol: &protocolo, ResponseBody: ptrString(string(corpo)),
		ResponseHash: &responseHash, ResponseContentType: &contentType,
		HTTPStatus: &status, Environment: &ambiente, AccessKey: &chave,
		DPSID: &dpsID, RequestHash: &requestHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.HTTPStatus == nil || *attempt.HTTPStatus != status {
		t.Fatalf("http_status nao persistido: %+v", attempt.HTTPStatus)
	}
	if attempt.Environment == nil || *attempt.Environment != ambiente {
		t.Fatalf("ambiente nao persistido: %+v", attempt.Environment)
	}
	if attempt.AccessKey == nil || *attempt.AccessKey != chave {
		t.Fatalf("chave_acesso nao persistida: %+v", attempt.AccessKey)
	}
	if attempt.DPSID == nil || *attempt.DPSID != dpsID {
		t.Fatalf("dps_id nao persistido: %+v", attempt.DPSID)
	}
	if attempt.RequestHash == nil || *attempt.RequestHash != requestHash {
		t.Fatalf("request_hash nao persistido: %+v", attempt.RequestHash)
	}
	replay, err := repository.RegisterEmissionAttempt(ctxTenant, fiscal.EmissionAttempt{
		DocumentID: document.ID, Competence: competence, Attempt: 99,
		IdempotencyKey: "nfse-metadata-key", Mode: "normal", Result: "rejected",
	})
	if err != nil || replay.ID != attempt.ID || replay.HTTPStatus == nil || *replay.HTTPStatus != status {
		t.Fatalf("replay nao preservou metadados: %+v err=%v", replay, err)
	}
	listed, err := repository.ListEmissionAttempts(ctxTenant, document.ID, competence)
	if err != nil || len(listed) != 1 || listed[0].HTTPStatus == nil || *listed[0].HTTPStatus != status {
		t.Fatalf("historico nao traz metadados: %+v err=%v", listed, err)
	}
	if _, err := repository.GetEmissionAttempt(ctxTenant, "nfse-metadata-key"); err != nil && !errors.Is(err, fiscal.ErrAttemptNotFound) {
		t.Fatalf("consulta por chave: %v", err)
	}
	if sha256Hex([]byte("corpo-cru-202")) == responseHash {
		t.Fatal("sha256 deveria mudar com 1 byte")
	}
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func ptrString(value string) *string { return &value }
