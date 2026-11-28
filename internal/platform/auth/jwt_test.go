package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func seedB64(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(priv.Seed())
}

func TestTenantIssuerIssueAndVerifyRoundTrip(t *testing.T) {
	keys, err := NewEnvKeyProvider(seedB64(t), "kid-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewTenantIssuer(keys, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims := TenantAccessClaims{UserID: uuid.New(), TenantID: uuid.New(), CompanyID: uuid.New(), Roles: []string{"owner"}}
	token, ttl, err := issuer.Issue(context.Background(), claims)
	if err != nil {
		t.Fatal(err)
	}
	if ttl != time.Minute {
		t.Fatalf("ttl = %v, quer %v", ttl, time.Minute)
	}
	identity, err := issuer.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.TenantID != claims.TenantID || identity.CompanyID != claims.CompanyID || identity.Subject != claims.UserID.String() {
		t.Fatalf("identidade verificada não corresponde aos claims emitidos: %+v", identity)
	}
	if identity.Issuer != tenantIssuerName {
		t.Fatalf("issuer = %q, quer %q", identity.Issuer, tenantIssuerName)
	}
}

func TestTenantIssuerVerifyExpiredToken(t *testing.T) {
	keys, err := NewEnvKeyProvider(seedB64(t), "kid-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewTenantIssuer(keys, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := keys.SigningKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload := tenantTokenPayload{
		Issuer: tenantIssuerName, Subject: uuid.NewString(), TenantID: uuid.NewString(), CompanyID: uuid.NewString(),
		IssuedAt: time.Now().Add(-time.Hour).Unix(), ExpiresAt: time.Now().Add(-time.Minute).Unix(), JTI: uuid.NewString(),
	}
	token, err := encodeEdDSA("kid-1", payload, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(context.Background(), token); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("token já expirado deveria falhar com ErrInvalidOIDCToken, obtido %v", err)
	}
}

func TestTenantIssuerVerifyTamperedSignatureRejected(t *testing.T) {
	keys, err := NewEnvKeyProvider(seedB64(t), "kid-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewTenantIssuer(keys, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := issuer.Issue(context.Background(), TenantAccessClaims{UserID: uuid.New(), TenantID: uuid.New(), CompanyID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token malformado: %q", token)
	}
	tamperedSignature := parts[2]
	if tamperedSignature[0] == 'A' {
		tamperedSignature = "B" + tamperedSignature[1:]
	} else {
		tamperedSignature = "A" + tamperedSignature[1:]
	}
	tampered := parts[0] + "." + parts[1] + "." + tamperedSignature
	if _, err := issuer.Verify(context.Background(), tampered); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("assinatura adulterada deveria falhar com ErrInvalidOIDCToken, obtido %v", err)
	}
}

func TestTenantIssuerVerifyUnknownKidRejected(t *testing.T) {
	keysA, err := NewEnvKeyProvider(seedB64(t), "kid-a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	issuerA, err := NewTenantIssuer(keysA, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := issuerA.Issue(context.Background(), TenantAccessClaims{UserID: uuid.New(), TenantID: uuid.New(), CompanyID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}

	keysB, err := NewEnvKeyProvider(seedB64(t), "kid-b", "", "")
	if err != nil {
		t.Fatal(err)
	}
	issuerB, err := NewTenantIssuer(keysB, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuerB.Verify(context.Background(), token); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("token assinado por outro kid/chave deveria falhar com ErrInvalidOIDCToken, obtido %v", err)
	}
}

func TestTenantIssuerVerifyWrongIssuerRejected(t *testing.T) {
	keys, err := NewEnvKeyProvider(seedB64(t), "kid-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewTenantIssuer(keys, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	key, err := decodeEd25519Seed(mustDecodeSeedSource(t, keys))
	if err != nil {
		t.Fatal(err)
	}
	payload := tenantTokenPayload{
		Issuer: "outro-emissor", Subject: uuid.NewString(), TenantID: uuid.NewString(), CompanyID: uuid.NewString(),
		IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), JTI: uuid.NewString(),
	}
	token, err := encodeEdDSA("kid-1", payload, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(context.Background(), token); !errors.Is(err, ErrInvalidOIDCToken) {
		t.Fatalf("issuer inesperado deveria falhar com ErrInvalidOIDCToken, obtido %v", err)
	}
}

// mustDecodeSeedSource recupera a chave privada do EnvKeyProvider de teste
// através de SigningKey, para montar manualmente um payload com issuer
// adulterado (o único jeito de produzir esse caso sem expor API nova).
func mustDecodeSeedSource(t *testing.T, keys *EnvKeyProvider) string {
	t.Helper()
	_, key, err := keys.SigningKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key.Seed())
}

func TestTenantIssuerVerifyAcceptsPreviousKeyDuringRotation(t *testing.T) {
	previousSeed := seedB64(t)
	previousKeys, err := NewEnvKeyProvider(previousSeed, "kid-old", "", "")
	if err != nil {
		t.Fatal(err)
	}
	previousIssuer, err := NewTenantIssuer(previousKeys, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := previousIssuer.Issue(context.Background(), TenantAccessClaims{UserID: uuid.New(), TenantID: uuid.New(), CompanyID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}

	currentKeys, err := NewEnvKeyProvider(seedB64(t), "kid-new", previousSeed, "kid-old")
	if err != nil {
		t.Fatal(err)
	}
	currentIssuer, err := NewTenantIssuer(currentKeys, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := currentIssuer.Verify(context.Background(), token); err != nil {
		t.Fatalf("token assinado pela chave anterior deveria continuar verificável durante a rotação: %v", err)
	}
}

func TestNewEnvKeyProviderRequiresSeedAndKid(t *testing.T) {
	if _, err := NewEnvKeyProvider("", "kid-1", "", ""); !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("seed vazia deveria falhar com ErrAuthenticationUnavailable, obtido %v", err)
	}
	if _, err := NewEnvKeyProvider(seedB64(t), "", "", ""); !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("kid vazio deveria falhar com ErrAuthenticationUnavailable, obtido %v", err)
	}
	if _, err := NewEnvKeyProvider("seed-invalida-nao-base64!!", "kid-1", "", ""); !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("seed inválida deveria falhar com ErrAuthenticationUnavailable, obtido %v", err)
	}
}

func TestNewTenantIssuerRequiresKeyProvider(t *testing.T) {
	if _, err := NewTenantIssuer(nil, time.Minute); !errors.Is(err, ErrAuthenticationUnavailable) {
		t.Fatalf("KeyProvider nulo deveria falhar com ErrAuthenticationUnavailable, obtido %v", err)
	}
}
