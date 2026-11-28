package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const tenantIssuerName = "amparius-saas"

// KeyProvider abstrai o cofre/KMS que guarda a chave de assinatura Ed25519 do
// emissor próprio de tenant. A implementação real de produção é um
// pré-requisito de infraestrutura fora desta fatia (ADR 0048 Lacuna 5); o
// único adaptador aqui (EnvKeyProvider) é de desenvolvimento/teste.
type KeyProvider interface {
	SigningKey(ctx context.Context) (kid string, key ed25519.PrivateKey, err error)
	VerificationKeys(ctx context.Context) (map[string]ed25519.PublicKey, error)
}

// EnvKeyProvider lê a chave de assinatura Ed25519 (seed de 32 bytes,
// base64 padrão) e o kid correspondente de valores injetados no processo.
// É a lacuna documentada pelo ADR 0048 (Lacuna 5): produção depende de um
// cofre/KMS real que esta fatia não implementa.
type EnvKeyProvider struct {
	kid            string
	key            ed25519.PrivateKey
	previousKid    string
	previousPublic ed25519.PublicKey
}

// NewEnvKeyProvider constrói o provedor a partir de seeds já lidos do
// ambiente. previousSeedB64/previousKid são opcionais e habilitam rotação:
// tokens assinados pela chave anterior continuam verificáveis até expirarem.
func NewEnvKeyProvider(seedB64, kid, previousSeedB64, previousKid string) (*EnvKeyProvider, error) {
	if strings.TrimSpace(seedB64) == "" || strings.TrimSpace(kid) == "" {
		return nil, fmt.Errorf("%w: seed/kid de assinatura ausentes", ErrAuthenticationUnavailable)
	}
	key, err := decodeEd25519Seed(seedB64)
	if err != nil {
		return nil, fmt.Errorf("%w: seed Ed25519 inválida", ErrAuthenticationUnavailable)
	}
	provider := &EnvKeyProvider{kid: kid, key: key}
	if strings.TrimSpace(previousSeedB64) != "" && strings.TrimSpace(previousKid) != "" {
		previousKey, err := decodeEd25519Seed(previousSeedB64)
		if err != nil {
			return nil, fmt.Errorf("%w: seed Ed25519 anterior inválida", ErrAuthenticationUnavailable)
		}
		provider.previousKid = previousKid
		provider.previousPublic = previousKey.Public().(ed25519.PublicKey)
	}
	return provider, nil
}

func decodeEd25519Seed(seedB64 string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seedB64))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, ErrAuthenticationUnavailable
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// SigningKey implementa KeyProvider.
func (p *EnvKeyProvider) SigningKey(context.Context) (string, ed25519.PrivateKey, error) {
	if p == nil || len(p.key) == 0 {
		return "", nil, ErrAuthenticationUnavailable
	}
	return p.kid, p.key, nil
}

// VerificationKeys implementa KeyProvider.
func (p *EnvKeyProvider) VerificationKeys(context.Context) (map[string]ed25519.PublicKey, error) {
	if p == nil || len(p.key) == 0 {
		return nil, ErrAuthenticationUnavailable
	}
	keys := map[string]ed25519.PublicKey{p.kid: p.key.Public().(ed25519.PublicKey)}
	if p.previousKid != "" {
		keys[p.previousKid] = p.previousPublic
	}
	return keys, nil
}

// TenantAccessClaims são os dados de negócio assinados no token de acesso do
// emissor próprio de tenant. Roles é só informativo para o cliente (contrato
// auth-login-http-v1): a autorização em cada requisição sempre revalida os
// papéis a partir do banco via ResolveSubject, nunca confia neste claim.
type TenantAccessClaims struct {
	UserID    uuid.UUID
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	Roles     []string
}

type tenantTokenPayload struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	TenantID  string   `json:"tenant_id"`
	CompanyID string   `json:"company_id"`
	Roles     []string `json:"roles"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
	JTI       string   `json:"jti"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// TenantIssuer emite e verifica tokens de acesso EdDSA (Ed25519) do emissor
// próprio de tenant, usando apenas a biblioteca padrão (sem dependência de
// terceiros para JWT, ausente do go.mod do repositório).
type TenantIssuer struct {
	keys KeyProvider
	ttl  time.Duration
}

// NewTenantIssuer constrói o emissor. ttl <= 0 usa o padrão de 10 minutos
// (ADR 0048 §6).
func NewTenantIssuer(keys KeyProvider, ttl time.Duration) (*TenantIssuer, error) {
	if keys == nil {
		return nil, ErrAuthenticationUnavailable
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &TenantIssuer{keys: keys, ttl: ttl}, nil
}

// Issue emite um token de acesso assinado e devolve seu tempo de vida.
func (iss *TenantIssuer) Issue(ctx context.Context, claims TenantAccessClaims) (string, time.Duration, error) {
	if iss == nil || iss.keys == nil {
		return "", 0, ErrAuthenticationUnavailable
	}
	kid, key, err := iss.keys.SigningKey(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("obter chave de assinatura: %w", err)
	}
	now := time.Now().UTC()
	payload := tenantTokenPayload{
		Issuer:    tenantIssuerName,
		Subject:   claims.UserID.String(),
		TenantID:  claims.TenantID.String(),
		CompanyID: claims.CompanyID.String(),
		Roles:     claims.Roles,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(iss.ttl).Unix(),
		JTI:       uuid.NewString(),
	}
	token, err := encodeEdDSA(kid, payload, key)
	if err != nil {
		return "", 0, fmt.Errorf("assinar token de acesso: %w", err)
	}
	return token, iss.ttl, nil
}

// Verify implementa auth.OIDCVerifier para o emissor próprio de tenant.
// CompanyID vem do token (companyId escolhido no login/refresh), não de um
// header controlado pelo cliente.
func (iss *TenantIssuer) Verify(ctx context.Context, token string) (OIDCIdentity, error) {
	if iss == nil || iss.keys == nil {
		return OIDCIdentity{}, ErrAuthenticationUnavailable
	}
	keys, err := iss.keys.VerificationKeys(ctx)
	if err != nil {
		return OIDCIdentity{}, fmt.Errorf("obter chaves de verificação: %w", err)
	}
	var payload tenantTokenPayload
	if err := decodeEdDSA(token, keys, &payload); err != nil {
		return OIDCIdentity{}, err
	}
	if payload.Issuer != tenantIssuerName {
		return OIDCIdentity{}, fmt.Errorf("%w: issuer inesperado", ErrInvalidOIDCToken)
	}
	if time.Now().UTC().Unix() > payload.ExpiresAt {
		return OIDCIdentity{}, fmt.Errorf("%w: token expirado", ErrInvalidOIDCToken)
	}
	tenantID, err := uuid.Parse(payload.TenantID)
	if err != nil {
		return OIDCIdentity{}, fmt.Errorf("%w: tenant_id malformado", ErrInvalidOIDCToken)
	}
	companyID, err := uuid.Parse(payload.CompanyID)
	if err != nil {
		return OIDCIdentity{}, fmt.Errorf("%w: company_id malformado", ErrInvalidOIDCToken)
	}
	return OIDCIdentity{
		Issuer:    payload.Issuer,
		Subject:   payload.Subject,
		TenantID:  tenantID,
		CompanyID: companyID,
	}, nil
}

func encodeEdDSA(kid string, payload any, key ed25519.PrivateKey) (string, error) {
	headerJSON, err := json.Marshal(jwtHeader{Alg: "EdDSA", Typ: "JWT", Kid: kid})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	signature := ed25519.Sign(key, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func decodeEdDSA(token string, keys map[string]ed25519.PublicKey, payload any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: token malformado", ErrInvalidOIDCToken)
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("%w: header malformado", ErrInvalidOIDCToken)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return fmt.Errorf("%w: header malformado", ErrInvalidOIDCToken)
	}
	if header.Alg != "EdDSA" {
		return fmt.Errorf("%w: algoritmo inesperado", ErrInvalidOIDCToken)
	}
	key, ok := keys[header.Kid]
	if !ok {
		return fmt.Errorf("%w: kid desconhecido", ErrInvalidOIDCToken)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("%w: assinatura malformada", ErrInvalidOIDCToken)
	}
	signingInput := parts[0] + "." + parts[1]
	if !ed25519.Verify(key, []byte(signingInput), signature) {
		return fmt.Errorf("%w: assinatura inválida", ErrInvalidOIDCToken)
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("%w: payload malformado", ErrInvalidOIDCToken)
	}
	if err := json.Unmarshal(payloadJSON, payload); err != nil {
		return fmt.Errorf("%w: payload malformado", ErrInvalidOIDCToken)
	}
	return nil
}
