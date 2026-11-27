// Package controlplane compõe os contratos de leitura usados pelo frontend
// administrativo. Ele não acessa tabelas de outro módulo diretamente: usa os
// serviços públicos de identity e entitlement.
package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

type entitlementDetailReader interface {
	GetForCompany(context.Context, uuid.UUID, string, time.Time) (entitlement.Entitlement, error)
}

type entitlementAuditReader interface {
	ListEventsPage(context.Context, entitlement.EventFilter, string) (entitlement.EventPage, error)
	AssignmentByID(context.Context, uuid.UUID) (entitlement.Assignment, error)
}

type entitlementAdminWriter interface {
	ActivateIdempotent(context.Context, uuid.UUID, string, entitlement.ActivateInput) (entitlement.Assignment, error)
	RevokeIdempotent(context.Context, uuid.UUID, string, entitlement.TransitionInput) (entitlement.Assignment, error)
	DeactivateIdempotent(context.Context, uuid.UUID, string, entitlement.TransitionInput) (entitlement.Assignment, error)
	SetModuleQuotaIdempotent(context.Context, uuid.UUID, string, string, int, int64) (entitlement.ModuleQuota, error)
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

func (h *Handler) EntitlementDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido", nil)
		return
	}
	_, companyID, ok := h.companyScope(w, r)
	if !ok || h.entitlement == nil {
		return
	}
	moduleCode := strings.TrimSpace(r.PathValue("module"))
	reader, specialized := h.entitlement.(entitlementDetailReader)
	if !specialized {
		h.writeError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível", nil)
		return
	}
	item, err := reader.GetForCompany(r.Context(), companyID, moduleCode, time.Now().UTC())
	if err != nil {
		h.writeEntitlementError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, entitlementResponse(item))
}

func (h *Handler) EntitlementAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido", nil)
		return
	}
	_, companyID, ok := h.companyScope(w, r)
	if !ok {
		return
	}
	moduleCode := strings.TrimSpace(r.PathValue("module"))
	h.audit(w, r, entitlement.EventFilter{ModuleCode: moduleCode, CompanyID: &companyID})
}

func (h *Handler) EntitlementAuditByID(w http.ResponseWriter, r *http.Request) {
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
	auditReader, specialized := h.entitlement.(entitlementAuditReader)
	if !specialized {
		h.writeError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "auditoria indisponível", nil)
		return
	}
	assignmentID, err := uuid.Parse(strings.TrimSpace(r.PathValue("entitlementId")))
	if err != nil || assignmentID == uuid.Nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "entitlementId inválido", nil)
		return
	}
	assignment, err := auditReader.AssignmentByID(r.Context(), assignmentID)
	if err != nil {
		h.writeEntitlementError(w, r, err)
		return
	}
	allowed, err := h.identity.UserCanAccessCompany(r.Context(), principal.UserID, assignment.CompanyID)
	if err != nil {
		h.writeIdentityError(w, r, err)
		return
	}
	if !allowed {
		h.writeError(w, r, http.StatusForbidden, "TENANT_MISMATCH", "empresa fora do escopo autenticado", nil)
		return
	}
	h.audit(w, r, entitlement.EventFilter{ModuleCode: assignment.ModuleCode, CompanyID: &assignment.CompanyID})
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request, filter entitlement.EventFilter) {
	auditReader, specialized := h.entitlement.(entitlementAuditReader)
	if !specialized {
		h.writeError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "auditoria indisponível", nil)
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "parâmetros de paginação inválidos", map[string]string{"field": "limit"})
		return
	}
	filter.Origin = strings.TrimSpace(r.URL.Query().Get("origin"))
	filter.Limit = limit
	page, err := auditReader.ListEventsPage(r.Context(), filter, r.URL.Query().Get("cursor"))
	if err != nil {
		if errors.Is(err, entitlement.ErrInvalidCursor) {
			h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "cursor inválido", nil)
			return
		}
		h.writeEntitlementError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, event := range page.Items {
		var company any
		if event.CompanyID != uuid.Nil {
			company = event.CompanyID.String()
		}
		items = append(items, map[string]any{
			"id": event.ID.String(), "tenantId": event.TenantID.String(), "module": event.ModuleCode,
			"companyId": company, "actorId": event.ActorID.String(), "action": event.Action,
			"origin": event.Origin, "at": event.At.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nullIfEmpty(page.NextCursor)})
}

type grantRequest struct {
	ValidFrom *string `json:"validFrom"`
	ValidTo   *string `json:"validTo"`
	Version   *int64  `json:"version"`
}

type patchEntitlementRequest struct {
	Status       string  `json:"status"`
	ValidFrom    *string `json:"validFrom"`
	ValidTo      *string `json:"validTo"`
	ClearValidTo bool    `json:"clearValidTo"`
	Quota        *int    `json:"quota"`
	Version      *int64  `json:"version"`
}

func (h *Handler) GrantEntitlement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		h.writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido", nil)
		return
	}
	principal, companyID, ok := h.companyScope(w, r)
	if !ok {
		return
	}
	key, ok := requiredIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request grantRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	from, err := parseOptionalDate(request.ValidFrom)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "validFrom inválido", nil)
		return
	}
	to, err := parseOptionalDate(request.ValidTo)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "validTo inválido", nil)
		return
	}
	writer, specialized := h.entitlement.(entitlementAdminWriter)
	if !specialized {
		h.writeError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível", nil)
		return
	}
	assignment, err := writer.ActivateIdempotent(r.Context(), principal.UserID, key, entitlement.ActivateInput{
		ModuleCode: strings.TrimSpace(r.PathValue("module")), CompanyID: companyID, Origin: entitlement.OriginAdmin,
		ValidFrom: from, ValidTo: to, ExpectedVersion: valueOrZero(request.Version),
	})
	if err != nil {
		h.writeEntitlementError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, assignmentResponse(assignment))
}

func (h *Handler) PatchEntitlement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		h.writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido", nil)
		return
	}
	principal, companyID, ok := h.companyScope(w, r)
	if !ok {
		return
	}
	key, ok := requiredIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request patchEntitlementRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	if request.Version == nil || *request.Version < 0 || (request.Quota == nil && *request.Version == 0) {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "version é obrigatória", map[string]string{"field": "version"})
		return
	}
	if request.Quota != nil && (request.Status != "" || request.ValidFrom != nil || request.ValidTo != nil || request.ClearValidTo) {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "quota não pode ser combinada com mudança da licença", nil)
		return
	}
	writer, specialized := h.entitlement.(entitlementAdminWriter)
	if !specialized {
		h.writeError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível", nil)
		return
	}
	moduleCode := strings.TrimSpace(r.PathValue("module"))
	if request.Quota != nil {
		quota, err := writer.SetModuleQuotaIdempotent(r.Context(), principal.UserID, key, moduleCode, *request.Quota, *request.Version)
		if err != nil {
			h.writeEntitlementError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, quotaResponse(quota))
		return
	}
	from, err := parseOptionalDate(request.ValidFrom)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "validFrom inválido", nil)
		return
	}
	to, err := parseOptionalDate(request.ValidTo)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "validTo inválido", nil)
		return
	}
	input := entitlement.TransitionInput{ModuleCode: moduleCode, CompanyID: companyID, Origin: entitlement.OriginAdmin, ExpectedVersion: *request.Version}
	var assignment entitlement.Assignment
	switch request.Status {
	case entitlement.StatusRevoked:
		assignment, err = writer.RevokeIdempotent(r.Context(), principal.UserID, key, input)
	case entitlement.StatusInactive:
		assignment, err = writer.DeactivateIdempotent(r.Context(), principal.UserID, key, input)
	case "", entitlement.StatusActive:
		assignment, err = writer.ActivateIdempotent(r.Context(), principal.UserID, key, entitlement.ActivateInput{
			ModuleCode: moduleCode, CompanyID: companyID, Origin: entitlement.OriginAdmin,
			ValidFrom: from, ValidTo: to, ClearValidTo: request.ClearValidTo, ExpectedVersion: *request.Version,
		})
	default:
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "status inválido", nil)
		return
	}
	if err != nil {
		h.writeEntitlementError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, assignmentResponse(assignment))
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

func (h *Handler) companyScope(w http.ResponseWriter, r *http.Request) (platformauth.Principal, uuid.UUID, bool) {
	principal, ok := h.principal(w, r)
	if !ok || h.identity == nil {
		return platformauth.Principal{}, uuid.Nil, ok
	}
	companyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("companyId")))
	if err != nil || companyID == uuid.Nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "companyId inválido", nil)
		return platformauth.Principal{}, uuid.Nil, false
	}
	allowed, err := h.identity.UserCanAccessCompany(r.Context(), principal.UserID, companyID)
	if err != nil {
		h.writeIdentityError(w, r, err)
		return platformauth.Principal{}, uuid.Nil, false
	}
	if !allowed {
		h.writeError(w, r, http.StatusForbidden, "TENANT_MISMATCH", "empresa fora do escopo autenticado", nil)
		return platformauth.Principal{}, uuid.Nil, false
	}
	return principal, companyID, true
}

func requiredIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code": "VALIDATION_ERROR", "message": "Idempotency-Key é obrigatória e deve ter até 200 caracteres",
			"details": map[string]any{"field": "Idempotency-Key"}, "traceId": observability.TraceIDFromContext(r.Context()),
		})
		return "", false
	}
	return key, true
}

func (h *Handler) decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido", nil)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "corpo JSON deve conter um único objeto", nil)
		return false
	}
	return true
}

func parseOptionalDate(value *string) (*time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(*value))
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func assignmentResponse(assignment entitlement.Assignment) map[string]any {
	return map[string]any{
		"id": assignment.ID.String(), "tenantId": assignment.TenantID.String(), "companyId": assignment.CompanyID.String(),
		"module": assignment.ModuleCode, "status": assignment.Status, "validFrom": assignment.ValidFrom.UTC().Format("2006-01-02"),
		"validTo": dateResponse(assignment.ValidTo), "version": assignment.Version,
		"updatedAt": assignment.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func quotaResponse(quota entitlement.ModuleQuota) map[string]any {
	return map[string]any{"module": quota.ModuleCode, "quota": quota.Quota, "used": quota.InUse, "configured": quota.Configured, "version": quota.Version}
}

func (h *Handler) writeEntitlementError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, entitlement.ErrInvalidCursor):
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "cursor inválido", nil)
	case errors.Is(err, entitlement.ErrInvalidRequest):
		h.writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "requisicao de entitlement inválida", nil)
	case errors.Is(err, entitlement.ErrAssignmentNotFound):
		h.writeError(w, r, http.StatusNotFound, "NOT_FOUND", "entitlement não encontrado", nil)
	case errors.Is(err, entitlement.ErrIdempotencyConflict):
		h.writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key já foi usada com outro pedido", nil)
	case errors.Is(err, entitlement.ErrStaleVersion):
		h.writeError(w, r, http.StatusConflict, "STALE_VERSION", "a versão do entitlement está obsoleta", nil)
	case errors.Is(err, entitlement.ErrQuotaExceeded):
		h.writeError(w, r, http.StatusConflict, "LICENSE_POOL_EXHAUSTED", "a quota de licenças foi excedida", nil)
	case errors.Is(err, entitlement.ErrQuotaNotConfigured):
		h.writeError(w, r, http.StatusConflict, "LICENSE_NOT_AVAILABLE", "não há quota de licença configurada", nil)
	case errors.Is(err, entitlement.ErrCoreModuleImmutable):
		h.writeError(w, r, http.StatusConflict, "CORE_MODULE_IMMUTABLE", "módulo núcleo não pode ser alterado", nil)
	case errors.Is(err, entitlement.ErrInvalidTransition):
		h.writeError(w, r, http.StatusConflict, "INVALID_TRANSITION", "transição de licença inválida", nil)
	default:
		h.writeError(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível", nil)
	}
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
		"version": item.Version, "quotaVersion": item.QuotaVersion,
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
