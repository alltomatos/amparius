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
	"testing"

	"github.com/alltomatos/amparius/internal/modules/party"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
)

// SearchPersons completa fakePartyService (declarado em http_test.go) para
// servir de ProductionService nos testes unitários do handler /v1/parties.
// Filtra kind=person, igual ao SQL real de party.Repository.SearchPersons
// (issue #72 correção), para que o fake nunca mascare uma regressão desse
// filtro nos testes que não usam Postgres.
func (f *fakePartyService) SearchPersons(ctx context.Context, query string, limit int) ([]party.Party, error) {
	if query == "" {
		return nil, party.ErrInvalidSearch
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []party.Party
	for _, byID := range f.parties {
		for _, value := range byID {
			if value.Kind != party.KindPerson {
				continue
			}
			if strings.Contains(strings.ToLower(value.Name), strings.ToLower(query)) {
				result = append(result, value)
			}
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// alwaysAllowGate é um duplo de teste confinado a este arquivo: autentica
// qualquer bearer com um principal fixo e nunca nega permissão. Nunca é
// usado no bootstrap de produção (cmd/api), só nestes testes de handler.
type alwaysAllowGate struct{ principal platformauth.Principal }

func (g alwaysAllowGate) Authenticate(context.Context, string) (platformauth.Principal, error) {
	return g.principal, nil
}

func (g alwaysAllowGate) Allows(platformauth.Principal, platformauth.Permission) bool { return true }

func newProductionTestHandler(t *testing.T, service party.ProductionService) http.Handler {
	t.Helper()
	principal := platformauth.Principal{
		Subject:          "unit-test-subject",
		TenantID:         uuid.New(),
		CompanyID:        uuid.New(),
		Roles:            []string{"owner"},
		MFAAuthenticated: true,
	}
	handler := party.NewProductionHTTPHandler(service)
	return httpserver.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), httpserver.Options{
		Authenticator: alwaysAllowGate{principal: principal},
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "POST /v1/parties", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.Create)},
			{Pattern: "GET /v1/parties", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.Search)},
		},
	}).Handler()
}

// TestProductionPartiesFailsClosedWithoutAuthenticator prova, sem banco, que
// /v1/parties segue o mesmo fail-closed do CP-06 (ADR 0038): sem auth.Gate
// configurado, nenhuma requisição alcança o handler e a resposta é 503.
func TestProductionPartiesFailsClosedWithoutAuthenticator(t *testing.T) {
	handler := party.NewProductionHTTPHandler(nil)
	server := httpserver.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), httpserver.Options{
		AuthenticatedRoutes: []httpserver.AuthenticatedRoute{
			{Pattern: "POST /v1/parties", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(handler.Create)},
			{Pattern: "GET /v1/parties", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(handler.Search)},
		},
	}).Handler()

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v1/parties", strings.NewReader(`{"name":"Maria"}`)),
		httptest.NewRequest(http.MethodGet, "/v1/parties?q=Maria", nil),
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

func TestProductionPartiesCreateValidation(t *testing.T) {
	service := newFakePartyService()
	handler := newProductionTestHandler(t, service)

	secretSocialName := "Nome-Social-Confidencial-Para-Nao-Vazar"

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"nome ausente", `{"social_name":"` + secretSocialName + `"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"nome em branco", `{"name":"   "}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"campo desconhecido tenant_id", `{"name":"Maria","tenant_id":"` + uuid.NewString() + `"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"campo desconhecido kind", `{"name":"Maria","kind":"company"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"campo desconhecido status", `{"name":"Maria","status":"inactive"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"nome excede limite", `{"name":"` + strings.Repeat("A", 201) + `"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/parties", strings.NewReader(testCase.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != testCase.wantStatus {
				t.Fatalf("status = %d, esperado %d: %s", rec.Code, testCase.wantStatus, rec.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("resposta inválida: %v", err)
			}
			if payload["code"] != testCase.wantCode {
				t.Fatalf("code = %v, esperado %q", payload["code"], testCase.wantCode)
			}
			if strings.Contains(rec.Body.String(), secretSocialName) {
				t.Fatalf("resposta de erro vazou dado pessoal submetido: %s", rec.Body.String())
			}
		})
	}
}

func TestProductionPartiesCreatePersistsThroughFakeService(t *testing.T) {
	service := newFakePartyService()
	handler := newProductionTestHandler(t, service)

	req := httptest.NewRequest(http.MethodPost, "/v1/parties", strings.NewReader(`{"name":"Maria da Silva","document":"12345678900"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "" {
		t.Fatalf("cabeçalho Location presente sem rota GET /v1/parties/{id}: %q", rec.Header().Get("Location"))
	}
	var response struct {
		ID          string  `json:"id"`
		Kind        string  `json:"kind"`
		CivilName   string  `json:"civilName"`
		DisplayName string  `json:"displayName"`
		SocialName  *string `json:"socialName"`
		Status      string  `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if response.Kind != party.KindPerson || response.CivilName != "Maria da Silva" || response.DisplayName != "Maria da Silva" || response.Status != party.StatusActive {
		t.Fatalf("resposta inesperada: %#v", response)
	}
	if len(service.created) != 1 {
		t.Fatalf("criações no serviço = %d, esperado 1", len(service.created))
	}
}

func TestProductionPartiesSearchValidation(t *testing.T) {
	service := newFakePartyService()
	handler := newProductionTestHandler(t, service)

	for _, limit := range []string{"0", "-1", "101", "abc"} {
		t.Run("limit="+limit, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/parties?q=Maria&limit="+limit, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestProductionPartiesSearchRequiresQuery prova que q é obrigatório em
// /v1/parties (correção da issue #72: o contrato antigo dizia "opcional",
// mas o código sempre rejeitou busca vazia; agora ambos estão alinhados).
func TestProductionPartiesSearchRequiresQuery(t *testing.T) {
	service := newFakePartyService()
	handler := newProductionTestHandler(t, service)

	for _, path := range []string{"/v1/parties", "/v1/parties?q=", "/v1/parties?q=%20%20"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, esperado 400: %s", rec.Code, rec.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("resposta inválida: %v", err)
			}
			if payload["code"] != "VALIDATION_ERROR" {
				t.Fatalf("code = %v, esperado VALIDATION_ERROR", payload["code"])
			}
		})
	}
}

// TestProductionPartiesSearchOnlyReturnsPersons prova, com fake service, que
// uma pessoa jurídica com o mesmo termo nunca aparece em /v1/parties — a
// mesma regra que a integração prova com SQL real e Postgres.
func TestProductionPartiesSearchOnlyReturnsPersons(t *testing.T) {
	service := newFakePartyService()
	handler := newProductionTestHandler(t, service)

	create := httptest.NewRequest(http.MethodPost, "/v1/parties", strings.NewReader(`{"name":"Zeta Compartilhado"}`))
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("setup de pessoa física falhou: %d %s", createRec.Code, createRec.Body.String())
	}

	// Injeta diretamente no fake uma empresa com o mesmo termo, mais recente
	// que a pessoa física — simula o cenário que a correção da issue #72
	// precisa recusar.
	companyID := uuid.New()
	tenantID := service.created[0].TenantID
	service.mu.Lock()
	service.parties[tenantID][companyID] = party.Party{
		ID: companyID, TenantID: tenantID, Kind: party.KindCompany,
		Name: "Zeta Compartilhado LTDA", Status: party.StatusActive,
	}
	service.mu.Unlock()

	search := httptest.NewRequest(http.MethodGet, "/v1/parties?q=Zeta+Compartilhado", nil)
	searchRec := httptest.NewRecorder()
	handler.ServeHTTP(searchRec, search)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200: %s", searchRec.Code, searchRec.Body.String())
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("items = %d, esperado 1 (só a pessoa física): %s", len(payload.Items), searchRec.Body.String())
	}
	if payload.Items[0]["kind"] != party.KindPerson {
		t.Fatalf("kind = %v, esperado person", payload.Items[0]["kind"])
	}
}

func TestProductionPartiesSearchReturnsItems(t *testing.T) {
	service := newFakePartyService()
	handler := newProductionTestHandler(t, service)

	create := httptest.NewRequest(http.MethodPost, "/v1/parties", strings.NewReader(`{"name":"Joana Teste"}`))
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("setup de busca falhou: %d %s", createRec.Code, createRec.Body.String())
	}

	search := httptest.NewRequest(http.MethodGet, "/v1/parties?q=Joana", nil)
	searchRec := httptest.NewRecorder()
	handler.ServeHTTP(searchRec, search)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200: %s", searchRec.Code, searchRec.Body.String())
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("items = %d, esperado 1: %s", len(payload.Items), searchRec.Body.String())
	}
}

func TestProductionPartiesCreateUnavailableWhenServiceFails(t *testing.T) {
	service := newFakePartyService()
	service.createErr = errors.New("indisponível")
	handler := newProductionTestHandler(t, service)

	req := httptest.NewRequest(http.MethodPost, "/v1/parties", strings.NewReader(`{"name":"Maria"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, esperado 503: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"PARTY_UNAVAILABLE"`) {
		t.Fatalf("code ausente: %s", rec.Body.String())
	}
}
