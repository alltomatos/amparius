package nfse_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Negativos de XSD sobre a DPS oficial VALIDA: mutacoes no XML devem ser
// rejeitadas pelo schema local, provando que o teste positivo nao e frouxo.
func TestDPSOficialNegativosDeXSD(t *testing.T) {
	validador := novoValidadorOficial(t)
	input := carregarFixtureOficial(t)
	valida, err := nfse.BuildDPSOficial(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	xml := string(valida.XML)
	casos := []struct {
		nome      string
		transform func(string) string
	}{
		{"versao fora do pattern", func(s string) string { return strings.Replace(s, `versao="1.01"`, `versao="9.99"`, 1) }},
		{"Id fora do pattern", func(s string) string {
			return strings.Replace(s, `Id="DPS000000000000000000000000000000000000000000"`, `Id="DPS-CURTO"`, 1)
		}},
		{"sem infDPS", func(s string) string {
			return strings.Replace(s, `<infDPS Id="DPS000000000000000000000000000000000000000000">`, ``, 1)
		}},
		{"tpAmb fora da enumeracao", func(s string) string { return strings.Replace(s, `<tpAmb>2</tpAmb>`, `<tpAmb>9</tpAmb>`, 1) }},
		{"CNPJ fora do pattern", func(s string) string { return strings.Replace(s, `<CNPJ>00000000000191</CNPJ>`, `<CNPJ>ABC</CNPJ>`, 1) }},
		{"elemento nao previsto", func(s string) string { return strings.Replace(s, `</infDPS>`, `<extra/></infDPS>`, 1) }},
		{"vServ fora do pattern", func(s string) string { return strings.Replace(s, `<vServ>1.00</vServ>`, `<vServ>1.000</vServ>`, 1) }},
		{"namespace divergente", func(s string) string { return strings.Replace(s, nfse.NamespaceDPS, "http://invalido.test", 1) }},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			mutado := c.transform(xml)
			if mutado == xml {
				t.Fatal("transformacao nao alterou o XML")
			}
			resultado, err := validador.ValidateDPS(context.Background(), nfse.DPS{XML: []byte(mutado), IDDps: input.ID})
			if err != nil {
				t.Fatal(err)
			}
			if resultado.Valid {
				t.Fatalf("XSD aceitou mutacao invalida (%s)", c.nome)
			}
		})
	}
}

func setupTenantOficial(t *testing.T, ctx context.Context, database *db.DB, admin *pgxpool.Pool) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	tenant, company, establishment := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		defer admin.Close()
		cleanupCtx := context.Background()
		tx, err := admin.Begin(cleanupCtx)
		if err != nil {
			t.Errorf("cleanup oficial begin: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(cleanupCtx) }()
		for _, q := range []string{"ALTER TABLE fiscal.emission_attempt DISABLE TRIGGER emission_attempt_append_only", "ALTER TABLE fiscal.fiscal_document DISABLE TRIGGER fiscal_document_history_preserve"} {
			if _, err = tx.Exec(cleanupCtx, q); err != nil {
				break
			}
		}
		for _, q := range []string{"DELETE FROM fiscal.emission_attempt WHERE tenant_id=$1", "DELETE FROM fiscal.fiscal_document WHERE tenant_id=$1", "DELETE FROM identity.establishment WHERE tenant_id=$1", "DELETE FROM identity.company WHERE tenant_id=$1", "DELETE FROM identity.tenant WHERE id=$1"} {
			if err != nil {
				break
			}
			_, err = tx.Exec(cleanupCtx, q, tenant)
		}
		for _, q := range []string{"ALTER TABLE fiscal.emission_attempt ENABLE TRIGGER emission_attempt_append_only", "ALTER TABLE fiscal.fiscal_document ENABLE TRIGGER fiscal_document_history_preserve"} {
			if err != nil {
				break
			}
			_, err = tx.Exec(cleanupCtx, q)
		}
		if err == nil {
			err = tx.Commit(cleanupCtx)
		}
		if err != nil {
			t.Errorf("cleanup oficial: %v", err)
		}
	})
	ctxTenant := db.WithTenant(ctx, tenant)
	if err := database.WithTenantTx(ctxTenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id,name,document,regime,status) VALUES ($1,$2,$3,'economic_group','active')`, tenant, tenant.String(), "nfse-of-"+tenant.String()); err != nil {
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
	return tenant, company, establishment
}

// Integra o builder oficial ao Service: XSDValidator + FakeAdapter +
// idempotencia + persistencia + timeout/pendencia continuam funcionando.
func TestServicoComDPSOficialMantemFluxo(t *testing.T) {
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
	tenant, company, establishment := setupTenantOficial(t, ctx, database, admin)
	ctxTenant := db.WithTenant(ctx, tenant)
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 100})
	if err != nil {
		t.Fatal(err)
	}
	official := carregarFixtureOficial(t)
	var logBuf bytes.Buffer
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository, Adapter: nfse.NewFakeAdapter(nfse.FakeConfig{}),
		Validator: novoValidadorOficial(t), Logger: slog.New(slog.NewJSONHandler(&logBuf, nil)),
		AttemptStore: nfse.NewRepositoryAttemptStore(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	entrada := nfse.ServiceInput{
		DocumentID: document.ID, Competence: competence,
		TenantID: tenant, CompanyID: company, EstablishmentID: establishment,
		IdempotencyKey: "svc-oficial-201", Official: &official,
	}
	resultado, err := servico.Emit(ctxTenant, entrada)
	if err != nil {
		t.Fatal(err)
	}
	if resultado.Status != "authorized" || resultado.AccessKey == "" {
		t.Fatalf("201 oficial: %+v", resultado)
	}
	attempt, err := repository.GetEmissionAttempt(ctxTenant, "svc-oficial-201")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.HTTPStatus == nil || *attempt.HTTPStatus != 201 || attempt.DPSID == nil || *attempt.DPSID != official.ID {
		t.Fatalf("persistencia oficial: %+v", attempt)
	}
	if attempt.RequestHash == nil || len(*attempt.RequestHash) != 64 {
		t.Fatal("request_hash da DPS oficial ausente ou invalido")
	}
	replay, err := servico.Emit(ctxTenant, entrada)
	if err != nil || replay.AccessKey != resultado.AccessKey {
		t.Fatalf("replay oficial: %+v err=%v", replay, err)
	}
	historico, err := repository.ListEmissionAttempts(ctxTenant, document.ID, competence)
	if err != nil || len(historico) != 1 {
		t.Fatalf("idempotencia oficial: %d err=%v", len(historico), err)
	}
	for _, proibido := range []string{"Servico de teste sintetico", "00000000000191", "<infDPS", resultado.AccessKey} {
		if bytes.Contains(logBuf.Bytes(), []byte(proibido)) {
			t.Fatalf("log contem dado proibido (%q)", proibido)
		}
	}
}

func TestServicoComDPSOficialTimeoutPendente(t *testing.T) {
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
	tenant, company, establishment := setupTenantOficial(t, ctx, database, admin)
	ctxTenant := db.WithTenant(ctx, tenant)
	repository, err := fiscal.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	competence := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 100, ContingencyMode: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	official := carregarFixtureOficial(t)
	official.ID = "DPS000000000000000000000000000000000000000001"
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository,
		Adapter:    nfse.NewFakeAdapter(nfse.FakeConfig{TimeoutOn: []string{official.ID}}),
		Validator:  novoValidadorOficial(t), AttemptStore: nfse.NewRepositoryAttemptStore(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	entrada := nfse.ServiceInput{
		DocumentID: document.ID, Competence: competence,
		TenantID: tenant, CompanyID: company, EstablishmentID: establishment,
		IdempotencyKey: "svc-oficial-timeout", Official: &official,
	}
	resultado, err := servico.Emit(ctxTenant, entrada)
	if !errors.Is(err, nfse.ErrAdapterUnavailable) {
		t.Fatalf("timeout oficial: %v", err)
	}
	if resultado.Status != "pending" {
		t.Fatalf("contingencia nunca autoriza: %+v", resultado)
	}
	attempt, err := repository.GetEmissionAttempt(ctxTenant, "svc-oficial-timeout")
	if err != nil || attempt.Result != "pending" {
		t.Fatalf("tentativa pendente: %+v err=%v", attempt, err)
	}
}
