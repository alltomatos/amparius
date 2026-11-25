// Package controlplane compõe os contratos de leitura usados pelo frontend
// administrativo. Ele não acessa tabelas de outro módulo diretamente: usa os
// serviços públicos de identity e entitlement.
package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	"github.com/alltomatos/amparius/internal/modules/identity"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

type IdentityReader interface {
	GetTenant(rctx context.Context) (identity.Tenant, error)
	GetUserCompany(rctx context.Context, userID, companyID uuid.UUID) (identity.Company, error)
	ListUserCompanies(rctx context.Context, userID uuid.UUID, cursor string, limit int) (identity.CompanyPage, error)
	UserCanAccessCompany(rctx context.Context, userID, companyID uuid.UUID) (bool, error)
}

type EntitlementReader interface {
	ListForCompany(rctx context.Context, companyID uuid.UUID, at time.Time, cursor string, limit int) (entitlement.EntitlementPage, error)
}

type Handler struct {
	identity    IdentityReader
	entitlement EntitlementReader
}

func NewHTTPHandler(identityReader IdentityReader, entitlementReader EntitlementReader) *Handler {
	return &Handler{identity: identityReader, entitlement: entitlementReader}
}

func (h *Handler) Context(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido", nil)
		return
	}
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	if h.identity == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "CONTEXT_UNAVAILABLE", "contexto indisponível", nil)
		return
	}
	tenant, err := h.identity.GetTenant(r.Context())
	if err != nil {
		h.writeIdentityError(w, r, err)
		return
	}
	company, err := h.identity.GetUserCompany(r.Context(), principal.UserID, principal.CompanyID)
	if err != nil {
		h.writeIdentityError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":  tenantResponse(tenant),
		"company": companyResponse(company),
	})
}

func (h *Handler) Companies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido", nil)
		return
	}
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	if h.identity == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "CONTEXT_UNAVAILABLE", "contexto indisponível", nil)
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "parâmetros de paginação inválidos", map[string]string{"field": "limit"})
		return
	}
	page, err := h.identity.ListUserCompanies(r.Context(), principal.UserID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCursor) {
			h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "cursor inválido", map[string]string{"field": "cursor"})
			return
		}
		h.writeIdentityError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, company := range page.Items {
		items = append(items, companyResponse(company))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nullIfEmpty(page.NextCursor)})
}

func (h *Handler) Entitlements(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido", nil)
		return
	}
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	if h.identity == nil || h.entitlement == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível", nil)
		return
	}
	companyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("companyId")))
	if err != nil || companyID == uuid.Nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "companyId inválido", map[string]string{"field": "companyId"})
		return
	}
	allowed, err := h.identity.UserCanAccessCompany(r.Context(), principal.UserID, companyID)
	if err != nil {
		h.writeIdentityError(w, r, err)
		return
	}
	if !allowed {
		h.writeError(w, r, http.StatusForbidden, "TENANT_MISMATCH", "empresa fora do escopo autenticado", nil)
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "parâmetros de paginação inválidos", map[string]string{"field": "limit"})
		return
	}
	page, err := h.entitlement.ListForCompany(r.Context(), companyID, time.Now().UTC(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		if errors.Is(err, entitlement.ErrInvalidCursor) {
			h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "cursor inválido", map[string]string{"field": "cursor"})
			return
		}
		h.writeError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível", nil)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, entitlementResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nullIfEmpty(page.NextCursor)})
}

func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (platformauth.Principal, bool) {
	principal, err := platformauth.PrincipalFromContext(r.Context())
	if err != nil {
		h.writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "autenticação necessária", nil)
		return platformauth.Principal{}, false
	}
	tenantID, err := db.TenantFromContext(r.Context())
	if err != nil || tenantID != principal.TenantID {
		h.writeError(w, r, http.StatusForbidden, "TENANT_MISMATCH", "tenant ausente no contexto autenticado", nil)
		return platformauth.Principal{}, false
	}
	companyID, err := db.CompanyFromContext(r.Context())
	if err != nil || companyID != principal.CompanyID {
		h.writeError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "empresa ausente no contexto autenticado", nil)
		return platformauth.Principal{}, false
	}
	return principal, true
}

func (h *Handler) writeIdentityError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, identity.ErrInvalidCursor) {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "cursor inválido", nil)
		return
	}
	if errors.Is(err, identity.ErrContextNotFound) {
		h.writeError(w, r, http.StatusForbidden, "TENANT_MISMATCH", "contexto fora do escopo autenticado", nil)
		return
	}
	h.writeError(w, r, http.StatusServiceUnavailable, "CONTEXT_UNAVAILABLE", "contexto indisponível", nil)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	writeJSON(w, status, map[string]any{
		"code":    code,
		"message": message,
		"details": detailsOrEmpty(details),
		"traceId": observability.TraceIDFromContext(r.Context()),
	})
}

func parseLimit(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return 50, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("limit fora do intervalo")
	}
	return limit, nil
}

func tenantResponse(tenant identity.Tenant) map[string]any {
	return map[string]any{"id": tenant.ID.String(), "name": tenant.Name, "status": tenant.Status}
}

func companyResponse(company identity.Company) map[string]any {
	return map[string]any{
		"id": company.ID.String(), "tenantId": company.TenantID.String(), "legalName": company.Name,
		"cnpj": identity.MaskDocument(company.Document), "kind": company.Kind, "status": company.Status,
		"createdAt": company.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func entitlementResponse(item entitlement.Entitlement) map[string]any {
	var id any
	if item.ID != nil && *item.ID != uuid.Nil {
		id = item.ID.String()
	}
	return map[string]any{
		"id": id, "tenantId": item.TenantID.String(), "companyId": item.CompanyID.String(),
		"module": item.ModuleCode, "moduleName": item.ModuleName, "kind": item.Kind, "status": item.Status,
		"quota": item.Quota, "quotaConfigured": item.QuotaConfigured, "used": item.Used,
		"validFrom": dateResponse(item.ValidFrom), "validTo": dateResponse(item.ValidTo),
		"updatedAt": timeResponse(item.UpdatedAt),
	}
}

func dateResponse(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format("2006-01-02")
}

func timeResponse(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func detailsOrEmpty(value any) any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
