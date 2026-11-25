package nfse_test

import (
	"context"
	"errors"
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

// Negativos de XSD sobre a DPS oficial: mutacoes no XML continuam rejeitadas
// pelo schema local vigente (NT 009).
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
			return strings.Replace(s, `Id="DPS230440020000000000019100001000000000000001"`, `Id="DPS-CURTO"`, 1)
		}},
		{"sem infDPS", func(s string) string {
			return strings.Replace(s, `<infDPS Id="DPS230440020000000000019100001000000000000001">`, ``, 1)
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

type adapterSpy struct {
	delegate *nfse.FakeAdapter
	calls    int
}

func (s *adapterSpy) EmitDPS(ctx context.Context, request nfse.EmitRequest) (nfse.EmitResult, error) {
	s.calls++
	return s.delegate.EmitDPS(ctx, request)
}

func (s *adapterSpy) GetNFSByKey(ctx context.Context, accessKey string) (nfse.EmitResult, error) {
	s.calls++
	return s.delegate.GetNFSByKey(ctx, accessKey)
}

func (s *adapterSpy) GetDPSStatus(ctx context.Context, idDps string) (nfse.DPSStatus, error) {
	s.calls++
	return s.delegate.GetDPSStatus(ctx, idDps)
}

func (s *adapterSpy) ValidateDPS(ctx context.Context, dps nfse.DPS) (nfse.ValidationResult, error) {
	s.calls++
	return s.delegate.ValidateDPS(ctx, dps)
}

func (s *adapterSpy) MunicipalParameters(ctx context.Context, municipalityCode, serviceCode string, competence time.Time) (nfse.MunicipalParameter, error) {
	s.calls++
	return s.delegate.MunicipalParameters(ctx, municipalityCode, serviceCode, competence)
}

// O Service deve falhar fechado antes do adapter quando a serie esta fora do
// TSSerieDPS vigente. Nao se registra tentativa nem se transmite.
func TestServicoComSerieForaDoFacetBloqueiaAntesDoAdapter(t *testing.T) {
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
	official.Serie = "99999"
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository, Adapter: nfse.NewFakeAdapter(nfse.FakeConfig{}),
		Validator:    novoValidadorOficial(t),
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
	if !errors.Is(err, nfse.ErrInvalidDPS) {
		t.Fatalf("serie fora do facet deveria bloquear a emissao: %v", err)
	}
	if resultado != (nfse.ServiceResult{}) {
		t.Fatalf("emissao bloqueada nao deveria produzir resultado: %+v", resultado)
	}
	if _, err := repository.GetEmissionAttempt(ctxTenant, "svc-oficial-201"); !errors.Is(err, fiscal.ErrAttemptNotFound) {
		t.Fatalf("emissao bloqueada nao deveria persistir tentativa: %v", err)
	}
}

func TestServicoComEntradaInvalidaBloqueiaAntesDaContingencia(t *testing.T) {
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
	official.FinNFSe = "9"
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
	if !errors.Is(err, nfse.ErrInvalidDPS) {
		t.Fatalf("entrada invalida deveria bloquear antes do timeout: %v", err)
	}
	if resultado != (nfse.ServiceResult{}) {
		t.Fatalf("emissao bloqueada nao deveria entrar em contingencia: %+v", resultado)
	}
	if _, err := repository.GetEmissionAttempt(ctxTenant, "svc-oficial-timeout"); !errors.Is(err, fiscal.ErrAttemptNotFound) {
		t.Fatalf("bloqueio XSD nao deveria persistir pendencia: %v", err)
	}
}

// A entrada e SINTETICA e NAO OFICIAL. O teste prova que a forma invalida de
// cTribMun falha antes de qualquer chamada ao adapter, persistencia ou fila de
// contingencia.
func TestServicoComCTribMunInvalidoBloqueiaAntesDoAdapter(t *testing.T) {
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
	competence := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	document, err := repository.CreateDocument(ctxTenant, fiscal.Document{CompanyID: company, EstablishmentID: establishment, Competence: competence, Kind: "nfse", TotalCents: 100, ContingencyMode: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	official := carregarFixtureOficial(t)
	valorInvalido := "12"
	official.Servico.CTribMun = &valorInvalido
	spy := &adapterSpy{delegate: nfse.NewFakeAdapter(nfse.FakeConfig{})}
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository: repository, Adapter: spy, Validator: novoValidadorOficial(t),
		AttemptStore: nfse.NewRepositoryAttemptStore(repository),
	})
	if err != nil {
		t.Fatal(err)
	}
	entrada := nfse.ServiceInput{
		DocumentID: document.ID, Competence: competence,
		TenantID: tenant, CompanyID: company, EstablishmentID: establishment,
		IdempotencyKey: "svc-oficial-ctribmun-invalido", Official: &official,
	}
	resultado, err := servico.Emit(ctxTenant, entrada)
	if !errors.Is(err, nfse.ErrInvalidDPS) {
		t.Fatalf("cTribMun invalido deveria falhar fechado: %v", err)
	}
	if resultado != (nfse.ServiceResult{}) {
		t.Fatalf("emissao bloqueada nao deveria produzir resultado: %+v", resultado)
	}
	if spy.calls != 0 {
		t.Fatalf("entrada invalida chegou ao adapter: %d chamadas", spy.calls)
	}
	if _, err := repository.GetEmissionAttempt(ctxTenant, "svc-oficial-ctribmun-invalido"); !errors.Is(err, fiscal.ErrAttemptNotFound) {
		t.Fatalf("entrada invalida nao deveria persistir tentativa: %v", err)
	}
}
