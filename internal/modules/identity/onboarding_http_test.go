package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/identity"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
)

type fakeOnboarder struct {
	requests []identity.OnboardingRequest
	result   identity.OnboardingResult
	err      error
	seen     map[string]identity.OnboardingRequest
}

func (f *fakeOnboarder) Onboard(_ context.Context, request identity.OnboardingRequest) (identity.OnboardingResult, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return identity.OnboardingResult{}, f.err
	}
	if previous, ok := f.seen[request.OnboardingKey]; ok {
		if !reflect.DeepEqual(previous, request) {
			return identity.OnboardingResult{}, fmt.Errorf("%w: payload diferente", identity.ErrIdempotencyConflict)
		}
		return f.result, nil
	}
	f.seen[request.OnboardingKey] = request
	return f.result, nil
}

func newOnboardingServer(t *testing.T, fake *fakeOnboarder, development bool) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpserver.New(logger, httpserver.Options{
		DevelopmentMode:       development,
		DevelopmentOnboarding: identity.NewDevelopmentHTTPHandler(fake),
	}).Handler()
}

func newFake() *fakeOnboarder {
	return &fakeOnboarder{
		result: identity.OnboardingResult{
			TenantID:        uuid.MustParse("01935f4a-7b5d-7e01-8c2d-123456789001"),
			CompanyID:       uuid.MustParse("01935f4a-7b5d-7e02-8c2d-123456789002"),
			EstablishmentID: uuid.MustParse("01935f4a-7b5d-7e03-8c2d-123456789003"),
			OwnerUserID:     uuid.MustParse("01935f4a-7b5d-7e04-8c2d-123456789004"),
		},
		seen: make(map[string]identity.OnboardingRequest),
	}
}

func doOnboarding(t *testing.T, handler http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/_dev/onboarding", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDevelopmentOnboardingSuccessUsesSyntheticFortalezaData(t *testing.T) {
	fake := newFake()
	handler := newOnboardingServer(t, fake, true)
	rec := doOnboarding(t, handler, "http-success-1", `{"owner_name":"Owner de Teste"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only = %q, quer true", got)
	}
	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("resposta não é JSON: %v", err)
	}
	if response["tenant_id"] != fake.result.TenantID.String() || response["owner_user_id"] != fake.result.OwnerUserID.String() {
		t.Fatalf("IDs da resposta = %#v", response)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("chamadas ao repositório = %d, quer 1", len(fake.requests))
	}
	request := fake.requests[0]
	if request.City != "Fortaleza" || request.State != "CE" || request.Zip != "60000-000" {
		t.Fatalf("local sintético = %q/%q/%q", request.City, request.State, request.Zip)
	}
	if request.OwnerName != "Owner de Teste" {
		t.Fatalf("owner_name = %q", request.OwnerName)
	}
}

func TestDevelopmentOnboardingReplayEqualReturnsSameResponse(t *testing.T) {
	fake := newFake()
	handler := newOnboardingServer(t, fake, true)
	first := doOnboarding(t, handler, "http-replay-1", `{"owner_name":"Owner A"}`)
	replay := doOnboarding(t, handler, "http-replay-1", `{"owner_name":"Owner A"}`)

	if first.Code != http.StatusOK || replay.Code != http.StatusOK {
		t.Fatalf("status primeiro/replay = %d/%d", first.Code, replay.Code)
	}
	if first.Body.String() != replay.Body.String() {
		t.Fatalf("resposta do replay divergiu: primeiro=%s replay=%s", first.Body.String(), replay.Body.String())
	}
	if len(fake.requests) != 2 {
		t.Fatalf("chamadas ao repositório = %d, quer 2", len(fake.requests))
	}
}

func TestDevelopmentOnboardingReplayDifferentPayloadFails(t *testing.T) {
	fake := newFake()
	handler := newOnboardingServer(t, fake, true)
	first := doOnboarding(t, handler, "http-replay-2", `{"owner_name":"Owner A"}`)
	second := doOnboarding(t, handler, "http-replay-2", `{"owner_name":"Owner B"}`)

	if first.Code != http.StatusOK {
		t.Fatalf("primeiro status = %d", first.Code)
	}
	if second.Code != http.StatusConflict {
		t.Fatalf("status divergente = %d, quer %d", second.Code, http.StatusConflict)
	}
	if strings.Contains(second.Body.String(), "payload diferente") {
		t.Fatal("erro interno do repositório vazou na resposta")
	}
	if got := second.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only no conflito = %q", got)
	}
}

func TestDevelopmentOnboardingRejectsInvalidJSONAndTenant(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "json inválido", body: `{"owner_name":`},
		{name: "tenant no corpo", body: `{"tenant_id":"01935f4a-7b5d-7e01-8c2d-123456789001"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFake()
			rec := doOnboarding(t, newOnboardingServer(t, fake, true), "http-invalid-"+test.name, test.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, quer %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if len(fake.requests) != 0 {
				t.Fatalf("repositório foi chamado %d vez(es)", len(fake.requests))
			}
			if got := rec.Header().Get("X-Amparius-Development-Only"); got != "true" {
				t.Fatalf("marcador dev-only no erro = %q", got)
			}
		})
	}
}

func TestDevelopmentOnboardingValidatesMethodAndIdempotencyKey(t *testing.T) {
	fake := newFake()
	handler := newOnboardingServer(t, fake, true)

	get := httptest.NewRequest(http.MethodGet, "/_dev/onboarding", nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, quer %d", getResponse.Code, http.StatusMethodNotAllowed)
	}
	if got := getResponse.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only no método inválido = %q", got)
	}

	missingKey := httptest.NewRequest(http.MethodPost, "/_dev/onboarding", strings.NewReader(`{}`))
	missingKeyResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingKeyResponse, missingKey)
	if missingKeyResponse.Code != http.StatusBadRequest {
		t.Fatalf("chave ausente status = %d, quer %d", missingKeyResponse.Code, http.StatusBadRequest)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("repositório foi chamado com chave inválida")
	}
	if got := missingKeyResponse.Header().Get("X-Amparius-Development-Only"); got != "true" {
		t.Fatalf("marcador dev-only na chave inválida = %q", got)
	}
}

func TestDevelopmentOnboardingRepositoryErrorIsGeneric(t *testing.T) {
	fake := newFake()
	fake.err = errors.New("falha de banco com token-super-secreto")
	rec := doOnboarding(t, newOnboardingServer(t, fake, true), "http-error-1", `{}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, quer %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "token-super-secreto") {
		t.Fatal("erro do repositório vazou na resposta")
	}
}

func TestDevelopmentOnboardingRouteNotRegisteredOutsideDevelopment(t *testing.T) {
	fake := newFake()
	rec := doOnboarding(t, newOnboardingServer(t, fake, false), "http-prod-1", `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status fora de development = %d, quer %d", rec.Code, http.StatusNotFound)
	}
	if len(fake.requests) != 0 {
		t.Fatal("repositório foi chamado fora de development")
	}
}
