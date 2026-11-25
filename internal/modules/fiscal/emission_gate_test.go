package fiscal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

type fakeEmissionStore struct {
	data      fiscalEmissionSnapshot
	err       error
	calls     int
	tenantID  uuid.UUID
	companyID uuid.UUID
}

func (f *fakeEmissionStore) snapshot(_ context.Context, tenantID, companyID uuid.UUID, _ *uuid.UUID) (fiscalEmissionSnapshot, error) {
	f.calls++
	f.tenantID, f.companyID = tenantID, companyID
	if f.err != nil {
		return fiscalEmissionSnapshot{}, f.err
	}
	return f.data, nil
}

func emissionContext(tenantID, companyID uuid.UUID) context.Context {
	return db.WithCompany(db.WithTenant(context.Background(), tenantID), companyID)
}

func ptrTime(value time.Time) *time.Time { return &value }

func testConfig(tenantID, companyID uuid.UUID, establishmentID *uuid.UUID, from time.Time, to *time.Time) FiscalConfig {
	return FiscalConfig{
		ID: uuid.New(), TenantID: tenantID, CompanyID: companyID, EstablishmentID: establishmentID,
		Regime: "geral", Environment: "homologacao", Status: "active",
		ValidFrom: from, ValidTo: to,
	}
}

func testCertificate(tenantID, companyID uuid.UUID, from, until time.Time) CertificateReference {
	return CertificateReference{
		ID: uuid.New(), TenantID: tenantID, CompanyID: companyID,
		Kind: "a1", SubjectCN: "Empresa Sintetica", SerialNumber: "1234",
		FingerprintSHA256: strings.Repeat("a", 64),
		StorageKind:       "vault", StorageRef: "vault://fiscal/sintetico",
		Status: "active", ValidFrom: from, ValidUntil: until,
	}
}

func newGate(t *testing.T, store *fakeEmissionStore) *EmissionGate {
	t.Helper()
	gate, err := NewEmissionGate(store)
	if err != nil {
		t.Fatal(err)
	}
	return gate
}

func TestEmissionGateResolveConfiguracaoECertificadoVigentes(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	competence := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	config := testConfig(tenantID, companyID, nil, dateOnly(competence), ptrTime(dateOnly(competence).AddDate(1, 0, 0)))
	certificate := testCertificate(tenantID, companyID, dateOnly(competence), dateOnly(competence).AddDate(0, 6, 0))
	gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{
		configs: []FiscalConfig{config}, certificates: []CertificateReference{certificate}, establishmentInScope: true,
	}})

	setup, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{Competence: competence})
	if err != nil {
		t.Fatal(err)
	}
	if setup.Config.ID != config.ID || setup.Certificate.ID != certificate.ID {
		t.Fatalf("setup retornado incorreto: %+v", setup)
	}
	if setup.Certificate.StorageRef != certificate.StorageRef {
		t.Fatal("referencia opaca do certificado nao preservada")
	}
}

func TestEmissionGatePrefereConfiguracaoDoEstabelecimento(t *testing.T) {
	tenantID, companyID, establishmentID := uuid.New(), uuid.New(), uuid.New()
	competence := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	defaultConfig := testConfig(tenantID, companyID, nil, dateOnly(competence), nil)
	specificConfig := testConfig(tenantID, companyID, &establishmentID, dateOnly(competence), nil)
	specificConfig.Environment = "producao"
	certificate := testCertificate(tenantID, companyID, dateOnly(competence), dateOnly(competence).AddDate(0, 6, 0))
	gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{
		configs:      []FiscalConfig{defaultConfig, specificConfig},
		certificates: []CertificateReference{certificate}, establishmentInScope: true,
	}})

	setup, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{
		EstablishmentID: &establishmentID, Competence: competence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if setup.Config.ID != specificConfig.ID || setup.Config.Environment != "producao" {
		t.Fatalf("configuracao do estabelecimento deveria ter precedencia: %+v", setup.Config)
	}
}

func TestEmissionGateUsaPadraoQuandoEspecificaEstaExpirada(t *testing.T) {
	tenantID, companyID, establishmentID := uuid.New(), uuid.New(), uuid.New()
	competence := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	expired := testConfig(tenantID, companyID, &establishmentID, dateOnly(competence).AddDate(-2, 0, 0), ptrTime(dateOnly(competence).AddDate(-1, 0, 0)))
	defaultConfig := testConfig(tenantID, companyID, nil, dateOnly(competence).AddDate(-1, 0, 0), nil)
	certificate := testCertificate(tenantID, companyID, dateOnly(competence), dateOnly(competence).AddDate(0, 6, 0))
	gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{
		configs:      []FiscalConfig{expired, defaultConfig},
		certificates: []CertificateReference{certificate}, establishmentInScope: true,
	}})

	setup, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{
		EstablishmentID: &establishmentID, Competence: competence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if setup.Config.ID != defaultConfig.ID {
		t.Fatalf("esperada configuracao-padrao vigente, veio %s", setup.Config.ID)
	}
}

func TestEmissionGateClassificaAusenciaExpiracaoEInatividade(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	competence := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	certificate := testCertificate(tenantID, companyID, dateOnly(competence), dateOnly(competence).AddDate(0, 6, 0))

	cases := []struct {
		name   string
		config FiscalConfig
		want   error
		code   string
	}{
		{
			name: "expirada",
			config: func() FiscalConfig {
				return testConfig(tenantID, companyID, nil, dateOnly(competence).AddDate(-2, 0, 0), ptrTime(dateOnly(competence).AddDate(-1, 0, 0)))
			}(),
			want: ErrFiscalConfigExpired,
			code: CodeFiscalConfigInvalid,
		},
		{
			name: "inativa",
			config: func() FiscalConfig {
				config := testConfig(tenantID, companyID, nil, dateOnly(competence).AddDate(-1, 0, 0), nil)
				config.Status = "suspended"
				return config
			}(),
			want: ErrFiscalConfigInactive,
			code: CodeFiscalConfigInvalid,
		},
		{
			name: "malformada",
			config: func() FiscalConfig {
				config := testConfig(tenantID, companyID, nil, dateOnly(competence).AddDate(-1, 0, 0), nil)
				config.Regime = "  "
				return config
			}(),
			want: ErrFiscalConfigMalformed,
			code: CodeFiscalConfigInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{
				configs: []FiscalConfig{tc.config}, certificates: []CertificateReference{certificate},
			}})
			_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{Competence: competence})
			if !errors.Is(err, tc.want) {
				t.Fatalf("esperado %v, veio %v", tc.want, err)
			}
			if code := EmissionErrorCode(err); code != tc.code {
				t.Fatalf("codigo %q, esperado %q", code, tc.code)
			}
		})
	}

	t.Run("ausente", func(t *testing.T) {
		gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{
			certificates: []CertificateReference{certificate},
		}})
		_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{Competence: competence})
		if !errors.Is(err, ErrFiscalConfigMissing) || EmissionErrorCode(err) != CodeFiscalConfigMissing {
			t.Fatalf("ausencia deveria falhar fechado como FISCAL_CONFIG_MISSING: %v", err)
		}
	})

	t.Run("ambigua", func(t *testing.T) {
		gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{
			configs: []FiscalConfig{
				testConfig(tenantID, companyID, nil, dateOnly(competence), nil),
				testConfig(tenantID, companyID, nil, dateOnly(competence), nil),
			},
			certificates: []CertificateReference{certificate},
		}})
		_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{Competence: competence})
		if !errors.Is(err, ErrFiscalConfigAmbiguous) {
			t.Fatalf("ambiguidade deveria falhar fechado: %v", err)
		}
	})
}

func TestEmissionGateClassificaCertificadoAusenteExpiradoEMalformado(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	competence := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	config := testConfig(tenantID, companyID, nil, dateOnly(competence).AddDate(-1, 0, 0), nil)

	cases := []struct {
		name        string
		certificate *CertificateReference
		want        error
	}{
		{
			name: "expirado",
			certificate: func() *CertificateReference {
				certificate := testCertificate(tenantID, companyID, dateOnly(competence).AddDate(-1, 0, 0), dateOnly(competence).AddDate(0, -1, 0))
				return &certificate
			}(),
			want: ErrFiscalCertificateExpired,
		},
		{
			name: "inativo",
			certificate: func() *CertificateReference {
				certificate := testCertificate(tenantID, companyID, dateOnly(competence), dateOnly(competence).AddDate(0, 6, 0))
				certificate.Status = "revoked"
				return &certificate
			}(),
			want: ErrFiscalCertificateInactive,
		},
		{
			name: "malformado",
			certificate: func() *CertificateReference {
				certificate := testCertificate(tenantID, companyID, dateOnly(competence), dateOnly(competence).AddDate(0, 6, 0))
				certificate.FingerprintSHA256 = "nao-e-hex"
				return &certificate
			}(),
			want: ErrFiscalCertificateMalformed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := fiscalEmissionSnapshot{configs: []FiscalConfig{config}}
			if tc.certificate != nil {
				data.certificates = []CertificateReference{*tc.certificate}
			}
			gate := newGate(t, &fakeEmissionStore{data: data})
			_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{Competence: competence})
			if !errors.Is(err, tc.want) {
				t.Fatalf("esperado %v, veio %v", tc.want, err)
			}
			if code := EmissionErrorCode(err); code != CodeFiscalCertificateInvalid {
				t.Fatalf("codigo %q, esperado %q", code, CodeFiscalCertificateInvalid)
			}
			if strings.Contains(err.Error(), "Sintetica") || strings.Contains(err.Error(), "vault://") {
				t.Fatalf("erro nao pode vazar metadado/referencia do certificado: %v", err)
			}
		})
	}

	t.Run("ausente", func(t *testing.T) {
		gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{configs: []FiscalConfig{config}}})
		_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{Competence: competence})
		if !errors.Is(err, ErrFiscalCertificateMissing) || EmissionErrorCode(err) != CodeFiscalCertificateInvalid {
			t.Fatalf("certificado ausente deveria falhar fechado: %v", err)
		}
	})
}

func TestEmissionGateAvaliaCertificadoNoMomentoDeEmissao(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	competence := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	config := testConfig(tenantID, companyID, nil, dateOnly(competence).AddDate(-1, 0, 0), nil)
	certificate := testCertificate(tenantID, companyID, dateOnly(competence), dateOnly(competence).AddDate(0, 6, 0))
	gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{
		configs: []FiscalConfig{config}, certificates: []CertificateReference{certificate},
	}})

	_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{
		Competence: competence, IssuedAt: dateOnly(competence).AddDate(0, 7, 0),
	})
	if !errors.Is(err, ErrFiscalCertificateExpired) {
		t.Fatalf("IssuedAt posterior deveria expirar o certificado: %v", err)
	}
}

func TestEmissionGateFalhaFechadoSemEscopoEstabelecimentoOuCompetencia(t *testing.T) {
	tenantID, companyID, establishmentID := uuid.New(), uuid.New(), uuid.New()
	competence := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	config := testConfig(tenantID, companyID, nil, dateOnly(competence).AddDate(-1, 0, 0), nil)
	certificate := testCertificate(tenantID, companyID, dateOnly(competence), dateOnly(competence).AddDate(0, 6, 0))

	t.Run("estabelecimento fora do escopo", func(t *testing.T) {
		gate := newGate(t, &fakeEmissionStore{data: fiscalEmissionSnapshot{
			configs: []FiscalConfig{config}, certificates: []CertificateReference{certificate},
			establishmentInScope: false,
		}})
		_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{
			EstablishmentID: &establishmentID, Competence: competence,
		})
		if !errors.Is(err, ErrEstablishmentNotInScope) {
			t.Fatalf("estabelecimento de outra empresa deveria bloquear: %v", err)
		}
	})

	t.Run("competencia zero", func(t *testing.T) {
		gate := newGate(t, &fakeEmissionStore{})
		_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{})
		if !errors.Is(err, ErrInvalidCompetence) {
			t.Fatalf("competencia zero deveria bloquear: %v", err)
		}
	})

	t.Run("sem tenant", func(t *testing.T) {
		gate := newGate(t, &fakeEmissionStore{})
		_, err := gate.Resolve(db.WithCompany(context.Background(), companyID), EmissionCriteria{Competence: competence})
		if !errors.Is(err, db.ErrTenantRequired) {
			t.Fatalf("tenant ausente deveria falhar antes da consulta: %v", err)
		}
	})

	t.Run("sem empresa", func(t *testing.T) {
		gate := newGate(t, &fakeEmissionStore{})
		_, err := gate.Resolve(db.WithTenant(context.Background(), tenantID), EmissionCriteria{Competence: competence})
		if !errors.Is(err, db.ErrCompanyRequired) {
			t.Fatalf("empresa ausente deveria falhar antes da consulta: %v", err)
		}
	})

	t.Run("erro de banco nao vira sucesso", func(t *testing.T) {
		gate := newGate(t, &fakeEmissionStore{err: errors.New("falha sintetica")})
		_, err := gate.Resolve(emissionContext(tenantID, companyID), EmissionCriteria{Competence: competence})
		if err == nil || EmissionErrorCode(err) != "" {
			t.Fatalf("falha de banco deveria propagar sem codigo de negocio: %v", err)
		}
	})
}

func TestEmissionErrorCodeSemErro(t *testing.T) {
	if code := EmissionErrorCode(nil); code != "" {
		t.Fatalf("erro nulo deveria ter codigo vazio: %q", code)
	}
}
