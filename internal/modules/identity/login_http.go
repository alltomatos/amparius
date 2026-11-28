package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

const maxAuthBodyBytes = 4 << 10

type loginHandler struct{ service *LoginService }

// PublicLoginRoutes monta as rotas de /v1/auth que não exigem autenticação
// (o corpo é quem carrega a credencial). O handler valida tudo: nenhuma rota
// aqui aceita tenant/escopo vindo de header.
func PublicLoginRoutes(service *LoginService) []httpserver.PublicRoute {
	h := loginHandler{service: service}
	return []httpserver.PublicRoute{
		{Pattern: "POST /v1/auth/login", Handler: h.available(h.login)},
		{Pattern: "POST /v1/auth/refresh", Handler: h.available(h.refresh)},
		{Pattern: "POST /v1/auth/bootstrap/complete", Handler: h.available(h.bootstrapComplete)},
		{Pattern: "POST /v1/auth/recovery/request", Handler: http.HandlerFunc(h.recoveryRequest)},
		{Pattern: "POST /v1/auth/recovery/complete", Handler: h.available(h.recoveryComplete)},
	}
}

// SelfServiceLoginRoutes monta logout/revoke, protegidas só por autenticação
// (sem Permission de negócio — ver auth.RequireAuthenticated).
func SelfServiceLoginRoutes(service *LoginService) []httpserver.AuthenticatedRoute {
	h := loginHandler{service: service}
	return []httpserver.AuthenticatedRoute{
		{Pattern: "POST /v1/auth/logout", Handler: h.available(h.logout)},
		{Pattern: "POST /v1/auth/revoke", Handler: h.available(h.revoke)},
	}
}

// InviteRoutes exige identity:write, igual a qualquer outra rota de escrita
// de identidade — convite não é autosserviço de sessão.
func InviteRoutes(service *LoginService) []httpserver.AuthenticatedRoute {
	h := loginHandler{service: service}
	return []httpserver.AuthenticatedRoute{
		{Pattern: "POST /v1/auth/invite", Permission: platformauth.PermissionIdentityWrite, Handler: h.available(h.invite)},
	}
}

func (h loginHandler) available(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if h.service == nil {
			writeAuthProductionError(w, r, http.StatusServiceUnavailable, "AUTHENTICATION_UNAVAILABLE", "autenticação indisponível")
			return
		}
		next(w, r)
	})
}

type loginPayload struct {
	Email      string  `json:"email"`
	Password   string  `json:"password"`
	CompanyID  string  `json:"companyId"`
	TenantHint *string `json:"tenantHint"`
}

func (h loginHandler) login(w http.ResponseWriter, r *http.Request) {
	payload, err := decodeAuthBody[loginPayload](w, r)
	if err != nil {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo inválido")
		return
	}
	companyID, err := uuid.Parse(strings.TrimSpace(payload.CompanyID))
	if err != nil || companyID == uuid.Nil || strings.TrimSpace(payload.Email) == "" || payload.Password == "" {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo inválido")
		return
	}
	result, err := h.service.Login(r.Context(), payload.Email, payload.Password, companyID, clientIPFrom(r))
	if err != nil {
		writeAuthServiceError(w, r, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, sessionResponse(result))
}

type refreshPayload struct {
	RefreshToken string  `json:"refreshToken"`
	CompanyID    *string `json:"companyId"`
}

func (h loginHandler) refresh(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len([]rune(key)) > 200 {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Idempotency-Key inválida")
		return
	}
	payload, err := decodeAuthBody[refreshPayload](w, r)
	if err != nil || strings.TrimSpace(payload.RefreshToken) == "" {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo inválido")
		return
	}
	var companyID *uuid.UUID
	if payload.CompanyID != nil && strings.TrimSpace(*payload.CompanyID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(*payload.CompanyID))
		if err != nil || parsed == uuid.Nil {
			writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "companyId inválido")
			return
		}
		companyID = &parsed
	}
	result, err := h.service.Refresh(r.Context(), payload.RefreshToken, companyID, key)
	if err != nil {
		writeAuthServiceError(w, r, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, sessionResponse(result))
}

type logoutPayload struct {
	RefreshToken string `json:"refreshToken"`
}

func (h loginHandler) logout(w http.ResponseWriter, r *http.Request) {
	principal, err := platformauth.PrincipalFromContext(r.Context())
	if err != nil {
		writeAuthServiceError(w, r, err)
		return
	}
	payload, err := decodeAuthBody[logoutPayload](w, r)
	if err != nil || strings.TrimSpace(payload.RefreshToken) == "" {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo inválido")
		return
	}
	if err := h.service.Logout(r.Context(), principal.UserID, payload.RefreshToken); err != nil {
		writeAuthServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type revokePayload struct {
	Password string `json:"password"`
}

func (h loginHandler) revoke(w http.ResponseWriter, r *http.Request) {
	principal, err := platformauth.PrincipalFromContext(r.Context())
	if err != nil {
		writeAuthServiceError(w, r, err)
		return
	}
	payload, err := decodeAuthBody[revokePayload](w, r)
	if err != nil || payload.Password == "" {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo inválido")
		return
	}
	if err := h.service.Revoke(r.Context(), principal.UserID, payload.Password); err != nil {
		writeAuthServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type completePayload struct {
	Token     string `json:"token"`
	Password  string `json:"password"`
	CompanyID string `json:"companyId"`
}

func (h loginHandler) bootstrapComplete(w http.ResponseWriter, r *http.Request) {
	h.complete(w, r, h.service.CompleteBootstrap)
}

func (h loginHandler) recoveryComplete(w http.ResponseWriter, r *http.Request) {
	h.complete(w, r, h.service.CompleteRecovery)
}

func (h loginHandler) complete(w http.ResponseWriter, r *http.Request, do func(ctx context.Context, token, password string, companyID uuid.UUID) (SessionResult, error)) {
	payload, err := decodeAuthBody[completePayload](w, r)
	if err != nil {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo inválido")
		return
	}
	companyID, err := uuid.Parse(strings.TrimSpace(payload.CompanyID))
	if err != nil || companyID == uuid.Nil || strings.TrimSpace(payload.Token) == "" || payload.Password == "" {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo inválido")
		return
	}
	result, err := do(r.Context(), payload.Token, payload.Password, companyID)
	if err != nil {
		writeAuthServiceError(w, r, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, sessionResponse(result))
}

// recoveryRequest responde sempre 503 nesta fatia: não há canal de entrega
// de e-mail configurado (contrato auth-login-http-v1, nunca 202 fingindo que
// algo foi enviado).
func (h loginHandler) recoveryRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeAuthProductionError(w, r, http.StatusServiceUnavailable, "RECOVERY_UNAVAILABLE", "recuperação de senha indisponível nesta fatia")
}

type invitePayload struct {
	Email      string   `json:"email"`
	Name       string   `json:"name"`
	RoleCodes  []string `json:"roleCodes"`
	CompanyIDs []string `json:"companyIds"`
}

func (h loginHandler) invite(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len([]rune(key)) > 200 {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Idempotency-Key inválida")
		return
	}
	payload, err := decodeAuthBody[invitePayload](w, r)
	if err != nil || strings.TrimSpace(payload.Email) == "" || len(payload.RoleCodes) == 0 || len(payload.CompanyIDs) == 0 {
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo inválido")
		return
	}
	companyIDs := make([]uuid.UUID, 0, len(payload.CompanyIDs))
	for _, raw := range payload.CompanyIDs {
		parsed, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || parsed == uuid.Nil {
			writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "companyIds inválido")
			return
		}
		companyIDs = append(companyIDs, parsed)
	}
	_, bootstrapToken, err := h.service.Invite(r.Context(), payload.Email, payload.Name, payload.RoleCodes, companyIDs)
	if err != nil {
		writeAuthServiceError(w, r, err)
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{"bootstrapToken": bootstrapToken})
}

func sessionResponse(result SessionResult) map[string]any {
	return map[string]any{
		"accessToken":           result.AccessToken,
		"accessTokenExpiresIn":  result.AccessTokenExpiresIn,
		"refreshToken":          result.RefreshToken,
		"refreshTokenExpiresIn": result.RefreshTokenExpiresIn,
		"tokenType":             result.TokenType,
		"user": map[string]any{
			"id":        result.UserID.String(),
			"roles":     result.Roles,
			"companyId": result.CompanyID.String(),
		},
	}
}

func writeAuthServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeAuthProductionError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "credenciais inválidas")
	case errors.Is(err, ErrAccountLocked):
		writeAuthProductionError(w, r, http.StatusForbidden, "ACCOUNT_LOCKED", "conta bloqueada temporariamente")
	case errors.Is(err, ErrAccountPendingAuth):
		writeAuthProductionError(w, r, http.StatusForbidden, "ACCOUNT_PENDING_AUTH", "conta pendente de ativação")
	case errors.Is(err, ErrCompanyNotAllowed):
		writeAuthProductionError(w, r, http.StatusForbidden, "COMPANY_NOT_ALLOWED", "empresa não vinculada ao usuário")
	case errors.Is(err, ErrRefreshInvalid):
		writeAuthProductionError(w, r, http.StatusUnauthorized, "REFRESH_INVALID", "refresh token inválido")
	case errors.Is(err, ErrRefreshReuseDetected):
		writeAuthProductionError(w, r, http.StatusUnauthorized, "REFRESH_REUSE_DETECTED", "reuso de refresh token detectado")
	case errors.Is(err, ErrCredentialTokenExpired):
		writeAuthProductionError(w, r, http.StatusGone, "TOKEN_EXPIRED", "token expirado")
	case errors.Is(err, ErrCredentialTokenConsumed):
		writeAuthProductionError(w, r, http.StatusConflict, "TOKEN_ALREADY_USED", "token já utilizado")
	case errors.Is(err, ErrCredentialTokenNotFound), errors.Is(err, ErrInviteRoleUnknown):
		writeAuthProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "dados inválidos")
	default:
		writeAuthProductionError(w, r, http.StatusServiceUnavailable, "AUTHENTICATION_UNAVAILABLE", "autenticação indisponível")
	}
}

func writeAuthProductionError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeAuthJSON(w, status, map[string]any{
		"code":    code,
		"message": message,
		"details": map[string]any{},
		"traceId": observability.TraceIDFromContext(r.Context()),
	})
}

func writeAuthJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeAuthBody[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var result T
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return result, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return result, errors.New("corpo deve ser objeto JSON")
	}
	strict := json.NewDecoder(bytes.NewReader(trimmed))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func clientIPFrom(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if first, _, ok := strings.Cut(forwarded, ","); ok {
			return strings.TrimSpace(first)
		}
		return forwarded
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
