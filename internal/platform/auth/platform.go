package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Escopo de plataforma (ADR 0040). Os papéis e permissões de plataforma são
// distintos dos papéis de tenant: nenhum papel de tenant recebe permissão
// `platform:*` e `platform_admin` não recebe nenhuma permissão de `/v1`.
const (
	RolePlatformAdmin = "platform_admin"

	PermissionPlatformRead  Permission = "platform:read"
	PermissionPlatformWrite Permission = "platform:write"
)

// PlatformIdentity é o resultado já validado pelo adaptador OIDC do domínio
// manager.amparius.com. O adaptador valida assinatura, issuer, audience
// (exclusivamente manager), expiração e nonce; este pacote só valida os campos
// de escopo e exige MFA. Os papéis vêm de fonte confiável, nunca do cliente.
type PlatformIdentity struct {
	Issuer  string
	Subject string
	UserID  uuid.UUID
	Roles   []string
	MFA     bool
}

// PlatformVerifier é o contrato do provedor OIDC de plataforma.
type PlatformVerifier interface {
	Verify(context.Context, string) (PlatformIdentity, error)
}

// PlatformVerifierFunc facilita adaptadores pequenos e testes.
type PlatformVerifierFunc func(context.Context, string) (PlatformIdentity, error)

func (f PlatformVerifierFunc) Verify(ctx context.Context, token string) (PlatformIdentity, error) {
	if f == nil {
		return PlatformIdentity{}, ErrAuthenticationUnavailable
	}
	return f(ctx, token)
}

// PlatformPrincipal é a identidade de plataforma da requisição. Não carrega
// tenant nem empresa: o tenant alvo é sempre um parâmetro explícito e auditado.
type PlatformPrincipal struct {
	Issuer  string
	Subject string
	UserID  uuid.UUID
	Roles   []string
}

type platformPrincipalKey struct{}

// WithPlatformPrincipal associa o principal de plataforma ao contexto.
func WithPlatformPrincipal(ctx context.Context, principal PlatformPrincipal) context.Context {
	return context.WithValue(ctx, platformPrincipalKey{}, principal)
}

// PlatformPrincipalFromContext devolve o principal de plataforma ou falha fechado.
func PlatformPrincipalFromContext(ctx context.Context) (PlatformPrincipal, error) {
	principal, ok := ctx.Value(platformPrincipalKey{}).(PlatformPrincipal)
	if !ok || principal.Subject == "" || principal.UserID == uuid.Nil {
		return PlatformPrincipal{}, ErrAuthenticationUnavailable
	}
	return principal, nil
}

// PlatformGate é a parte mínima consumida pelo middleware de plataforma.
type PlatformGate interface {
	Authenticate(context.Context, string) (PlatformPrincipal, error)
	Allows(PlatformPrincipal, Permission) bool
}

// PlatformService autentica o bearer de plataforma: OIDC + MFA + papel
// `platform_admin`. Todas as dependências são obrigatórias.
type PlatformService struct {
	verifier PlatformVerifier
	policy   RolePolicy
}

// DefaultPlatformRolePolicy concede somente `platform_admin` e somente as
// permissões de plataforma.
func DefaultPlatformRolePolicy() (RolePolicy, error) {
	return NewRolePolicy(map[string][]Permission{
		RolePlatformAdmin: {PermissionPlatformRead, PermissionPlatformWrite},
	})
}

// NewPlatformService constrói a autenticação de plataforma.
func NewPlatformService(verifier PlatformVerifier, policy RolePolicy) (*PlatformService, error) {
	if verifier == nil || len(policy.roles) == 0 {
		return nil, ErrAuthenticationUnavailable
	}
	return &PlatformService{verifier: verifier, policy: policy}, nil
}

// Authenticate valida o valor completo do cabeçalho Authorization.
func (s *PlatformService) Authenticate(ctx context.Context, authorization string) (PlatformPrincipal, error) {
	if s == nil || s.verifier == nil || len(s.policy.roles) == 0 {
		return PlatformPrincipal{}, ErrAuthenticationUnavailable
	}
	token, err := bearerToken(authorization)
	if err != nil {
		return PlatformPrincipal{}, err
	}
	identity, err := s.verifier.Verify(ctx, token)
	if err != nil {
		return PlatformPrincipal{}, fmt.Errorf("verificar identidade de plataforma: %w", err)
	}
	if strings.TrimSpace(identity.Issuer) == "" || strings.TrimSpace(identity.Subject) == "" || identity.UserID == uuid.Nil {
		return PlatformPrincipal{}, ErrInvalidIdentity
	}
	if !identity.MFA {
		return PlatformPrincipal{}, ErrMFARequired
	}
	return PlatformPrincipal{
		Issuer: identity.Issuer, Subject: identity.Subject, UserID: identity.UserID,
		Roles: append([]string(nil), identity.Roles...),
	}, nil
}

// Allows aplica o RBAC de plataforma.
func (s *PlatformService) Allows(principal PlatformPrincipal, permission Permission) bool {
	if s == nil {
		return false
	}
	return s.policy.Allows(principal.Roles, permission)
}

// RequirePlatform protege uma rota `/admin/v1` com autenticação, MFA e uma
// permissão de plataforma. Sem gate configurado responde 503; sem
// `platform_admin` válido responde 401/403 (falha fechada, ADR 0040 decisão 6).
func RequirePlatform(gate PlatformGate, permission Permission, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if permission == "" {
				logDecision(logger, r, permission, "PERMISSION_DENIED")
				writeAuthError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "permissão insuficiente")
				return
			}
			if gate == nil {
				logDecision(logger, r, permission, "unavailable")
				writeAuthError(w, r, http.StatusServiceUnavailable, "AUTHENTICATION_UNAVAILABLE", "autenticação indisponível")
				return
			}
			principal, err := gate.Authenticate(r.Context(), r.Header.Get("Authorization"))
			if err != nil {
				status, code, message := authenticationError(err)
				logDecision(logger, r, permission, code)
				if status == http.StatusUnauthorized {
					w.Header().Set("WWW-Authenticate", `Bearer realm="amparius-platform"`)
				}
				writeAuthError(w, r, status, code, message)
				return
			}
			if !gate.Allows(principal, permission) {
				logDecision(logger, r, permission, "PERMISSION_DENIED")
				writeAuthError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "permissão insuficiente")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPlatformPrincipal(r.Context(), principal)))
		})
	}
}
