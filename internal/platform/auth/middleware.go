package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

// Gate é a parte mínima consumida pelo middleware HTTP e facilita testes com
// adaptadores reais ou doubles controlados.
type Gate interface {
	Authenticate(context.Context, string) (Principal, error)
	Allows(Principal, Permission) bool
}

// Require protege uma rota com autenticação, MFA, escopo tenant/empresa e uma
// permissão RBAC. O middleware nunca aceita escopo vindo de header ou corpo.
func Require(gate Gate, permission Permission, logger *slog.Logger) func(http.Handler) http.Handler {
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
					w.Header().Set("WWW-Authenticate", `Bearer realm="amparius"`)
				}
				writeAuthError(w, r, status, code, message)
				return
			}
			if !gate.Allows(principal, permission) {
				logDecision(logger, r, permission, "PERMISSION_DENIED")
				writeAuthError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "permissão insuficiente")
				return
			}
			if principal.Subject == "" || principal.TenantID == uuid.Nil || principal.CompanyID == uuid.Nil || !principal.MFAAuthenticated {
				logDecision(logger, r, permission, "SCOPE_FORBIDDEN")
				writeAuthError(w, r, http.StatusForbidden, "SCOPE_FORBIDDEN", "escopo não autorizado")
				return
			}

			ctx := WithPrincipal(r.Context(), principal)
			ctx = db.WithTenant(ctx, principal.TenantID)
			ctx = db.WithCompany(ctx, principal.CompanyID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authenticationError(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrAuthenticationUnavailable):
		return http.StatusServiceUnavailable, "AUTHENTICATION_UNAVAILABLE", "autenticação indisponível"
	case errors.Is(err, ErrMFARequired):
		return http.StatusForbidden, "MFA_REQUIRED", "MFA obrigatório"
	case errors.Is(err, ErrScopeForbidden), errors.Is(err, ErrContextConflict):
		return http.StatusForbidden, "SCOPE_FORBIDDEN", "escopo não autorizado"
	case errors.Is(err, ErrInvalidBearer), errors.Is(err, ErrInvalidOIDCToken), errors.Is(err, ErrInvalidIdentity):
		return http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "credencial inválida"
	default:
		return http.StatusServiceUnavailable, "AUTHENTICATION_UNAVAILABLE", "autenticação indisponível"
	}
}

func logDecision(logger *slog.Logger, r *http.Request, permission Permission, result string) {
	attrs := []slog.Attr{
		slog.String("event", "auth_decision"),
		slog.String("method", r.Method),
		slog.String("permission", string(permission)),
		slog.String("result", result),
	}
	if requestID := r.Header.Get("X-Request-Id"); requestID != "" {
		attrs = append(attrs, slog.String("request_id", requestID))
	}
	if traceID := observability.TraceIDFromContext(r.Context()); traceID != "" {
		attrs = append(attrs, slog.String("trace_id", traceID))
	}
	logger.LogAttrs(r.Context(), slog.LevelInfo, "auth_decision", attrs...)
}

func writeAuthError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": message,
		"details": map[string]any{},
		"traceId": observability.TraceIDFromContext(r.Context()),
	})
}
