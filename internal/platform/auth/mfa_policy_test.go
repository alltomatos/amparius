package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestMFAPolicyDisabledSkipsMFAEntirely cobre o caminho do emissor próprio de
// tenant (ADR 0048 §9, AUTH-01B): com MFAPolicyDisabled, NewService aceita
// mfa=nil e Authenticate nunca exige MFAEnabled nem chama MFAVerifier.Verify.
func TestMFAPolicyDisabledSkipsMFAEntirely(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	policy, err := DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &companyResolver{belongs: true}
	service, err := NewService(
		OIDCVerifierFunc(func(_ context.Context, token string) (OIDCIdentity, error) {
			return OIDCIdentity{Issuer: "amparius-saas", Subject: "user-1", TenantID: tenantID, CompanyID: companyID}, nil
		}),
		nil, // nenhum MFAVerifier: MFAPolicyDisabled não exige um
		subjectResolver{record: SubjectRecord{Roles: []string{"owner"}, CompanyIDs: []uuid.UUID{companyID}, MFAEnabled: false}},
		resolver, policy, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), MFAPolicyDisabled,
	)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/parties", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rec := httptest.NewRecorder()
	var reached bool
	Require(service, PermissionPartyRead, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if !reached || rec.Code != http.StatusNoContent {
		t.Fatalf("esperava alcançar a rota sem MFA; status=%d corpo=%s", rec.Code, rec.Body.String())
	}
}

// TestMFAPolicyRequiredIsDefaultAndStillRejectsWithoutMFA confirma que o
// comportamento histórico (usado pela plataforma) continua inalterado: sem o
// parâmetro variádico, a política é MFAPolicyRequired, e mfa=nil continua
// sendo rejeitado na construção do serviço.
func TestMFAPolicyRequiredIsDefaultAndStillRejectsWithoutMFA(t *testing.T) {
	policy, err := DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewService(
		OIDCVerifierFunc(func(context.Context, string) (OIDCIdentity, error) { return OIDCIdentity{}, nil }),
		nil,
		subjectResolver{}, &companyResolver{belongs: true}, policy, nil,
	)
	if !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("política padrão deveria exigir MFAVerifier; erro = %v", err)
	}
}

// TestMFAPolicyOptionalCollapsesToRequired documenta a decisão deliberada: um
// valor MFAPolicyOptional nunca afrouxa silenciosamente para Disabled.
func TestMFAPolicyOptionalCollapsesToRequired(t *testing.T) {
	policy, err := DefaultRolePolicy()
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewService(
		OIDCVerifierFunc(func(context.Context, string) (OIDCIdentity, error) { return OIDCIdentity{}, nil }),
		nil,
		subjectResolver{}, &companyResolver{belongs: true}, policy, nil, MFAPolicyOptional,
	)
	if !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("MFAPolicyOptional deveria se comportar como Required (mfa=nil rejeitado); erro = %v", err)
	}
}
