package party_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/party"
	"github.com/alltomatos/amparius/internal/platform/db"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestDevelopmentPartiesHTTPIntegration exercita o handler completo contra
// PostgreSQL real: cadastro no tenant A, leitura no mesmo tenant e 404 no
// tenant B, provando que o cabeçalho vira contexto de tenant antes do RLS.
func TestDevelopmentPartiesHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("AMPA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AMPA_TEST_POSTGRES_DSN não definido")
	}

	ctx := context.Background()
	database, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository, err := party.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}

	tenantA, tenantB := uuid.New(), uuid.New()
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id, name, document, regime, status) VALUES ($1,$2,$3,$4,$5)`,
				tenantID, "Teste party "+tenantID.String(), "doc-"+tenantID.String(), "economic_group", "active")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger, httpserver.Options{
		DevelopmentMode:    true,
		DevelopmentParties: party.NewDevelopmentHTTPHandler(repository),
	}).Handler()

	request := func(method, path string, tenantID uuid.UUID, body string) *httptest.ResponseRecorder {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("X-Amparius-Development-Tenant-ID", tenantID.String())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	created := request(http.MethodPost, "/_dev/parties", tenantA, `{"name":"Maria Integração","document":"123"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status do cadastro = %d: %s", created.Code, created.Body.String())
	}
	var createResponse struct {
		Party struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"party"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createResponse); err != nil {
		t.Fatalf("resposta inválida: %v", err)
	}
	if createResponse.Party.Name != "Maria Integração" {
		t.Fatalf("nome criado = %q", createResponse.Party.Name)
	}
	path := "/_dev/parties/" + createResponse.Party.ID

	if same := request(http.MethodGet, path, tenantA, ""); same.Code != http.StatusOK {
		t.Fatalf("consulta no tenant A = %d: %s", same.Code, same.Body.String())
	}
	if other := request(http.MethodGet, path, tenantB, ""); other.Code != http.StatusNotFound {
		t.Fatalf("consulta no tenant B = %d, quer 404: %s", other.Code, other.Body.String())
	}
	if unknown := request(http.MethodPost, "/_dev/parties", uuid.New(), `{"name":"Maria"}`); unknown.Code != http.StatusNotFound {
		t.Fatalf("cadastro com tenant inexistente = %d, quer 404: %s", unknown.Code, unknown.Body.String())
	}
}
