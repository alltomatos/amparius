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

// CNPJ sintético de teste (sequência válida, nunca documento real), conforme
// AGENTS.md #5.
const testSyntheticCNPJ = "12345678000195"

// fakeProductionLegalEntityService captura as entradas recebidas pelo
// handler autenticado para os oito métodos do contrato da issue #78.
type fakeProductionLegalEntityService struct {
	mu sync.Mutex

	legalEntities map[uuid.UUID]map[uuid.UUID]party.LegalEntity
	aggregates    map[uuid.UUID]map[uuid.UUID]party.LegalEntityAggregate
	contacts      map[uuid.UUID][]party.Contact
	addresses     map[uuid.UUID][]party.Address

	createdLegalEntities   []party.LegalEntityCreate
	createdRepresentatives []party.RepresentativeCreate
	createdContacts        []party.ContactCreate
	createdAddresses       []party.AddressCreate

	createErr         error
	searchErr         error
	getErr            error
	representativeErr error
	contactErr        error
	listContactsErr   error
	addressErr        error
	listAddressesErr  error

	searchResult []party.LegalEntity
}

func newFakeProductionLegalEntityService() *fakeProductionLegalEntityService {
	return &fakeProductionLegalEntityService{
		legalEntities: make(map[uuid.UUID]map[uuid.UUID]party.LegalEntity),
		aggregates:    make(map[uuid.UUID]map[uuid.UUID]party.LegalEntityAggregate),
		contacts:      make(map[uuid.UUID][]party.Contact),
		addresses:     make(map[uuid.UUID][]party.Address),
	}
}

func (f *fakeProductionLegalEntityService) CreateLegalEntity(ctx context.Context, input party.LegalEntityCreate) (party.LegalEntity, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.LegalEntity{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return party.LegalEntity{}, f.createErr
	}
	value := party.LegalEntity{
		ID:                    uuid.New(),
		TenantID:              tenantID,
		LegalName:             input.LegalName,
		CNPJ:                  input.CNPJ,
		TradeName:             input.TradeName,
		StateRegistration:     input.StateRegistration,
		MunicipalRegistration: input.MunicipalRegistration,
		Status:                party.StatusActive,
	}
	if f.legalEntities[tenantID] == nil {
		f.legalEntities[tenantID] = make(map[uuid.UUID]party.LegalEntity)
	}
	f.legalEntities[tenantID][value.ID] = value
	if f.aggregates[tenantID] == nil {
		f.aggregates[tenantID] = make(map[uuid.UUID]party.LegalEntityAggregate)
	}
	f.aggregates[tenantID][value.ID] = party.LegalEntityAggregate{LegalEntity: value}
	f.createdLegalEntities = append(f.createdLegalEntities, input)
	return value, nil
}

func (f *fakeProductionLegalEntityService) SearchLegalEntities(ctx context.Context, query string, limit int) ([]party.LegalEntity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if f.searchResult != nil {
		return f.searchResult, nil
	}
	return nil, nil
}

func (f *fakeProductionLegalEntityService) GetLegalEntityAggregate(ctx context.Context, id uuid.UUID) (party.LegalEntityAggregate, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.LegalEntityAggregate{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return party.LegalEntityAggregate{}, f.getErr
	}
	value, ok := f.aggregates[tenantID][id]
	if !ok {
		return party.LegalEntityAggregate{}, party.ErrLegalEntityNotFound
	}
	return value, nil
}

func (f *fakeProductionLegalEntityService) CreateRepresentativeWithAudit(ctx context.Context, input party.RepresentativeCreate, _ audit.PolicyProvider) (party.Representative, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.Representative{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.representativeErr != nil {
		return party.Representative{}, f.representativeErr
	}
	value := party.Representative{
		ID:        uuid.New(),
		TenantID:  tenantID,
		CompanyID: input.CompanyID,
		PersonID:  input.PersonID,
		Role:      input.Role,
		Person:    party.Party{ID: input.PersonID, Kind: party.KindPerson, Name: "Representante Civil"},
	}
	f.createdRepresentatives = append(f.createdRepresentatives, input)
	return value, nil
}

func (f *fakeProductionLegalEntityService) CreateContactWithAudit(ctx context.Context, input party.ContactCreate, _ audit.PolicyProvider) (party.Contact, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.Contact{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.contactErr != nil {
		return party.Contact{}, f.contactErr
	}
	value := party.Contact{
		ID: uuid.New(), TenantID: tenantID, PartyID: input.PartyID,
		Kind: input.Kind, Value: input.Value, Primary: input.Primary,
	}
	f.contacts[input.PartyID] = append(f.contacts[input.PartyID], value)
	f.createdContacts = append(f.createdContacts, input)
	return value, nil
}

func (f *fakeProductionLegalEntityService) ListContacts(ctx context.Context, partyID uuid.UUID) ([]party.Contact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listContactsErr != nil {
		return nil, f.listContactsErr
	}
	return f.contacts[partyID], nil
}

func (f *fakeProductionLegalEntityService) CreateAddressWithAudit(ctx context.Context, input party.AddressCreate, _ audit.PolicyProvider) (party.Address, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.Address{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addressErr != nil {
		return party.Address{}, f.addressErr
	}
	value := party.Address{
		ID: uuid.New(), TenantID: tenantID, PartyID: input.PartyID,
		Street: input.Street, Number: input.Number, City: input.City,
		State: input.State, Zip: input.Zip, Primary: input.Primary,
	}
	f.addresses[input.PartyID] = append(f.addresses[input.PartyID], value)
	f.createdAddresses = append(f.createdAddresses, input)
	return value, nil
}

func (f *fakeProductionLegalEntityService) ListAddresses(ctx context.Context, partyID uuid.UUID) ([]party.Address, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listAddressesErr != nil {
		return nil, f.listAddressesErr
	}
	return f.addresses[partyID], nil
}

func newProductionLegalEntityTestHandler(t *testing.T, service party.ProductionLegalEntityService, policy audit.PolicyProvider) http.Handler {
	t.Helper()
	principal := platformauth.Principal{
		Subject:          "unit-test-subject",
		TenantID:         uuid.New(),
		CompanyID:        uuid.New(),
		Roles:            []string{"owner"},
		MFAAuthenticated: true,
	}
	handler := party.NewProductionLegalEntityHTTPHandler(service, policy)
	return httpserver.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), httpserver.Options{
		Authenticator: alwaysAllowGate{principal: principal},
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "POST /v1/legal-entities", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.Create)},
			{Pattern: "GET /v1/legal-entities", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.Search)},
			{Pattern: "GET /v1/legal-entities/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.Get)},
			{Pattern: "POST /v1/legal-entities/{id}/representatives", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.CreateRepresentative)},
			{Pattern: "POST /v1/parties/{partyId}/contacts", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.CreateContact)},
			{Pattern: "GET /v1/parties/{partyId}/contacts", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.ListContacts)},
			{Pattern: "POST /v1/parties/{partyId}/addresses", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.CreateAddress)},
			{Pattern: "GET /v1/parties/{partyId}/addresses", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.ListAddresses)},
		},
	}).Handler()
}

// legalEntityMutationRequests enumera os três POSTs de dados pessoais do
// contrato (contato, endereço, representante), usados pelos testes de
// fail-closed sem política e sem autenticador.
func legalEntityMutationRequests(companyID, partyID uuid.UUID) []struct{ path, body string } {
	return []struct{ path, body string }{
		{"/v1/legal-entities/" + companyID.String() + "/representatives", `{"personId":"` + uuid.NewString() + `","role":"legal_representative"}`},
		{"/v1/parties/" + partyID.String() + "/contacts", `{"kind":"email","value":"contato@example.com"}`},
		{"/v1/parties/" + partyID.String() + "/addresses", `{"street":"Rua A","number":"10","city":"Fortaleza","state":"CE","zip":"60000000"}`},
	}
}

// TestProductionLegalEntityFailsClosedWithoutAuthenticator prova, sem banco,
// que as oito rotas novas seguem o mesmo fail-closed do CP-06 (ADR 0038):
// sem auth.Gate configurado, nenhuma requisição alcança o handler.
func TestProductionLegalEntityFailsClosedWithoutAuthenticator(t *testing.T) {
	handler := party.NewProductionLegalEntityHTTPHandler(nil, nil)
	server := httpserver.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), httpserver.Options{
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "POST /v1/legal-entities", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.Create)},
			{Pattern: "GET /v1/legal-entities", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.Search)},
			{Pattern: "GET /v1/legal-entities/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.Get)},
			{Pattern: "POST /v1/legal-entities/{id}/representatives", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.CreateRepresentative)},
			{Pattern: "POST /v1/parties/{partyId}/contacts", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.CreateContact)},
			{Pattern: "GET /v1/parties/{partyId}/contacts", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.ListContacts)},
			{Pattern: "POST /v1/parties/{partyId}/addresses", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.CreateAddress)},
			{Pattern: "GET /v1/parties/{partyId}/addresses", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.ListAddresses)},
		},
	}).Handler()

	companyID, partyID := uuid.New(), uuid.New()
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v1/legal-entities", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodGet, "/v1/legal-entities?q=x", nil),
		httptest.NewRequest(http.MethodGet, "/v1/legal-entities/"+companyID.String(), nil),
		httptest.NewRequest(http.MethodGet, "/v1/parties/"+partyID.String()+"/contacts", nil),
		httptest.NewRequest(http.MethodGet, "/v1/parties/"+partyID.String()+"/addresses", nil),
	}
	for _, mutation := range legalEntityMutationRequests(companyID, partyID) {
		requests = append(requests, httptest.NewRequest(http.MethodPost, mutation.path, strings.NewReader(mutation.body)))
	}

	for _, req := range requests {
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

// TestProductionLegalEntityMutationsFailClosedWithoutPolicy prova que, com
// autenticador configurado mas sem audit.PolicyProvider, os três POSTs de
// dados pessoais respondem 503 PRIVACY_POLICY_UNAVAILABLE sem jamais tocar o
// serviço (ADR 0042 §4).
func TestProductionLegalEntityMutationsFailClosedWithoutPolicy(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	handler := newProductionLegalEntityTestHandler(t, service, nil)

	for _, request := range legalEntityMutationRequests(uuid.New(), uuid.New()) {
		req := httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(request.body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"PRIVACY_POLICY_UNAVAILABLE"`) {
			t.Fatalf("POST %s = %d %s, esperado 503 PRIVACY_POLICY_UNAVAILABLE", request.path, rec.Code, rec.Body)
		}
	}
	if len(service.createdRepresentatives) != 0 || len(service.createdContacts) != 0 || len(service.createdAddresses) != 0 {
		t.Fatal("serviço chamado sem política")
	}
}

func TestProductionLegalEntityCreateValidation(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	handler := newProductionLegalEntityTestHandler(t, service, &syntheticPolicyProvider{})

	cases := []struct {
		name string
		body string
	}{
		{"legalName ausente", `{"cnpj":"` + testSyntheticCNPJ + `"}`},
		{"cnpj ausente", `{"legalName":"Padaria Pão Quente"}`},
		{"cnpj com dígito verificador inválido", `{"legalName":"Padaria Pão Quente","cnpj":"12345678000196"}`},
		{"campo desconhecido", `{"legalName":"Padaria Pão Quente","cnpj":"` + testSyntheticCNPJ + `","tenantId":"` + uuid.NewString() + `"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/legal-entities", strings.NewReader(testCase.body))
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
	if len(service.createdLegalEntities) != 0 {
		t.Fatalf("serviço chamado com entrada inválida: %#v", service.createdLegalEntities)
	}
}

func TestProductionLegalEntityCreatePersistsThroughFakeServiceAndIsVisibleInGet(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	handler := newProductionLegalEntityTestHandler(t, service, &syntheticPolicyProvider{})

	req := httptest.NewRequest(http.MethodPost, "/v1/legal-entities", strings.NewReader(
		`{"legalName":"Padaria Pão Quente","cnpj":"`+testSyntheticCNPJ+`","tradeName":"Pão Quente"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		LegalName string `json:"legalName"`
		CNPJ      string `json:"cnpj"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if response.Kind != party.KindCompany || response.LegalName != "Padaria Pão Quente" || response.CNPJ != testSyntheticCNPJ {
		t.Fatalf("resposta inesperada: %#v", response)
	}
	if len(service.createdLegalEntities) != 1 {
		t.Fatalf("criações = %d, esperado 1", len(service.createdLegalEntities))
	}

	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/v1/legal-entities/"+response.ID, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", getRec.Code, getRec.Body.String())
	}
	var aggregate struct {
		Contacts        []any `json:"contacts"`
		Addresses       []any `json:"addresses"`
		Representatives []any `json:"representatives"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &aggregate); err != nil {
		t.Fatalf("resposta do agregado inválida: %v", err)
	}
	if aggregate.Contacts == nil || aggregate.Addresses == nil || aggregate.Representatives == nil {
		t.Fatalf("agregado deve devolver listas vazias, nunca null: %s", getRec.Body.String())
	}
}

func TestProductionLegalEntityGetMapsServiceErrors(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"não encontrado", party.ErrLegalEntityNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"erro interno", errors.New("detalhe interno não deve sair"), http.StatusServiceUnavailable, "PARTY_UNAVAILABLE"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := newFakeProductionLegalEntityService()
			service.getErr = testCase.err
			handler := newProductionLegalEntityTestHandler(t, service, nil)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/legal-entities/"+uuid.NewString(), nil))
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

func TestProductionLegalEntitySearchRequiresQuery(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	handler := newProductionLegalEntityTestHandler(t, service, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/legal-entities", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"VALIDATION_ERROR"`) {
		t.Fatalf("code ausente: %s", rec.Body.String())
	}
}

func TestProductionLegalEntitySearchInvalidLimit(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	handler := newProductionLegalEntityTestHandler(t, service, nil)

	for _, limit := range []string{"0", "-1", "101", "abc"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/legal-entities?q=padaria&limit="+limit, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s status = %d, esperado 400: %s", limit, rec.Code, rec.Body.String())
		}
	}
}

func TestProductionLegalEntityCreateContactValidation(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	policy := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
		RetentionUntil: time.Now().Add(24 * time.Hour),
	}}
	handler := newProductionLegalEntityTestHandler(t, service, policy)
	partyID := uuid.NewString()

	cases := []struct {
		name string
		body string
	}{
		{"kind ausente", `{"value":"contato@example.com"}`},
		{"kind inválido", `{"kind":"telegram","value":"contato@example.com"}`},
		{"value ausente", `{"kind":"email"}`},
		{"e-mail malformado", `{"kind":"email","value":"não-é-email"}`},
		{"telefone com poucos dígitos", `{"kind":"phone","value":"123"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/parties/"+partyID+"/contacts", strings.NewReader(testCase.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if len(service.createdContacts) != 0 {
		t.Fatalf("serviço chamado com entrada inválida: %#v", service.createdContacts)
	}
}

func TestProductionLegalEntityCreateContactAndListSucceeds(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	policy := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
		RetentionUntil: time.Now().Add(24 * time.Hour),
	}}
	handler := newProductionLegalEntityTestHandler(t, service, policy)
	partyID := uuid.NewString()

	req := httptest.NewRequest(http.MethodPost, "/v1/parties/"+partyID+"/contacts", strings.NewReader(
		`{"kind":"email","value":"CONTATO@Example.com","primary":true}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201: %s", rec.Code, rec.Body.String())
	}

	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/v1/parties/"+partyID+"/contacts", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Items []struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].Value != "CONTATO@Example.com" {
		t.Fatalf("lista inesperada: %#v", listed.Items)
	}
}

func TestProductionLegalEntityCreateAddressValidation(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	policy := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
		RetentionUntil: time.Now().Add(24 * time.Hour),
	}}
	handler := newProductionLegalEntityTestHandler(t, service, policy)
	partyID := uuid.NewString()

	cases := []struct {
		name string
		body string
	}{
		{"street ausente", `{"number":"10","city":"Fortaleza","state":"CE","zip":"60000000"}`},
		{"state com mais de 2 letras", `{"street":"Rua A","number":"10","city":"Fortaleza","state":"CEA","zip":"60000000"}`},
		{"zip com menos de 8 dígitos", `{"street":"Rua A","number":"10","city":"Fortaleza","state":"CE","zip":"6000"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/parties/"+partyID+"/addresses", strings.NewReader(testCase.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if len(service.createdAddresses) != 0 {
		t.Fatalf("serviço chamado com entrada inválida: %#v", service.createdAddresses)
	}
}

func TestProductionLegalEntityCreateAddressAcceptsMaskedZip(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	policy := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
		RetentionUntil: time.Now().Add(24 * time.Hour),
	}}
	handler := newProductionLegalEntityTestHandler(t, service, policy)
	partyID := uuid.NewString()

	req := httptest.NewRequest(http.MethodPost, "/v1/parties/"+partyID+"/addresses", strings.NewReader(
		`{"street":"Rua A","number":"10","city":"Fortaleza","state":"ce","zip":"60000-000"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201: %s", rec.Code, rec.Body.String())
	}
	if len(service.createdAddresses) != 1 {
		t.Fatalf("criações = %d, esperado 1", len(service.createdAddresses))
	}
	created := service.createdAddresses[0]
	if created.State != "CE" || created.Zip != "60000000" {
		t.Fatalf("normalização inesperada: %#v", created)
	}
}

func TestProductionLegalEntityCreateRepresentativeValidationAndErrors(t *testing.T) {
	policy := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
		RetentionUntil: time.Now().Add(24 * time.Hour),
	}}
	companyID := uuid.NewString()

	t.Run("personId ausente", func(t *testing.T) {
		service := newFakeProductionLegalEntityService()
		handler := newProductionLegalEntityTestHandler(t, service, policy)
		req := httptest.NewRequest(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", strings.NewReader(`{"role":"legal_representative"}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("role ausente", func(t *testing.T) {
		service := newFakeProductionLegalEntityService()
		handler := newProductionLegalEntityTestHandler(t, service, policy)
		req := httptest.NewRequest(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", strings.NewReader(`{"personId":"`+uuid.NewString()+`"}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
		}
	})

	for _, testCase := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"pessoa não encontrada", party.ErrRepresentativePersonNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"pessoa jurídica como representante", party.ErrPartyKindInvalid, http.StatusConflict, "PARTY_KIND_INVALID"},
		{"papel inválido", party.ErrRepresentativeRoleInvalid, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"já vinculado", party.ErrRepresentativeAlreadyExists, http.StatusConflict, "REPRESENTATIVE_ALREADY_EXISTS"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := newFakeProductionLegalEntityService()
			service.representativeErr = testCase.err
			handler := newProductionLegalEntityTestHandler(t, service, policy)
			req := httptest.NewRequest(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", strings.NewReader(
				`{"personId":"`+uuid.NewString()+`","role":"legal_representative"}`))
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

func TestProductionLegalEntityCreateRepresentativeSucceeds(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	policy := &syntheticPolicyProvider{policy: audit.Policy{
		PurposeCode: "test.purpose", LegalBasisCode: "test.legal_basis",
		RetentionUntil: time.Now().Add(24 * time.Hour),
	}}
	handler := newProductionLegalEntityTestHandler(t, service, policy)
	companyID := uuid.NewString()
	personID := uuid.NewString()

	req := httptest.NewRequest(http.MethodPost, "/v1/legal-entities/"+companyID+"/representatives", strings.NewReader(
		`{"personId":"`+personID+`","role":"legal_representative"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		CompanyID   string `json:"companyId"`
		PersonID    string `json:"personId"`
		Role        string `json:"role"`
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if response.CompanyID != companyID || response.PersonID != personID || response.Role != "legal_representative" {
		t.Fatalf("resposta inesperada: %#v", response)
	}
	if len(service.createdRepresentatives) != 1 {
		t.Fatalf("criações = %d, esperado 1", len(service.createdRepresentatives))
	}
}

func TestProductionLegalEntityCreateLegalEntityMapsAlreadyExists(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	service.createErr = party.ErrLegalEntityAlreadyExists
	handler := newProductionLegalEntityTestHandler(t, service, &syntheticPolicyProvider{})

	req := httptest.NewRequest(http.MethodPost, "/v1/legal-entities", strings.NewReader(
		`{"legalName":"Padaria Pão Quente","cnpj":"`+testSyntheticCNPJ+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, esperado 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"LEGAL_ENTITY_ALREADY_EXISTS"`) {
		t.Fatalf("code ausente: %s", rec.Body.String())
	}
}

// TestProductionLegalEntityMethodNotAllowed prova que um método sem rota
// registrada em /v1/legal-entities é rejeitado com 405. O corpo vem do
// dispatch padrão do net/http.ServeMux (não do envelope JSON do handler),
// porque cada método tem seu próprio padrão em AuthenticatedRoutes — mesmo
// comportamento das demais rotas autenticadas de party (deceased, family
// links); o branch 405 dentro do próprio handler nunca é alcançado por esse
// caminho de registro e serve apenas de defesa caso o handler seja montado
// sem o ServeMux (por exemplo em teste direto).
func TestProductionLegalEntityMethodNotAllowed(t *testing.T) {
	service := newFakeProductionLegalEntityService()
	handler := newProductionLegalEntityTestHandler(t, service, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/legal-entities", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, esperado 405: %s", rec.Code, rec.Body.String())
	}
}
