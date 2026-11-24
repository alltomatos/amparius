package party_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type fakePartyService struct {
	mu        sync.Mutex
	parties   map[uuid.UUID]map[uuid.UUID]party.Party
	createErr error
	getErr    error
	updateErr error
	created   []party.Party
	tenants   []uuid.UUID
}

func newFakePartyService() *fakePartyService {
	return &fakePartyService{parties: make(map[uuid.UUID]map[uuid.UUID]party.Party)}
}

func (f *fakePartyService) Create(ctx context.Context, value party.Party) (party.Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.Party{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants = append(f.tenants, tenantID)
	if f.createErr != nil {
		return party.Party{}, f.createErr
	}
	value.TenantID = tenantID
	value.ID = uuid.MustParse("01935f4a-7b5d-7e01-8c2d-0000000000" + fmt.Sprintf("%02d", len(f.created)))
	if f.parties[tenantID] == nil {
		f.parties[tenantID] = make(map[uuid.UUID]party.Party)
	}
	f.parties[tenantID][value.ID] = value
	f.created = append(f.created, value)
	return value, nil
}

func (f *fakePartyService) GetByID(ctx context.Context, id uuid.UUID) (party.Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.Party{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants = append(f.tenants, tenantID)
	if f.getErr != nil {
		return party.Party{}, f.getErr
	}
	if value, ok := f.parties[tenantID][id]; ok {
		return value, nil
	}
	return party.Party{}, party.ErrNotFound
}

func (f *fakePartyService) UpdateSocialName(ctx context.Context, id uuid.UUID, socialName *string) (party.Party, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.Party{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants = append(f.tenants, tenantID)
	if f.updateErr != nil {
		return party.Party{}, f.updateErr
	}
	value, ok := f.parties[tenantID][id]
	if !ok {
		return party.Party{}, party.ErrNotFound
	}
	if value.Kind != party.KindPerson {
		return party.Party{}, party.ErrSocialNameNotApplicable
	}
	if socialName == nil {
		value.SocialName = nil
	} else if trimmed := strings.TrimSpace(*socialName); trimmed == "" {
		value.SocialName = nil
	} else {
		value.SocialName = &trimmed
	}
	f.parties[tenantID][id] = value
	return value, nil
}

func newPartiesServer(t *testing.T, service party.Service, development bool) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpserver.New(logger, httpserver.Options{
		DevelopmentMode:    development,
		DevelopmentParties: party.NewDevelopmentHTTPHandler(service),
	}).Handler()
}

func doParties(t *testing.T, handler http.Handler, method, path, tenantHeader, body string) *httptest.ResponseRecorder {
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

func decodeParty(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("resposta não é JSON: %v (%s)", err, rec.Body.String())
	}
	return response
}

func TestDevelopmentPartiesCreateAndGetAreScopedByTenantHeader(t *testing.T) {
	service := newFakePartyService()
	handler := newPartiesServer(t, service, true)
	tenantA := "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	tenantB := "01935f4a-7b5d-7e01-8c2d-0000000000b2"

	created := doParties(t, handler, http.MethodPost, "/_dev/parties", tenantA, `{"name":"Maria da Silva","document":"123"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status do cadastro = %d: %s", created.Code, created.Body.String())
	}
	if got := created.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only = %q", got)
	}
	response := decodeParty(t, created)
	body := response["party"].(map[string]any)
	if body["kind"] != "person" || body["status"] != "active" || body["name"] != "Maria da Silva" {
		t.Fatalf("party criada = %#v", body)
	}
	if strings.Contains(created.Body.String(), "tenant_id") {
		t.Fatalf("resposta vazou tenant_id: %s", created.Body.String())
	}
	id := body["id"].(string)
	if got, want := created.Header().Get("Location"), "/_dev/parties/"+id; got != want {
		t.Fatalf("Location = %q, quer %q", got, want)
	}

	sameTenant := doParties(t, handler, http.MethodGet, "/_dev/parties/"+id, tenantA, "")
	if sameTenant.Code != http.StatusOK {
		t.Fatalf("status da consulta no tenant A = %d: %s", sameTenant.Code, sameTenant.Body.String())
	}
	if got := decodeParty(t, sameTenant)["party"].(map[string]any)["name"]; got != "Maria da Silva" {
		t.Fatalf("nome consultado = %v", got)
	}

	otherTenant := doParties(t, handler, http.MethodGet, "/_dev/parties/"+id, tenantB, "")
	if otherTenant.Code != http.StatusNotFound {
		t.Fatalf("isolamento: status no tenant B = %d, quer 404", otherTenant.Code)
	}
	if got := otherTenant.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only no 404 = %q", got)
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.created) != 1 {
		t.Fatalf("criações = %d, quer 1", len(service.created))
	}
	if service.created[0].TenantID.String() != tenantA {
		t.Fatalf("tenant recebido no repositório = %s, quer %s", service.created[0].TenantID, tenantA)
	}
}

func TestDevelopmentPartiesRejectsMissingOrInvalidTenantHeader(t *testing.T) {
	for _, header := range []string{"", "nao-e-uuid", "00000000-0000-0000-0000-000000000000", "01935f4a-7b5d-7e01-8c2d"} {
		name := header
		if name == "" {
			name = "ausente"
		}
		t.Run(name, func(t *testing.T) {
			service := newFakePartyService()
			rec := doParties(t, newPartiesServer(t, service, true), http.MethodPost, "/_dev/parties", header, `{"name":"Maria"}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, quer 400: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("X-Amparius-Development-Only"); got != "true" {
				t.Fatalf("marcador dev-only no erro = %q", got)
			}
			if len(service.tenants) != 0 {
				t.Fatalf("repositório foi chamado com tenant inválido")
			}
		})
	}
}

func TestDevelopmentPartiesRejectsInvalidPayloads(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	longName := strings.Repeat("a", 201)
	longDocument := strings.Repeat("1", 65)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "json inválido", body: `{"name":`},
		{name: "corpo não objeto", body: `["Maria"]`},
		{name: "corpo vazio", body: ``},
		{name: "nome ausente", body: `{}`},
		{name: "nome em branco", body: `{"name":"   "}`},
		{name: "nome longo", body: fmt.Sprintf(`{"name":%q}`, longName)},
		{name: "documento longo", body: fmt.Sprintf(`{"name":"Maria","document":%q}`, longDocument)},
		{name: "kind empresa", body: `{"kind":"company","name":"ACME"}`},
		{name: "kind desconhecido", body: `{"kind":"vehicle","name":"Maria"}`},
		{name: "status inválido", body: `{"name":"Maria","status":"deleted"}`},
		{name: "tenant no corpo", body: `{"name":"Maria","tenant_id":"01935f4a-7b5d-7e01-8c2d-0000000000a1"}`},
		{name: "campo desconhecido", body: `{"name":"Maria","cpf":"123"}`},
		{name: "dois json", body: `{"name":"Maria"} {"name":"João"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newFakePartyService()
			rec := doParties(t, newPartiesServer(t, service, true), http.MethodPost, "/_dev/parties", tenant, test.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, quer 400: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("X-Amparius-Development-Only"); got != "true" {
				t.Fatalf("marcador dev-only no erro = %q", got)
			}
			if len(service.created) != 0 {
				t.Fatalf("repositório foi chamado com carga inválida")
			}
		})
	}
}

func TestDevelopmentPartiesDefaultsDocumentAndStatus(t *testing.T) {
	service := newFakePartyService()
	rec := doParties(t, newPartiesServer(t, service, true), http.MethodPost, "/_dev/parties", "01935f4a-7b5d-7e01-8c2d-0000000000a1", `{"name":"  Maria  "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeParty(t, rec)["party"].(map[string]any)
	if created["name"] != "Maria" || created["document"] != "" || created["status"] != "active" || created["kind"] != "person" {
		t.Fatalf("defaults = %#v", created)
	}
}

func TestDevelopmentPartiesNotFoundAndTenantNotFound(t *testing.T) {
	service := newFakePartyService()
	handler := newPartiesServer(t, service, true)
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"

	missing := doParties(t, handler, http.MethodGet, "/_dev/parties/01935f4a-7b5d-7e01-8c2d-0000000000ff", tenant, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("status de party inexistente = %d: %s", missing.Code, missing.Body.String())
	}

	service.createErr = party.ErrTenantNotFound
	unknown := doParties(t, handler, http.MethodPost, "/_dev/parties", tenant, `{"name":"Maria"}`)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("status de tenant inexistente = %d: %s", unknown.Code, unknown.Body.String())
	}
	if !strings.Contains(unknown.Body.String(), "tenant não encontrado") {
		t.Fatalf("corpo = %s", unknown.Body.String())
	}
}

func TestDevelopmentPartiesDoesNotLeakInternalError(t *testing.T) {
	service := newFakePartyService()
	service.createErr = errors.New("detalhe interno com senha=segredo")
	rec := doParties(t, newPartiesServer(t, service, true), http.MethodPost, "/_dev/parties", "01935f4a-7b5d-7e01-8c2d-0000000000a1", `{"name":"Maria"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, quer 500: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "senha") || strings.Contains(rec.Body.String(), "detalhe interno") {
		t.Fatalf("erro interno vazou: %s", rec.Body.String())
	}
	if got := rec.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only = %q", got)
	}
}

func TestDevelopmentPartiesMethodAndIDValidation(t *testing.T) {
	service := newFakePartyService()
	handler := newPartiesServer(t, service, true)
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"

	deleteCollection := doParties(t, handler, http.MethodDelete, "/_dev/parties", tenant, "")
	if deleteCollection.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, quer 405", deleteCollection.Code)
	}
	if got := deleteCollection.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, quer POST", got)
	}

	postItem := doParties(t, handler, http.MethodPost, "/_dev/parties/01935f4a-7b5d-7e01-8c2d-0000000000ff", tenant, `{"name":"Maria"}`)
	if postItem.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, quer 405", postItem.Code)
	}
	if got := postItem.Header().Get("Allow"); got != http.MethodGet+", "+http.MethodPatch {
		t.Fatalf("Allow = %q, quer GET, PATCH", got)
	}

	invalidID := doParties(t, handler, http.MethodGet, "/_dev/parties/nao-e-uuid", tenant, "")
	if invalidID.Code != http.StatusBadRequest {
		t.Fatalf("status de id inválido = %d, quer 400", invalidID.Code)
	}
}

func TestDevelopmentPartiesAbsentWithoutDevelopmentMode(t *testing.T) {
	service := newFakePartyService()
	handler := newPartiesServer(t, service, false)
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"

	if rec := doParties(t, handler, http.MethodPost, "/_dev/parties", tenant, `{"name":"Maria"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("POST sem modo dev = %d, quer 404", rec.Code)
	}
	if rec := doParties(t, handler, http.MethodGet, "/_dev/parties/01935f4a-7b5d-7e01-8c2d-0000000000ff", tenant, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET sem modo dev = %d, quer 404", rec.Code)
	}
	if len(service.tenants) != 0 {
		t.Fatal("repositório foi chamado fora do modo dev")
	}
}

func TestDevelopmentPartiesSocialNameDisplaySemantics(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	for _, test := range []struct {
		name        string
		body        string
		wantSocial  any
		wantDisplay string
	}{
		{name: "ausente", body: `{"name":"Ana Civil"}`, wantSocial: nil, wantDisplay: "Ana Civil"},
		{name: "nulo", body: `{"name":"Ana Civil","social_name":null}`, wantSocial: nil, wantDisplay: "Ana Civil"},
		{name: "vazio", body: `{"name":"Ana Civil","social_name":""}`, wantSocial: nil, wantDisplay: "Ana Civil"},
		{name: "somente espaços", body: `{"name":"Ana Civil","social_name":"   "}`, wantSocial: nil, wantDisplay: "Ana Civil"},
		{name: "preenchido", body: `{"name":"Ana Civil","social_name":"  Ana Social  "}`, wantSocial: "Ana Social", wantDisplay: "Ana Social"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newFakePartyService()
			handler := newPartiesServer(t, service, true)
			created := doParties(t, handler, http.MethodPost, "/_dev/parties", tenant, test.body)
			if created.Code != http.StatusCreated {
				t.Fatalf("status = %d: %s", created.Code, created.Body.String())
			}
			body := decodeParty(t, created)["party"].(map[string]any)
			if body["name"] != "Ana Civil" || body["civil_name"] != "Ana Civil" {
				t.Fatalf("name/civil_name = %v/%v, esperado Ana Civil/Ana Civil", body["name"], body["civil_name"])
			}
			if body["display_name"] != test.wantDisplay {
				t.Fatalf("display_name = %v, esperado %s", body["display_name"], test.wantDisplay)
			}
			if body["social_name"] != test.wantSocial {
				t.Fatalf("social_name = %v, esperado %v", body["social_name"], test.wantSocial)
			}

			id := body["id"].(string)
			got := doParties(t, handler, http.MethodGet, "/_dev/parties/"+id, tenant, "")
			if got.Code != http.StatusOK {
				t.Fatalf("status da consulta = %d: %s", got.Code, got.Body.String())
			}
			fetched := decodeParty(t, got)["party"].(map[string]any)
			if fetched["name"] != "Ana Civil" || fetched["civil_name"] != "Ana Civil" ||
				fetched["display_name"] != test.wantDisplay || fetched["social_name"] != test.wantSocial {
				t.Fatalf("consulta = %#v", fetched)
			}
		})
	}
}

func TestDevelopmentPartiesSocialNameRejectsLongValue(t *testing.T) {
	service := newFakePartyService()
	longSocial := strings.Repeat("s", 201)
	rec := doParties(t, newPartiesServer(t, service, true), http.MethodPost, "/_dev/parties",
		"01935f4a-7b5d-7e01-8c2d-0000000000a1", fmt.Sprintf(`{"name":"Ana","social_name":%q}`, longSocial))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quer 400: %s", rec.Code, rec.Body.String())
	}
	if len(service.created) != 0 {
		t.Fatal("repositório foi chamado com social_name inválido")
	}
}

func TestDevelopmentPartiesPatchUpdatesOnlySocialName(t *testing.T) {
	service := newFakePartyService()
	handler := newPartiesServer(t, service, true)
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"

	created := doParties(t, handler, http.MethodPost, "/_dev/parties", tenant,
		`{"name":"Ana Civil","social_name":"Ana Social","document":"123"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status do cadastro = %d: %s", created.Code, created.Body.String())
	}
	partyCreated := decodeParty(t, created)["party"].(map[string]any)
	id := partyCreated["id"].(string)

	patch := func(body string) map[string]any {
		t.Helper()
		rec := doParties(t, handler, http.MethodPatch, "/_dev/parties/"+id, tenant, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status do PATCH = %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Amparius-Development-Only"); got != "true" {
			t.Fatalf("marcador dev-only = %q", got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q", got)
		}
		return decodeParty(t, rec)["party"].(map[string]any)
	}

	updated := patch(`{"social_name":"  Ana Nova  "}`)
	if updated["id"] != id || updated["name"] != "Ana Civil" || updated["civil_name"] != "Ana Civil" ||
		updated["document"] != "123" || updated["social_name"] != "Ana Nova" || updated["display_name"] != "Ana Nova" {
		t.Fatalf("party após atualizar = %#v", updated)
	}

	cleared := patch(`{"social_name":null}`)
	if cleared["social_name"] != nil || cleared["display_name"] != "Ana Civil" || cleared["name"] != "Ana Civil" {
		t.Fatalf("party após limpar com null = %#v", cleared)
	}

	patch(`{"social_name":"Ana Temporária"}`)
	clearedByEmpty := patch(`{"social_name":""}`)
	if clearedByEmpty["social_name"] != nil || clearedByEmpty["display_name"] != "Ana Civil" {
		t.Fatalf("party após limpar com vazio = %#v", clearedByEmpty)
	}

	patch(`{"social_name":"Ana de Novo"}`)
	clearedByBlank := patch(`{"social_name":"   "}`)
	if clearedByBlank["social_name"] != nil || clearedByBlank["display_name"] != "Ana Civil" {
		t.Fatalf("party após limpar com espaços = %#v", clearedByBlank)
	}

	got := doParties(t, handler, http.MethodGet, "/_dev/parties/"+id, tenant, "")
	fetched := decodeParty(t, got)["party"].(map[string]any)
	if fetched["name"] != "Ana Civil" || fetched["document"] != "123" || fetched["social_name"] != nil {
		t.Fatalf("consulta após PATCH = %#v", fetched)
	}
}

func TestDevelopmentPartiesPatchRejectsInvalidPayloads(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	longSocial := strings.Repeat("s", 201)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "json inválido", body: `{"social_name":`},
		{name: "corpo não objeto", body: `["Ana"]`},
		{name: "corpo vazio", body: ``},
		{name: "campo ausente", body: `{}`},
		{name: "campo desconhecido", body: `{"social_name":"Ana","name":"Outra"}`},
		{name: "tenant no corpo", body: `{"social_name":"Ana","tenant_id":"01935f4a-7b5d-7e01-8c2d-0000000000a1"}`},
		{name: "social não texto", body: `{"social_name":123}`},
		{name: "social longo", body: fmt.Sprintf(`{"social_name":%q}`, longSocial)},
		{name: "dois json", body: `{"social_name":"Ana"} {"social_name":"Bia"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newFakePartyService()
			handler := newPartiesServer(t, service, true)
			created := doParties(t, handler, http.MethodPost, "/_dev/parties", tenant, `{"name":"Ana Civil","social_name":"Ana Social"}`)
			id := decodeParty(t, created)["party"].(map[string]any)["id"].(string)

			rec := doParties(t, handler, http.MethodPatch, "/_dev/parties/"+id, tenant, test.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, quer 400: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("X-Amparius-Development-Only"); got != "true" {
				t.Fatalf("marcador dev-only no erro = %q", got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control no erro = %q", got)
			}
			stored := service.parties[uuid.MustParse(tenant)][uuid.MustParse(id)]
			if stored.SocialName == nil || *stored.SocialName != "Ana Social" {
				t.Fatalf("PATCH inválido alterou o nome social: %#v", stored.SocialName)
			}
		})
	}
}

func TestDevelopmentPartiesPatchRejectsCompanyAndWrongTenant(t *testing.T) {
	service := newFakePartyService()
	handler := newPartiesServer(t, service, true)
	tenantA := uuid.MustParse("01935f4a-7b5d-7e01-8c2d-0000000000a1")
	tenantB := uuid.MustParse("01935f4a-7b5d-7e01-8c2d-0000000000b2")

	companyID := uuid.MustParse("01935f4a-7b5d-7e01-8c2d-0000000000c3")
	service.parties[tenantA] = map[uuid.UUID]party.Party{
		companyID: {ID: companyID, TenantID: tenantA, Kind: party.KindCompany, Name: "ACME LTDA", Status: party.StatusActive},
	}
	company := doParties(t, handler, http.MethodPatch, "/_dev/parties/"+companyID.String(), tenantA.String(), `{"social_name":"ACME Social"}`)
	if company.Code != http.StatusConflict {
		t.Fatalf("PATCH em pessoa jurídica = %d, quer 409: %s", company.Code, company.Body.String())
	}
	if got := company.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only no 409 = %q", got)
	}

	created := doParties(t, handler, http.MethodPost, "/_dev/parties", tenantA.String(), `{"name":"Ana Civil","social_name":"Ana Social"}`)
	id := decodeParty(t, created)["party"].(map[string]any)["id"].(string)

	wrongTenant := doParties(t, handler, http.MethodPatch, "/_dev/parties/"+id, tenantB.String(), `{"social_name":"Invasão"}`)
	if wrongTenant.Code != http.StatusNotFound {
		t.Fatalf("PATCH em tenant errado = %d, quer 404: %s", wrongTenant.Code, wrongTenant.Body.String())
	}
	stored := service.parties[tenantA][uuid.MustParse(id)]
	if stored.SocialName == nil || *stored.SocialName != "Ana Social" {
		t.Fatalf("tenant errado alterou o nome social: %#v", stored.SocialName)
	}
}

func TestDevelopmentPartiesPatchMethodAndIDValidation(t *testing.T) {
	service := newFakePartyService()
	handler := newPartiesServer(t, service, true)
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"

	put := doParties(t, handler, http.MethodPut, "/_dev/parties/01935f4a-7b5d-7e01-8c2d-0000000000ff", tenant, `{"social_name":"Ana"}`)
	if put.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, quer 405", put.Code)
	}
	if got := put.Header().Get("Allow"); got != http.MethodGet+", "+http.MethodPatch {
		t.Fatalf("Allow = %q, quer GET, PATCH", got)
	}

	invalidID := doParties(t, handler, http.MethodPatch, "/_dev/parties/nao-e-uuid", tenant, `{"social_name":"Ana"}`)
	if invalidID.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quer 400", invalidID.Code)
	}
}
