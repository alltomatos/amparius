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

	"github.com/alltomatos/amparius/internal/modules/party"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
)

// CNPJ sintético dedicado aos testes do handler dev-only, distinto de
// testSyntheticCNPJ (usado pelos testes do handler autenticado) para evitar
// qualquer acoplamento entre os dois arquivos de teste.
const devSyntheticCNPJ = "11444777000161"

// fakeDevelopmentLegalEntityService implementa party.DevelopmentLegalEntityService
// sem auditoria e sem política de privacidade, como a fixture dev-only exige.
type fakeDevelopmentLegalEntityService struct {
	mu sync.Mutex

	legalEntities   map[uuid.UUID]map[uuid.UUID]party.LegalEntity
	aggregates      map[uuid.UUID]map[uuid.UUID]party.LegalEntityAggregate
	createdContacts []party.ContactCreate

	createErr         error
	getErr            error
	contactErr        error
	addressErr        error
	representativeErr error
}

func newFakeDevelopmentLegalEntityService() *fakeDevelopmentLegalEntityService {
	return &fakeDevelopmentLegalEntityService{
		legalEntities: make(map[uuid.UUID]map[uuid.UUID]party.LegalEntity),
		aggregates:    make(map[uuid.UUID]map[uuid.UUID]party.LegalEntityAggregate),
	}
}

func (f *fakeDevelopmentLegalEntityService) CreateLegalEntity(ctx context.Context, input party.LegalEntityCreate) (party.LegalEntity, error) {
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
	return value, nil
}

func (f *fakeDevelopmentLegalEntityService) SearchLegalEntities(ctx context.Context, query string, limit int) ([]party.LegalEntity, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []party.LegalEntity
	for _, entity := range f.legalEntities[tenantID] {
		if entity.LegalName == query && len(found) < limit {
			found = append(found, entity)
		}
	}
	return found, nil
}

func (f *fakeDevelopmentLegalEntityService) GetLegalEntityAggregate(ctx context.Context, id uuid.UUID) (party.LegalEntityAggregate, error) {
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

func (f *fakeDevelopmentLegalEntityService) CreateContact(ctx context.Context, input party.ContactCreate) (party.Contact, error) {
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
	f.createdContacts = append(f.createdContacts, input)
	f.appendContactToAggregates(tenantID, input.PartyID, value)
	return value, nil
}

func (f *fakeDevelopmentLegalEntityService) CreateAddress(ctx context.Context, input party.AddressCreate) (party.Address, error) {
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
	f.appendAddressToAggregates(tenantID, input.PartyID, value)
	return value, nil
}

func (f *fakeDevelopmentLegalEntityService) CreateRepresentative(ctx context.Context, input party.RepresentativeCreate) (party.Representative, error) {
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
	f.appendRepresentativeToAggregate(tenantID, input.CompanyID, value)
	return value, nil
}

// appendContactToAggregates e as duas funções seguintes atualizam o agregado
// em memória da pessoa jurídica para refletir o que o GET deve devolver,
// mimetizando a leitura real de contatos/endereços/representantes vinculados
// à party (contato e endereço não sabem a qual empresa pertencem: o teste só
// os anexa quando partyID coincide com o id da própria pessoa jurídica).
func (f *fakeDevelopmentLegalEntityService) appendContactToAggregates(tenantID, partyID uuid.UUID, value party.Contact) {
	if aggregate, ok := f.aggregates[tenantID][partyID]; ok {
		aggregate.Contacts = append(aggregate.Contacts, value)
		f.aggregates[tenantID][partyID] = aggregate
	}
}

func (f *fakeDevelopmentLegalEntityService) appendAddressToAggregates(tenantID, partyID uuid.UUID, value party.Address) {
	if aggregate, ok := f.aggregates[tenantID][partyID]; ok {
		aggregate.Addresses = append(aggregate.Addresses, value)
		f.aggregates[tenantID][partyID] = aggregate
	}
}

func (f *fakeDevelopmentLegalEntityService) appendRepresentativeToAggregate(tenantID, companyID uuid.UUID, value party.Representative) {
	if aggregate, ok := f.aggregates[tenantID][companyID]; ok {
		aggregate.Representatives = append(aggregate.Representatives, value)
		f.aggregates[tenantID][companyID] = aggregate
	}
}

func newDevelopmentLegalEntityServer(service party.DevelopmentLegalEntityService, development bool) http.Handler {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpserver.New(logger, httpserver.Options{
		DevelopmentMode:          development,
		DevelopmentLegalEntities: party.NewDevelopmentLegalEntityHTTPHandler(service),
	}).Handler()
}

func doLegalEntity(t *testing.T, handler http.Handler, method, path, tenantHeader, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if tenantHeader != "" {
		req.Header.Set("X-Amparius-Development-Tenant-ID", tenantHeader)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDevelopmentLegalEntityCreateGetContactAddressRepresentative(t *testing.T) {
	service := newFakeDevelopmentLegalEntityService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"

	created := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost, "/_dev/legal-entities", tenant,
		`{"legalName":"Padaria Pão Quente","cnpj":"`+devSyntheticCNPJ+`","tradeName":"Pão Quente"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", created.Code, created.Body.String())
	}
	if created.Header().Get("X-Amparius-Development-Only") != "true" || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers dev-only/no-store ausentes: %#v", created.Header())
	}
	var createdBody struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		LegalName string `json:"legalName"`
		CNPJ      string `json:"cnpj"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatalf("resposta inválida: %v (%s)", err, created.Body.String())
	}
	if createdBody.Kind != party.KindCompany || createdBody.LegalName != "Padaria Pão Quente" || createdBody.CNPJ != devSyntheticCNPJ {
		t.Fatalf("resposta inesperada: %#v", createdBody)
	}
	if strings.Contains(created.Body.String(), "tenant_id") || strings.Contains(created.Body.String(), "tenantId") {
		t.Fatalf("resposta vazou tenant: %s", created.Body.String())
	}
	companyID := createdBody.ID

	personID := uuid.NewString()
	representative := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost,
		"/_dev/legal-entities/"+companyID+"/representatives", tenant,
		`{"personId":"`+personID+`","role":"legal_representative"}`)
	if representative.Code != http.StatusCreated {
		t.Fatalf("representative status = %d: %s", representative.Code, representative.Body.String())
	}

	contact := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost,
		"/_dev/parties/"+companyID+"/contacts", tenant,
		`{"kind":"email","value":"contato@example.com","primary":true}`)
	if contact.Code != http.StatusCreated {
		t.Fatalf("contact status = %d: %s", contact.Code, contact.Body.String())
	}

	address := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost,
		"/_dev/parties/"+companyID+"/addresses", tenant,
		`{"street":"Rua A","number":"10","city":"Fortaleza","state":"CE","zip":"60000-000","primary":true}`)
	if address.Code != http.StatusCreated {
		t.Fatalf("address status = %d: %s", address.Code, address.Body.String())
	}

	got := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodGet, "/_dev/legal-entities/"+companyID, tenant, "")
	if got.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", got.Code, got.Body.String())
	}
	var aggregate struct {
		Contacts []struct {
			Value   string `json:"value"`
			Primary bool   `json:"primary"`
		} `json:"contacts"`
		Addresses []struct {
			City  string `json:"city"`
			State string `json:"state"`
			Zip   string `json:"zip"`
		} `json:"addresses"`
		Representatives []struct {
			PersonID string `json:"personId"`
			Role     string `json:"role"`
		} `json:"representatives"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &aggregate); err != nil {
		t.Fatalf("resposta do agregado inválida: %v (%s)", err, got.Body.String())
	}
	if len(aggregate.Contacts) != 1 || aggregate.Contacts[0].Value != "contato@example.com" || !aggregate.Contacts[0].Primary {
		t.Fatalf("contatos do agregado = %#v", aggregate.Contacts)
	}
	if len(aggregate.Addresses) != 1 || aggregate.Addresses[0].State != "CE" || aggregate.Addresses[0].Zip != "60000000" {
		t.Fatalf("endereços do agregado = %#v", aggregate.Addresses)
	}
	if len(aggregate.Representatives) != 1 || aggregate.Representatives[0].PersonID != personID || aggregate.Representatives[0].Role != "legal_representative" {
		t.Fatalf("representantes do agregado = %#v", aggregate.Representatives)
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.createdContacts) != 1 {
		t.Fatalf("contatos criados = %#v", service.createdContacts)
	}
}

func TestDevelopmentLegalEntityRejectsTenantAndUnknownFields(t *testing.T) {
	service := newFakeDevelopmentLegalEntityService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "tenant no corpo", body: `{"tenantId":"01935f4a-7b5d-7e01-8c2d-0000000000b2","legalName":"Padaria Pão Quente","cnpj":"` + devSyntheticCNPJ + `"}`},
		{name: "companyId no corpo", body: `{"companyId":"01935f4a-7b5d-7e01-8c2d-0000000000b2","legalName":"Padaria Pão Quente","cnpj":"` + devSyntheticCNPJ + `"}`},
		{name: "kind no corpo", body: `{"kind":"company","legalName":"Padaria Pão Quente","cnpj":"` + devSyntheticCNPJ + `"}`},
		{name: "status no corpo", body: `{"status":"active","legalName":"Padaria Pão Quente","cnpj":"` + devSyntheticCNPJ + `"}`},
		{name: "campo desconhecido", body: `{"legalName":"Padaria Pão Quente","cnpj":"` + devSyntheticCNPJ + `","apelido":"não permitido"}`},
		{name: "legalName ausente", body: `{"cnpj":"` + devSyntheticCNPJ + `"}`},
		{name: "cnpj ausente", body: `{"legalName":"Padaria Pão Quente"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost, "/_dev/legal-entities", tenant, test.body)
			if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amparius-Development-Only") != "true" || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("resposta = %d, headers=%#v, body=%s", rec.Code, rec.Header(), rec.Body.String())
			}
		})
	}
}

func TestDevelopmentLegalEntityMapsServiceErrors(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	const body = `{"personId":"01935f4a-7b5d-7e01-8c2d-0000000000c2","role":"legal_representative"}`
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "representante ausente", err: party.ErrRepresentativePersonNotFound, want: http.StatusNotFound},
		{name: "pessoa jurídica como representante", err: party.ErrPartyKindInvalid, want: http.StatusConflict},
		{name: "papel inválido", err: party.ErrRepresentativeRoleInvalid, want: http.StatusBadRequest},
		{name: "representante duplicado", err: party.ErrRepresentativeAlreadyExists, want: http.StatusConflict},
		{name: "party ausente", err: party.ErrNotFound, want: http.StatusNotFound},
		{name: "erro interno", err: errors.New("PII interno não deve sair"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newFakeDevelopmentLegalEntityService()
			service.representativeErr = test.err
			rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost,
				"/_dev/legal-entities/"+uuid.NewString()+"/representatives", tenant, body)
			if rec.Code != test.want || strings.Contains(rec.Body.String(), "PII interno") {
				t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDevelopmentLegalEntityCreateMapsAlreadyExists(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	service := newFakeDevelopmentLegalEntityService()
	service.createErr = party.ErrLegalEntityAlreadyExists
	rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost, "/_dev/legal-entities", tenant,
		`{"legalName":"Padaria Pão Quente","cnpj":"`+devSyntheticCNPJ+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDevelopmentLegalEntityGetErrors(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	service := newFakeDevelopmentLegalEntityService()
	if rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodGet, "/_dev/legal-entities/nao-e-uuid", tenant, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("id inválido = %d", rec.Code)
	}
	if rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodGet, "/_dev/legal-entities/"+uuid.NewString(), tenant, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("pessoa jurídica ausente = %d", rec.Code)
	}
	service.getErr = errors.New("erro interno")
	if rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodGet, "/_dev/legal-entities/"+uuid.NewString(), tenant, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("erro interno no GET = %d", rec.Code)
	}
}

func TestDevelopmentLegalEntityRequiresTenantHeaderAndMethods(t *testing.T) {
	service := newFakeDevelopmentLegalEntityService()
	if rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost, "/_dev/legal-entities", "", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("sem tenant = %d", rec.Code)
	}
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	if rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodDelete, "/_dev/legal-entities", tenant, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("método não permitido = %d", rec.Code)
	}
	if rec := doLegalEntity(t, newDevelopmentLegalEntityServer(service, true), http.MethodPost, "/_dev/legal-entities/"+uuid.NewString(), tenant, `{}`); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST com id = %d", rec.Code)
	}
}

func TestDevelopmentLegalEntitySearchRequiresTenantAndValidParams(t *testing.T) {
	handler := newDevelopmentLegalEntityServer(newFakeDevelopmentLegalEntityService(), true)
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	for _, check := range []struct {
		query, tenant string
	}{
		{"?q=Empresa", ""},
		{"", tenant},
		{"?q=Empresa&limit=101", tenant},
	} {
		rec := doLegalEntity(t, handler, http.MethodGet, "/_dev/legal-entities"+check.query, check.tenant, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %q = %d %s, esperado 400", check.query, rec.Code, rec.Body)
		}
	}
}

func TestDevelopmentLegalEntityAbsentWithoutDevelopmentMode(t *testing.T) {
	service := newFakeDevelopmentLegalEntityService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	handler := newDevelopmentLegalEntityServer(service, false)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if rec := doLegalEntity(t, handler, method, "/_dev/legal-entities?q=Empresa", tenant, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s sem modo dev = %d", method, rec.Code)
		}
	}
}
