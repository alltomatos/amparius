package identity

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPFromIgnoresForwardedHeaderWithoutTrustedProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	r.RemoteAddr = "203.0.113.9:54321"
	r.Header.Set("X-Forwarded-For", "198.51.100.1, 10.0.0.1")

	// Sem TrustedProxies configurado (padrão de produção), o cabeçalho é
	// ignorado mesmo vindo de um peer arbitrário: um cliente malicioso não
	// pode forjar o IP usado no rate-limit de login só enviando o cabeçalho.
	if got := clientIPFrom(r, nil); got != "203.0.113.9" {
		t.Fatalf("clientIPFrom sem proxy confiável = %q, esperado RemoteAddr 203.0.113.9", got)
	}
}

func TestClientIPFromIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	trusted, err := NewTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	r.RemoteAddr = "203.0.113.9:54321"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")

	if got := clientIPFrom(r, trusted); got != "203.0.113.9" {
		t.Fatalf("clientIPFrom com peer fora da allowlist = %q, esperado RemoteAddr 203.0.113.9", got)
	}
}

func TestClientIPFromTrustsForwardedHeaderFromAllowlistedProxy(t *testing.T) {
	trusted, err := NewTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	r.RemoteAddr = "10.0.0.5:54321"
	r.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.9")

	if got := clientIPFrom(r, trusted); got != "198.51.100.1" {
		t.Fatalf("clientIPFrom com peer na allowlist = %q, esperado o primeiro salto 198.51.100.1", got)
	}
}

func TestClientIPFromFallsBackToRemoteAddrWithoutHeader(t *testing.T) {
	trusted, err := NewTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	r.RemoteAddr = "10.0.0.5:54321"

	if got := clientIPFrom(r, trusted); got != "10.0.0.5" {
		t.Fatalf("clientIPFrom sem cabeçalho = %q, esperado RemoteAddr 10.0.0.5", got)
	}
}

func TestNewTrustedProxiesRejectsInvalidCIDR(t *testing.T) {
	if _, err := NewTrustedProxies([]string{"nao-e-um-cidr"}); err == nil {
		t.Fatal("CIDR inválido deveria devolver erro")
	}
}
