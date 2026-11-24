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
	"github.com/jackc/pgx/v5/pgtype"
)

// TestDevelopmentPartiesSocialNameHTTPIntegration exercita a fatia de nome
// social contra PostgreSQL real: persistência em party.person, resposta aditiva
// e alteração posterior do nome social sem mudar o nome civil.
func TestDevelopmentPartiesSocialNameHTTPIntegration(t *testing.T) {
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

	tenantID := uuid.New()
	if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO identity.tenant (id, name, document, regime, status) VALUES ($1,$2,$3,$4,$5)`,
			tenantID, "Nome social "+tenantID.String(), "doc-"+tenantID.String(), "economic_group", "active")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpserver.New(logger, httpserver.Options{
		DevelopmentMode:    true,
		DevelopmentParties: party.NewDevelopmentHTTPHandler(repository),
	}).Handler()

	request := func(method, path, body string) *httptest.ResponseRecorder {
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
	type partyResponse struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		CivilName   string  `json:"civil_name"`
		SocialName  *string `json:"social_name"`
		DisplayName string  `json:"display_name"`
	}
	decode := func(rec *httptest.ResponseRecorder) partyResponse {
		t.Helper()
		var envelope struct {
			Party partyResponse `json:"party"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("resposta inválida: %v (%s)", err, rec.Body.String())
		}
		return envelope.Party
	}

	created := request(http.MethodPost, "/_dev/parties", `{"name":"Ana Civil","social_name":"Ana Social","document":"123"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("status do cadastro = %d: %s", created.Code, created.Body.String())
	}
	partyCreated := decode(created)
	if partyCreated.Name != "Ana Civil" || partyCreated.CivilName != "Ana Civil" {
		t.Fatalf("nome civil alterado: %#v", partyCreated)
	}
	if partyCreated.SocialName == nil || *partyCreated.SocialName != "Ana Social" || partyCreated.DisplayName != "Ana Social" {
		t.Fatalf("nome social não refletido: %#v", partyCreated)
	}

	assertStored := func(wantCivil string, wantSocial *string) {
		t.Helper()
		var civil string
		var social pgtype.Text
		if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT p.name, pe.social_name
				  FROM party.party p
				  JOIN party.person pe ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
				 WHERE p.id = $1`, partyCreated.ID).Scan(&civil, &social)
		}); err != nil {
			t.Fatalf("leitura no banco: %v", err)
		}
		if civil != wantCivil {
			t.Fatalf("nome civil no banco = %q, esperado %q", civil, wantCivil)
		}
		if wantSocial == nil {
			if social.Valid {
				t.Fatalf("social_name no banco = %q, esperado NULL", social.String)
			}
			return
		}
		if !social.Valid || social.String != *wantSocial {
			t.Fatalf("social_name no banco = %q, esperado %q", social.String, *wantSocial)
		}
	}
	social := "Ana Social"
	assertStored("Ana Civil", &social)

	if err := database.WithTenantTx(db.WithTenant(ctx, tenantID), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE party.person SET social_name = 'Ana Atualizado' WHERE party_id = $1`, partyCreated.ID)
		return err
	}); err != nil {
		t.Fatalf("atualizar nome social: %v", err)
	}
	social = "Ana Atualizado"
	assertStored("Ana Civil", &social)

	got := request(http.MethodGet, "/_dev/parties/"+partyCreated.ID, "")
	if got.Code != http.StatusOK {
		t.Fatalf("status da consulta = %d: %s", got.Code, got.Body.String())
	}
	partyFetched := decode(got)
	if partyFetched.Name != "Ana Civil" || partyFetched.CivilName != "Ana Civil" {
		t.Fatalf("consulta alterou o nome civil: %#v", partyFetched)
	}
	if partyFetched.SocialName == nil || *partyFetched.SocialName != "Ana Atualizado" || partyFetched.DisplayName != "Ana Atualizado" {
		t.Fatalf("consulta não refletiu o nome social: %#v", partyFetched)
	}

	withoutSocial := request(http.MethodPost, "/_dev/parties", `{"name":"Bruno Civil"}`)
	if withoutSocial.Code != http.StatusCreated {
		t.Fatalf("status do cadastro sem nome social = %d: %s", withoutSocial.Code, withoutSocial.Body.String())
	}
	plain := decode(withoutSocial)
	if plain.SocialName != nil || plain.DisplayName != "Bruno Civil" || plain.Name != "Bruno Civil" {
		t.Fatalf("cadastro sem nome social = %#v", plain)
	}
}
