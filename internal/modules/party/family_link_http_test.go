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

type fakeFamilyLinkService struct {
	mu        sync.Mutex
	items     map[uuid.UUID]map[uuid.UUID]party.FamilyLink
	created   []party.FamilyLinkCreate
	getErr    error
	createErr error
}

func newFakeFamilyLinkService() *fakeFamilyLinkService {
	return &fakeFamilyLinkService{items: make(map[uuid.UUID]map[uuid.UUID]party.FamilyLink)}
}

func (f *fakeFamilyLinkService) CreateFamilyLink(ctx context.Context, input party.FamilyLinkCreate) (party.FamilyLink, error) {
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
		Deceased: party.Party{
			ID:         input.DeceasedID,
			Kind:       party.KindPerson,
			Name:       "Maria Civil",
			SocialName: nil,
		},
		Person: party.Party{
			ID:         input.PersonID,
			Kind:       party.KindPerson,
			Name:       "João Civil",
			SocialName: stringPointer("João Social"),
		},
	}
	if f.items[tenantID] == nil {
		f.items[tenantID] = make(map[uuid.UUID]party.FamilyLink)
	}
	f.items[tenantID][value.ID] = value
	f.created = append(f.created, input)
	return value, nil
}

func (f *fakeFamilyLinkService) GetFamilyLinkByID(ctx context.Context, id uuid.UUID) (party.FamilyLink, error) {
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

func newFamilyLinkServer(t *testing.T, service party.FamilyLinkService, development bool) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpserver.New(logger, httpserver.Options{
		DevelopmentMode:        development,
		DevelopmentFamilyLinks: party.NewDevelopmentFamilyLinkHTTPHandler(service),
	}).Handler()
}

func doFamilyLink(t *testing.T, handler http.Handler, method, path, tenantHeader, body string) *httptest.ResponseRecorder {
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

func TestDevelopmentFamilyLinkCreateAndGetResolveNames(t *testing.T) {
	service := newFakeFamilyLinkService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	const deceased = "01935f4a-7b5d-7e01-8c2d-0000000000c1"
	const person = "01935f4a-7b5d-7e01-8c2d-0000000000c2"

	created := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodPost, "/_dev/family-links", tenant,
		`{"deceased_id":"`+deceased+`","person_id":"`+person+`","relation":"child"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", created.Code, created.Body.String())
	}
	if created.Header().Get("X-Amparius-Development-Only") != "true" || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers dev-only/no-store ausentes: %#v", created.Header())
	}
	body := decodeFamilyLink(t, created)
	link := body["family_link"].(map[string]any)
	if link["relation"] != "child" {
		t.Fatalf("relation = %#v", link["relation"])
	}
	deceasedObj := link["deceased"].(map[string]any)
	personObj := link["person"].(map[string]any)
	if deceasedObj["civil_name"] != "Maria Civil" || deceasedObj["display_name"] != "Maria Civil" || deceasedObj["social_name"] != nil {
		t.Fatalf("nomes do falecido = %#v", deceasedObj)
	}
	if personObj["civil_name"] != "João Civil" || personObj["social_name"] != "João Social" || personObj["display_name"] != "João Social" {
		t.Fatalf("nomes do familiar = %#v", personObj)
	}
	if _, ok := link["name"]; ok || strings.Contains(created.Body.String(), "tenant_id") {
		t.Fatalf("resposta duplicou nome ou vazou tenant: %s", created.Body.String())
	}
	if deceasedObj["id"] != deceased || personObj["id"] != person {
		t.Fatalf("ids resolvidos = %#v / %#v", deceasedObj["id"], personObj["id"])
	}

	id := link["id"].(string)
	got := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodGet, "/_dev/family-links/"+id, tenant, "")
	if got.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", got.Code, got.Body.String())
	}
	fetched := decodeFamilyLink(t, got)["family_link"].(map[string]any)
	if fetched["relation"] != "child" ||
		fetched["person"].(map[string]any)["display_name"] != "João Social" ||
		fetched["deceased"].(map[string]any)["civil_name"] != "Maria Civil" {
		t.Fatalf("GET vínculo = %#v", fetched)
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.created) != 1 || service.created[0].DeceasedID.String() != deceased ||
		service.created[0].PersonID.String() != person || service.created[0].Relation != "child" {
		t.Fatalf("entrada criada = %#v", service.created)
	}
}

func TestDevelopmentFamilyLinkRejectsTenantAndUnknownFields(t *testing.T) {
	service := newFakeFamilyLinkService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "tenant no corpo", body: `{"tenant_id":"01935f4a-7b5d-7e01-8c2d-0000000000b2","deceased_id":"01935f4a-7b5d-7e01-8c2d-0000000000c1","person_id":"01935f4a-7b5d-7e01-8c2d-0000000000c2","relation":"child"}`},
		{name: "campo desconhecido", body: `{"deceased_id":"01935f4a-7b5d-7e01-8c2d-0000000000c1","person_id":"01935f4a-7b5d-7e01-8c2d-0000000000c2","relation":"child","name":"não permitido"}`},
		{name: "deceased_id ausente", body: `{"person_id":"01935f4a-7b5d-7e01-8c2d-0000000000c2","relation":"child"}`},
		{name: "person_id inválido", body: `{"deceased_id":"01935f4a-7b5d-7e01-8c2d-0000000000c1","person_id":"nao-e-uuid","relation":"child"}`},
		{name: "relation ausente", body: `{"deceased_id":"01935f4a-7b5d-7e01-8c2d-0000000000c1","person_id":"01935f4a-7b5d-7e01-8c2d-0000000000c2"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodPost, "/_dev/family-links", tenant, test.body)
			if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amparius-Development-Only") != "true" || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("resposta = %d, headers=%#v, body=%s", rec.Code, rec.Header(), rec.Body.String())
			}
		})
	}
}

func TestDevelopmentFamilyLinkMapsServiceErrors(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	const body = `{"deceased_id":"01935f4a-7b5d-7e01-8c2d-0000000000c1","person_id":"01935f4a-7b5d-7e01-8c2d-0000000000c2","relation":"child"}`
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "falecido ausente", err: party.ErrFamilyLinkDeceasedNotFound, want: http.StatusNotFound},
		{name: "familiar ausente", err: party.ErrFamilyLinkPersonNotFound, want: http.StatusNotFound},
		{name: "pessoa jurídica", err: party.ErrFamilyLinkPersonNotApplicable, want: http.StatusConflict},
		{name: "relação inválida", err: party.ErrFamilyLinkRelationInvalid, want: http.StatusBadRequest},
		{name: "erro interno", err: errors.New("PII interno não deve sair"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newFakeFamilyLinkService()
			service.createErr = test.err
			rec := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodPost, "/_dev/family-links", tenant, body)
			if rec.Code != test.want || strings.Contains(rec.Body.String(), "PII interno") {
				t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDevelopmentFamilyLinkGetErrors(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	service := newFakeFamilyLinkService()
	if rec := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodGet, "/_dev/family-links/nao-e-uuid", tenant, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("id inválido = %d", rec.Code)
	}
	if rec := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodGet, "/_dev/family-links/"+uuid.NewString(), tenant, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("vínculo ausente = %d", rec.Code)
	}
	service.getErr = errors.New("erro interno")
	if rec := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodGet, "/_dev/family-links/"+uuid.NewString(), tenant, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("erro interno no GET = %d", rec.Code)
	}
}

func TestDevelopmentFamilyLinkRequiresTenantHeaderAndMethods(t *testing.T) {
	service := newFakeFamilyLinkService()
	if rec := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodPost, "/_dev/family-links", "", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("sem tenant = %d", rec.Code)
	}
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	if rec := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodDelete, "/_dev/family-links", tenant, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("método não permitido = %d", rec.Code)
	}
	if rec := doFamilyLink(t, newFamilyLinkServer(t, service, true), http.MethodPost, "/_dev/family-links/"+uuid.NewString(), tenant, `{}`); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST com id = %d", rec.Code)
	}
}

func TestDevelopmentFamilyLinkAbsentWithoutDevelopmentMode(t *testing.T) {
	service := newFakeFamilyLinkService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	if rec := doFamilyLink(t, newFamilyLinkServer(t, service, false), http.MethodPost, "/_dev/family-links", tenant, `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("POST sem modo dev = %d", rec.Code)
	}
}

func decodeFamilyLink(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("JSON inválido: %v (%s)", err, rec.Body.String())
	}
	return value
}
