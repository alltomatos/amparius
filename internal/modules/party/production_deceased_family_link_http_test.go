package party_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/party"
	"github.com/alltomatos/amparius/internal/platform/audit"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
)

// fakeProductionFamilyLinkService captures the input to the authenticated handler.
type fakeProductionFamilyLinkService struct {
	mu          sync.Mutex
	items       map[uuid.UUID]map[uuid.UUID]party.FamilyLink
	created     []party.FamilyLinkCreate
	evidenceRef []uuid.UUID
	getErr      error
	createErr   error
}

func newFakeProductionFamilyLinkService() *fakeProductionFamilyLinkService {
	return &fakeProductionFamilyLinkService{items: make(map[uuid.UUID]map[uuid.UUID]party.FamilyLink)}
}

func (f *fakeProductionFamilyLinkService) CreateFamilyLinkWithConsent(ctx context.Context, input party.FamilyLinkCreate, evidenceRef uuid.UUID, _ audit.PolicyProvider) (party.FamilyLink, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.FamilyLink{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return party.FamilyLink{}, f.createErr
	}
	value := party.FamilyLink{
		ID:         uuid.New(),
		TenantID:   tenantID,
		DeceasedID: input.DeceasedID,
		PersonID:   input.PersonID,
		Relation:   input.Relation,
		Deceased:   party.Party{ID: input.DeceasedID, Kind: party.KindPerson, Name: "Maria Civil"},
		Person:     party.Party{ID: input.PersonID, Kind: party.KindPerson, Name: "João Civil", SocialName: stringPointer("João Social")},
	}
	if f.items[tenantID] == nil {
		f.items[tenantID] = make(map[uuid.UUID]party.FamilyLink)
	}
	f.items[tenantID][value.ID] = value
	f.created = append(f.created, input)
	f.evidenceRef = append(f.evidenceRef, evidenceRef)
	return value, nil
}

func (f *fakeProductionFamilyLinkService) GetFamilyLinkByID(ctx context.Context, id uuid.UUID) (party.FamilyLink, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.FamilyLink{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return party.FamilyLink{}, f.getErr
	}
	value, ok := f.items[tenantID][id]
	if !ok {
		return party.FamilyLink{}, party.ErrFamilyLinkNotFound
	}
	return value, nil
}

// syntheticPolicyProvider é um audit.PolicyProvider só de teste: devolve uma
// política fixa que NENHUM DPO real aprovou. Usado apenas para exercitar o
// caminho feliz do contrato Go/HTTP; nunca é o provedor do bootstrap de
// produção (ADR 0042 §4).
type syntheticPolicyProvider struct {
	policy audit.Policy
	err    error
}

func (p *syntheticPolicyProvider) Resolve(_ context.Context, _ string) (audit.Policy, error) {
	if p.err != nil {
		return audit.Policy{}, p.err
	}
	return p.policy, nil
}

func (f *fakeDeceasedService) CreateDeceasedWithAudit(ctx context.Context, input party.DeceasedCreate, _ audit.PolicyProvider) (party.Deceased, error) {
	return f.CreateDeceased(ctx, input)
}

const testConsentEvidence = "d9ed095f-f5a6-4d21-b5b3-9694f54753e0"

func newProductionDeceasedFamilyLinkTestHandler(t *testing.T, deceasedService party.ProductionDeceasedService, familyLinkService party.ProductionFamilyLinkService, policy audit.PolicyProvider) http.Handler {
	t.Helper()
	principal := platformauth.Principal{
		Subject:          "unit-test-subject",
		TenantID:         uuid.New(),
		CompanyID:        uuid.New(),
		Roles:            []string{"owner"},
		MFAAuthenticated: true,
	}
	deceasedHandler := party.NewProductionDeceasedHTTPHandler(deceasedService, policy)
	familyLinkHandler := party.NewProductionFamilyLinkHTTPHandler(familyLinkService, policy)
	return httpserver.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), httpserver.Options{
		Authenticator: alwaysAllowGate{principal: principal},
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "POST /v1/deceased", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(deceasedHandler.Create)},
			{Pattern: "GET /v1/deceased/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(deceasedHandler.Get)},
			{Pattern: "POST /v1/deceased/{deceasedId}/family-links", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(familyLinkHandler.Create)},
			{Pattern: "GET /v1/family-links/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(familyLinkHandler.Get)},
		},
	}).Handler()
}

// TestProductionDeceasedFamilyLinkFailsClosedWithoutAuthenticator prova, sem
// banco, que as quatro rotas novas seguem o mesmo fail-closed do CP-06 (ADR
// 0038): sem auth.Gate configurado, nenhuma requisição alcança o handler.
func TestProductionDeceasedFamilyLinkFailsClosedWithoutAuthenticator(t *testing.T) {
	deceasedHandler := party.NewProductionDeceasedHTTPHandler(nil, nil)
	familyLinkHandler := party.NewProductionFamilyLinkHTTPHandler(nil, nil)
	server := httpserver.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), httpserver.Options{
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "POST /v1/deceased", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(deceasedHandler.Create)},
			{Pattern: "GET /v1/deceased/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(deceasedHandler.Get)},
			{Pattern: "POST /v1/deceased/{deceasedId}/family-links", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(familyLinkHandler.Create)},
			{Pattern: "GET /v1/family-links/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(familyLinkHandler.Get)},
		},
	}).Handler()

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v1/deceased", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodGet, "/v1/deceased/"+uuid.NewString(), nil),
		httptest.NewRequest(http.MethodPost, "/v1/deceased/"+uuid.NewString()+"/family-links", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodGet, "/v1/family-links/"+uuid.NewString(), nil),
	} {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s status = %d, esperado 503: %s", req.Method, req.URL.Path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"AUTHENTICATION_UNAVAILABLE"`) {
			t.Fatalf("corpo sem AUTHENTICATION_UNAVAILABLE: %s", rec.Body.String())
		}
	}
}

func TestProductionDeceasedCreateValidation(t *testing.T) {
	deceasedService := newFakeDeceasedService()
	familyLinkService := newFakeProductionFamilyLinkService()
	handler := newProductionDeceasedFamilyLinkTestHandler(t, deceasedService, familyLinkService, &syntheticPolicyProvider{})

	cases := []struct {
		name string
		body string
	}{
		{"personId ausente", `{"deathAt":"2026-11-24T12:00:00Z"}`},
		{"deathAt ausente", `{"personId":"` + uuid.NewString() + `"}`},
		{"deathAt inválido", `{"personId":"` + uuid.NewString() + `","deathAt":"24/11/2026"}`},
		{"campo desconhecido", `{"personId":"` + uuid.NewString() + `","deathAt":"2026-11-24T12:00:00Z","personName":"não permitido"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/deceased", strings.NewReader(testCase.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"VALIDATION_ERROR"`) {
				t.Fatalf("code ausente: %s", rec.Body.String())
			}
		})
	}
}

func TestProductionDeceasedCreatePersistsThroughFakeService(t *testing.T) {
	deceasedService := newFakeDeceasedService()
	familyLinkService := newFakeProductionFamilyLinkService()
	handler := newProductionDeceasedFamilyLinkTestHandler(t, deceasedService, familyLinkService, &syntheticPolicyProvider{})

	personID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/v1/deceased", strings.NewReader(
		`{"personId":"`+personID+`","deathAt":"2026-11-24T12:00:00Z","deathCertificate":"DC-9","burialSite":"Jazigo 3"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		ID               string  `json:"id"`
		PersonID         string  `json:"personId"`
		DeathCertificate *string `json:"deathCertificate"`
		CivilName        string  `json:"civilName"`
		DisplayName      string  `json:"displayName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if response.PersonID != personID || response.CivilName != "Ana Civil" || response.DisplayName != "Ana Social" {
		t.Fatalf("resposta inesperada: %#v", response)
	}
	if len(deceasedService.created) != 1 {
		t.Fatalf("criações = %d, esperado 1", len(deceasedService.created))
	}

	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/v1/deceased/"+response.ID, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", getRec.Code, getRec.Body.String())
	}
}

func TestProductionDeceasedGetMapsServiceErrors(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"não encontrado", party.ErrDeceasedNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"erro interno", errors.New("detalhe interno não deve sair"), http.StatusServiceUnavailable, "PARTY_UNAVAILABLE"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deceasedService := newFakeDeceasedService()
			deceasedService.getErr = testCase.err
			familyLinkService := newFakeProductionFamilyLinkService()
			handler := newProductionDeceasedFamilyLinkTestHandler(t, deceasedService, familyLinkService, nil)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/deceased/"+uuid.NewString(), nil))
			if rec.Code != testCase.want {
				t.Fatalf("status = %d, esperado %d: %s", rec.Code, testCase.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"`+testCase.code+`"`) {
				t.Fatalf("code ausente (%s): %s", testCase.code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "detalhe interno") {
				t.Fatalf("resposta vazou erro interno: %s", rec.Body.String())
			}
		})
	}
}

func TestProductionMutationsFailClosedWithoutPolicy(t *testing.T) {
	deceasedService := newFakeDeceasedService()
	familyLinkService := newFakeProductionFamilyLinkService()
	handler := newProductionDeceasedFamilyLinkTestHandler(t, deceasedService, familyLinkService, nil)

	for _, request := range mutationRequests(uuid.New(), uuid.New()) {
		req := httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(request.body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"PRIVACY_POLICY_UNAVAILABLE"`) {
			t.Fatalf("POST %s = %d %s, esperado 503 PRIVACY_POLICY_UNAVAILABLE", request.path, rec.Code, rec.Body)
		}
	}
	if len(deceasedService.created) != 0 || len(familyLinkService.created) != 0 {
		t.Fatal("serviço chamado sem política")
	}
}

func TestProductionFamilyLinkCreateValidation(t *testing.T) {
	deceasedService := newFakeDeceasedService()
	familyLinkService := newFakeProductionFamilyLinkService()
	policy := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
		RetentionUntil: time.Now().Add(24 * time.Hour),
	}}
	handler := newProductionDeceasedFamilyLinkTestHandler(t, deceasedService, familyLinkService, policy)
	deceasedID := uuid.NewString()

	cases := []struct {
		name string
		body string
	}{
		{"personId ausente", `{"relation":"filho","consentEvidenceRef":"` + testConsentEvidence + `"}`},
		{"relation ausente", `{"personId":"` + uuid.NewString() + `","consentEvidenceRef":"` + testConsentEvidence + `"}`},
		{"consentEvidenceRef com texto livre", `{"personId":"` + uuid.NewString() + `","relation":"child","consentEvidenceRef":"12345678901"}`},
		{"consentEvidenceRef UUID nulo", `{"personId":"` + uuid.NewString() + `","relation":"child","consentEvidenceRef":"00000000-0000-0000-0000-000000000000"}`},
		{"consentEvidenceRef ausente", `{"personId":"` + uuid.NewString() + `","relation":"filho"}`},
		{"campo desconhecido purposeCode", `{"personId":"` + uuid.NewString() + `","relation":"filho","consentEvidenceRef":"` + testConsentEvidence + `","purposeCode":"inventado"}`},
		{"campo desconhecido legalBasisCode", `{"personId":"` + uuid.NewString() + `","relation":"filho","consentEvidenceRef":"` + testConsentEvidence + `","legalBasisCode":"inventado"}`},
		{"campo desconhecido retentionUntil", `{"personId":"` + uuid.NewString() + `","relation":"filho","consentEvidenceRef":"` + testConsentEvidence + `","retentionUntil":"2030-01-01T00:00:00Z"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/deceased/"+deceasedID+"/family-links", strings.NewReader(testCase.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if len(familyLinkService.created) != 0 {
		t.Fatalf("serviço foi chamado com entrada inválida: created = %#v", familyLinkService.created)
	}
}

// TestProductionFamilyLinkCreateWithSyntheticPolicySucceeds exercita o
// caminho feliz com um PolicyProvider sintético (não aprovado por nenhum DPO
// real, ver comentário em syntheticPolicyProvider) para provar que a
// evidência chega intacta ao serviço e nunca é a origem de purpose/legal
// basis/retention — esses três só vêm do policy.Resolve.
func TestProductionFamilyLinkCreateWithSyntheticPolicySucceeds(t *testing.T) {
	deceasedService := newFakeDeceasedService()
	familyLinkService := newFakeProductionFamilyLinkService()
	policy := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
		RetentionUntil: time.Now().Add(24 * time.Hour),
	}}
	handler := newProductionDeceasedFamilyLinkTestHandler(t, deceasedService, familyLinkService, policy)

	deceasedID := uuid.NewString()
	personID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/v1/deceased/"+deceasedID+"/family-links", strings.NewReader(
		`{"personId":"`+personID+`","relation":"filho","consentEvidenceRef":"`+testConsentEvidence+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		ID       string `json:"id"`
		Relation string `json:"relation"`
		Person   struct {
			ID string `json:"id"`
		} `json:"person"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if response.Relation != "filho" || response.Person.ID != personID {
		t.Fatalf("resposta inesperada: %#v", response)
	}
	if len(familyLinkService.evidenceRef) != 1 || familyLinkService.evidenceRef[0] != uuid.MustParse(testConsentEvidence) {
		t.Fatalf("evidência recebida pelo serviço = %#v", familyLinkService.evidenceRef)
	}
}

func TestProductionFamilyLinkCreateMapsServiceErrors(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"falecido não encontrado", party.ErrFamilyLinkDeceasedNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"familiar não encontrado", party.ErrFamilyLinkPersonNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"pessoa jurídica", party.ErrFamilyLinkPersonNotApplicable, http.StatusConflict, "PARTY_KIND_INVALID"},
		{"autovínculo", party.ErrFamilyLinkSelf, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"relação inválida", party.ErrFamilyLinkRelationInvalid, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"duplicado", party.ErrFamilyLinkAlreadyExists, http.StatusConflict, "FAMILY_LINK_ALREADY_EXISTS"},
		{"política indisponível na resolução", audit.ErrPolicyUnavailable, http.StatusServiceUnavailable, "PRIVACY_POLICY_UNAVAILABLE"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deceasedService := newFakeDeceasedService()
			familyLinkService := newFakeProductionFamilyLinkService()
			familyLinkService.createErr = testCase.err
			policy := &syntheticPolicyProvider{policy: audit.Policy{
				PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
				RetentionUntil: time.Now().Add(24 * time.Hour),
			}}
			handler := newProductionDeceasedFamilyLinkTestHandler(t, deceasedService, familyLinkService, policy)

			req := httptest.NewRequest(http.MethodPost, "/v1/deceased/"+uuid.NewString()+"/family-links", strings.NewReader(
				`{"personId":"`+uuid.NewString()+`","relation":"filho","consentEvidenceRef":"`+testConsentEvidence+`"}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != testCase.want {
				t.Fatalf("status = %d, esperado %d: %s", rec.Code, testCase.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"`+testCase.code+`"`) {
				t.Fatalf("code ausente (%s): %s", testCase.code, rec.Body.String())
			}
		})
	}
}

func TestProductionFamilyLinkGetMapsNotFound(t *testing.T) {
	deceasedService := newFakeDeceasedService()
	familyLinkService := newFakeProductionFamilyLinkService()
	handler := newProductionDeceasedFamilyLinkTestHandler(t, deceasedService, familyLinkService, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/family-links/"+uuid.NewString(), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperado 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("code ausente: %s", rec.Body.String())
	}
}
