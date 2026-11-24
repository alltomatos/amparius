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

type fakeDeceasedService struct {
	mu        sync.Mutex
	items     map[uuid.UUID]map[uuid.UUID]party.Deceased
	created   []party.DeceasedCreate
	getErr    error
	createErr error
}

func newFakeDeceasedService() *fakeDeceasedService {
	return &fakeDeceasedService{items: make(map[uuid.UUID]map[uuid.UUID]party.Deceased)}
}

func (f *fakeDeceasedService) CreateDeceased(ctx context.Context, input party.DeceasedCreate) (party.Deceased, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.Deceased{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return party.Deceased{}, f.createErr
	}
	value := party.Deceased{
		ID:               uuid.New(),
		TenantID:         tenantID,
		PersonID:         input.PersonID,
		DeathAt:          input.DeathAt,
		DeathCertificate: input.DeathCertificate,
		BurialSite:       input.BurialSite,
		Person: party.Party{
			ID:         input.PersonID,
			Kind:       party.KindPerson,
			Name:       "Ana Civil",
			SocialName: stringPointer("Ana Social"),
		},
	}
	if f.items[tenantID] == nil {
		f.items[tenantID] = make(map[uuid.UUID]party.Deceased)
	}
	f.items[tenantID][value.ID] = value
	f.created = append(f.created, input)
	return value, nil
}

func (f *fakeDeceasedService) GetDeceasedByID(ctx context.Context, id uuid.UUID) (party.Deceased, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return party.Deceased{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return party.Deceased{}, f.getErr
	}
	value, ok := f.items[tenantID][id]
	if !ok {
		return party.Deceased{}, party.ErrDeceasedNotFound
	}
	return value, nil
}

func stringPointer(value string) *string { return &value }

func newDeceasedServer(t *testing.T, service party.DeceasedService, development bool) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpserver.New(logger, httpserver.Options{
		DevelopmentMode:     development,
		DevelopmentDeceased: party.NewDevelopmentDeceasedHTTPHandler(service),
	}).Handler()
}

func doDeceased(t *testing.T, handler http.Handler, method, path, tenantHeader, body string) *httptest.ResponseRecorder {
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

func TestDevelopmentDeceasedCreateAndGetUsePersonNames(t *testing.T) {
	service := newFakeDeceasedService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	created := doDeceased(t, newDeceasedServer(t, service, true), http.MethodPost, "/_dev/deceased", tenant,
		`{"person_id":"01935f4a-7b5d-7e01-8c2d-0000000000b1","death_at":"2026-11-24T12:00:00-03:00","death_certificate":"DC-123","burial_site":"Jazigo 7"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", created.Code, created.Body.String())
	}
	if created.Header().Get("X-Amparius-Development-Only") != "true" || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers dev-only/no-store ausentes: %#v", created.Header())
	}
	body := decodeDeceased(t, created)
	item := body["deceased"].(map[string]any)
	if item["civil_name"] != "Ana Civil" || item["social_name"] != "Ana Social" || item["display_name"] != "Ana Social" {
		t.Fatalf("nomes = %#v", item)
	}
	if _, ok := item["name"]; ok || strings.Contains(created.Body.String(), "tenant_id") {
		t.Fatalf("resposta duplicou nome ou vazou tenant: %s", created.Body.String())
	}
	id := item["id"].(string)
	got := doDeceased(t, newDeceasedServer(t, service, true), http.MethodGet, "/_dev/deceased/"+id, tenant, "")
	if got.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", got.Code, got.Body.String())
	}
	fetched := decodeDeceased(t, got)["deceased"].(map[string]any)
	if fetched["display_name"] != "Ana Social" || fetched["civil_name"] != "Ana Civil" {
		t.Fatalf("GET nomes = %#v", fetched)
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.created) != 1 || service.created[0].PersonID.String() != "01935f4a-7b5d-7e01-8c2d-0000000000b1" {
		t.Fatalf("entrada criada = %#v", service.created)
	}
}

func TestDevelopmentDeceasedRejectsTenantAndUnknownFields(t *testing.T) {
	service := newFakeDeceasedService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "tenant no corpo", body: `{"tenant_id":"01935f4a-7b5d-7e01-8c2d-0000000000b2","person_id":"01935f4a-7b5d-7e01-8c2d-0000000000b1","death_at":"2026-11-24T12:00:00Z"}`},
		{name: "campo desconhecido", body: `{"person_id":"01935f4a-7b5d-7e01-8c2d-0000000000b1","death_at":"2026-11-24T12:00:00Z","name":"não permitido"}`},
		{name: "data inválida", body: `{"person_id":"01935f4a-7b5d-7e01-8c2d-0000000000b1","death_at":"24/11/2026"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := doDeceased(t, newDeceasedServer(t, service, true), http.MethodPost, "/_dev/deceased", tenant, test.body)
			if rec.Code != http.StatusBadRequest || rec.Header().Get("X-Amparius-Development-Only") != "true" || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("resposta = %d, headers=%#v, body=%s", rec.Code, rec.Header(), rec.Body.String())
			}
		})
	}
}

func TestDevelopmentDeceasedMapsPersonAndServiceErrors(t *testing.T) {
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "pessoa ausente", err: party.ErrDeceasedPersonNotFound, want: http.StatusNotFound},
		{name: "pessoa jurídica", err: party.ErrDeceasedPersonNotApplicable, want: http.StatusConflict},
		{name: "erro interno", err: errors.New("PII interno não deve sair"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newFakeDeceasedService()
			service.createErr = test.err
			rec := doDeceased(t, newDeceasedServer(t, service, true), http.MethodPost, "/_dev/deceased", tenant,
				`{"person_id":"01935f4a-7b5d-7e01-8c2d-0000000000b1","death_at":"2026-11-24T12:00:00Z"}`)
			if rec.Code != test.want || strings.Contains(rec.Body.String(), "PII interno") {
				t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDevelopmentDeceasedAbsentWithoutDevelopmentMode(t *testing.T) {
	service := newFakeDeceasedService()
	const tenant = "01935f4a-7b5d-7e01-8c2d-0000000000a1"
	if rec := doDeceased(t, newDeceasedServer(t, service, false), http.MethodPost, "/_dev/deceased", tenant, `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("POST sem modo dev = %d", rec.Code)
	}
}

func decodeDeceased(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("JSON inválido: %v (%s)", err, rec.Body.String())
	}
	return value
}
