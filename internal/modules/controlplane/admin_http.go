package controlplane

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

// PlatformReports é a leitura cross-tenant (views de reporting) usada por /admin/v1.
type PlatformReports interface {
	ListTenants(ctx context.Context, cursor string, limit int) (TenantSummaryPage, error)
	GetTenant(ctx context.Context, tenantID uuid.UUID) (TenantSummary, error)
	ListLicensePools(ctx context.Context, tenantID uuid.UUID) ([]LicensePool, error)
	ListAssignments(ctx context.Context, tenantID uuid.UUID, cursor string, limit int) (PlatformAssignmentPage, error)
	ListEvents(ctx context.Context, tenantID uuid.UUID, cursor string, limit int) (PlatformEventPage, error)
}

// AdminHandler serve `/admin/v1/*` (ADR 0040). Leituras usam as views de
// reporting; escritas usam o serviço público de entitlement com o tenant alvo
// explícito no contexto, preservando RLS, Idempotency-Key, version e origem
// `admin` com o ator de plataforma.
type AdminHandler struct {
	reports PlatformReports
	writer  entitlementAdminWriter
	logger  *slog.Logger
	now     func() time.Time
}

func NewAdminHandler(reports PlatformReports, writer entitlementAdminWriter, logger *slog.Logger) *AdminHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminHandler{reports: reports, writer: writer, logger: logger, now: time.Now}
}

// Tenants: GET /admin/v1/tenants
func (h *AdminHandler) Tenants(w http.ResponseWriter, r *http.Request) {
	_, ok := h.begin(w, r)
	if !ok {
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "parâmetros de paginação inválidos", map[string]string{"field": "limit"})
		return
	}
	page, err := h.reports.ListTenants(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		h.failReport(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, tenantSummaryResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"scope": "platform", "items": items, "nextCursor": nullIfEmpty(page.NextCursor)})
}

// Licenses: GET /admin/v1/tenants/{tenantId}/licenses (cotas + licenças por empresa)
func (h *AdminHandler) Licenses(w http.ResponseWriter, r *http.Request) {
	_, tenantID, ok := h.beginTenant(w, r)
	if !ok {
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "parâmetros de paginação inválidos", map[string]string{"field": "limit"})
		return
	}
	summary, err := h.reports.GetTenant(r.Context(), tenantID)
	if err != nil {
		h.failReport(w, r, err)
		return
	}
	pools, err := h.reports.ListLicensePools(r.Context(), tenantID)
	if err != nil {
		h.failReport(w, r, err)
		return
	}
	page, err := h.reports.ListAssignments(r.Context(), tenantID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		h.failReport(w, r, err)
		return
	}
	poolItems := make([]map[string]any, 0, len(pools))
	for _, pool := range pools {
		poolItems = append(poolItems, map[string]any{
			"module": pool.ModuleCode, "quota": pool.Quota, "used": pool.InUse, "version": pool.Version,
			"updatedAt": pool.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	assignments := make([]map[string]any, 0, len(page.Items))
	for _, item := range page.Items {
		assignments = append(assignments, map[string]any{
			"id": item.ID.String(), "tenantId": item.TenantID.String(), "companyId": item.CompanyID.String(),
			"module": item.ModuleCode, "status": item.Status, "validFrom": item.ValidFrom.UTC().Format("2006-01-02"),
			"validTo": dateResponse(item.ValidTo), "version": item.Version,
			"updatedAt": item.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope": "platform", "tenant": tenantSummaryResponse(summary),
		"pools": poolItems, "assignments": assignments, "nextCursor": nullIfEmpty(page.NextCursor),
	})
}

// Events: GET /admin/v1/tenants/{tenantId}/entitlement-events
func (h *AdminHandler) Events(w http.ResponseWriter, r *http.Request) {
	_, tenantID, ok := h.beginTenant(w, r)
	if !ok {
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "parâmetros de paginação inválidos", map[string]string{"field": "limit"})
		return
	}
	page, err := h.reports.ListEvents(r.Context(), tenantID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		h.failReport(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, event := range page.Items {
		items = append(items, map[string]any{
			"id": event.ID.String(), "tenantId": event.TenantID.String(), "companyId": event.CompanyID.String(),
			"module": event.ModuleCode, "actorId": event.ActorID.String(), "action": event.Action,
			"origin": event.Origin, "at": event.At.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"scope": "platform", "items": items, "nextCursor": nullIfEmpty(page.NextCursor)})
}

type adminGrantRequest struct {
	ValidFrom *string `json:"validFrom"`
	ValidTo   *string `json:"validTo"`
	Version   *int64  `json:"version"`
}

type adminVersionRequest struct {
	Version *int64 `json:"version"`
}

type adminQuotaRequest struct {
	Quota   *int   `json:"quota"`
	Version *int64 `json:"version"`
}

// Grant: PUT /admin/v1/tenants/{tenantId}/companies/{companyId}/entitlements/{module}
func (h *AdminHandler) Grant(w http.ResponseWriter, r *http.Request) {
	principal, tenantID, ok := h.beginTenant(w, r)
	if !ok {
		return
	}
	companyID, key, ok := h.writeScope(w, r)
	if !ok {
		return
	}
	var request adminGrantRequest
	if !h.decode(w, r, &request) {
		return
	}
	from, err := parseOptionalDate(request.ValidFrom)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "validFrom inválido", nil)
		return
	}
	to, err := parseOptionalDate(request.ValidTo)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "validTo inválido", nil)
		return
	}
	assignment, err := h.writer.ActivateIdempotent(db.WithTenant(r.Context(), tenantID), principal.UserID, key, entitlement.ActivateInput{
		ModuleCode: strings.TrimSpace(r.PathValue("module")), CompanyID: companyID, Origin: entitlement.OriginAdmin,
		ValidFrom: from, ValidTo: to, ExpectedVersion: valueOrZero(request.Version),
	})
	if err != nil {
		h.failEntitlement(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, assignmentResponse(assignment))
}

// Revoke: POST /admin/v1/tenants/{tenantId}/companies/{companyId}/entitlements/{module}/revoke
func (h *AdminHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	principal, tenantID, ok := h.beginTenant(w, r)
	if !ok {
		return
	}
	companyID, key, ok := h.writeScope(w, r)
	if !ok {
		return
	}
	var request adminVersionRequest
	if !h.decode(w, r, &request) {
		return
	}
	if request.Version == nil || *request.Version < 1 {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "version é obrigatória", map[string]string{"field": "version"})
		return
	}
	assignment, err := h.writer.RevokeIdempotent(db.WithTenant(r.Context(), tenantID), principal.UserID, key, entitlement.TransitionInput{
		ModuleCode: strings.TrimSpace(r.PathValue("module")), CompanyID: companyID, Origin: entitlement.OriginAdmin, ExpectedVersion: *request.Version,
	})
	if err != nil {
		h.failEntitlement(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, assignmentResponse(assignment))
}

// SetQuota: PUT /admin/v1/tenants/{tenantId}/modules/{module}/quota
func (h *AdminHandler) SetQuota(w http.ResponseWriter, r *http.Request) {
	principal, tenantID, ok := h.beginTenant(w, r)
	if !ok {
		return
	}
	key, ok := h.idempotencyKey(w, r)
	if !ok {
		return
	}
	var request adminQuotaRequest
	if !h.decode(w, r, &request) {
		return
	}
	if request.Quota == nil || *request.Quota < 0 || request.Version == nil || *request.Version < 0 {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "quota e version são obrigatórias", nil)
		return
	}
	quota, err := h.writer.SetModuleQuotaIdempotent(db.WithTenant(r.Context(), tenantID), principal.UserID, key,
		strings.TrimSpace(r.PathValue("module")), *request.Quota, *request.Version)
	if err != nil {
		h.failEntitlement(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, quotaResponse(quota))
}

// --- infraestrutura ---

// auditedWriter registra o status final para a auditoria de cada resposta.
type auditedWriter struct {
	http.ResponseWriter
	status int
}

func (a *auditedWriter) WriteHeader(status int) {
	a.status = status
	a.ResponseWriter.WriteHeader(status)
}

// Audited aplica a auditoria de plataforma (ADR 0040 §5) a uma rota: toda
// resposta registra ator, tenant alvo, ação e resultado, inclusive recusas.
func (h *AdminHandler) Audited(action string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &auditedWriter{ResponseWriter: w, status: http.StatusOK}
		w.Header().Set("Cache-Control", "no-store")
		next(rec, r)
		principal, _ := platformauth.PlatformPrincipalFromContext(r.Context())
		tenantID, _ := uuid.Parse(strings.TrimSpace(r.PathValue("tenantId")))
		h.audit(r, principal, action, tenantID, rec.status)
	})
}

func (h *AdminHandler) begin(w http.ResponseWriter, r *http.Request) (platformauth.PlatformPrincipal, bool) {
	principal, err := platformauth.PlatformPrincipalFromContext(r.Context())
	if err != nil {
		h.fail(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "autenticação de plataforma necessária", nil)
		return platformauth.PlatformPrincipal{}, false
	}
	if h.reports == nil {
		h.fail(w, r, http.StatusServiceUnavailable, "PLATFORM_REPORT_UNAVAILABLE", "leitura de plataforma indisponível", nil)
		return principal, false
	}
	return principal, true
}

func (h *AdminHandler) beginTenant(w http.ResponseWriter, r *http.Request) (platformauth.PlatformPrincipal, uuid.UUID, bool) {
	principal, ok := h.begin(w, r)
	if !ok {
		return principal, uuid.Nil, false
	}
	tenantID, err := uuid.Parse(strings.TrimSpace(r.PathValue("tenantId")))
	if err != nil || tenantID == uuid.Nil {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "tenantId inválido", nil)
		return principal, uuid.Nil, false
	}
	return principal, tenantID, true
}

func (h *AdminHandler) writeScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	companyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("companyId")))
	if err != nil || companyID == uuid.Nil {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "companyId inválido", nil)
		return uuid.Nil, "", false
	}
	key, ok := h.idempotencyKey(w, r)
	if !ok {
		return uuid.Nil, "", false
	}
	return companyID, key, true
}

func (h *AdminHandler) idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.writer == nil {
		h.fail(w, r, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível", nil)
		return "", false
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Idempotency-Key é obrigatória e deve ter até 200 caracteres", map[string]string{"field": "Idempotency-Key"})
		return "", false
	}
	return key, true
}

func (h *AdminHandler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	return (&Handler{}).decodeJSON(w, r, target)
}

func (h *AdminHandler) fail(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	writeJSON(w, status, map[string]any{
		"code": code, "message": message, "details": detailsOrEmpty(details),
		"traceId": observability.TraceIDFromContext(r.Context()),
	})
}

func (h *AdminHandler) failReport(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrPlatformInvalidCursor):
		h.fail(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "cursor inválido", map[string]string{"field": "cursor"})
	case errors.Is(err, ErrPlatformTenantNotFound):
		h.fail(w, r, http.StatusNotFound, "TENANT_NOT_FOUND", "tenant sem registro de licenças no control plane", nil)
	case errors.Is(err, ErrPlatformReportUnavailable):
		h.fail(w, r, http.StatusServiceUnavailable, "PLATFORM_REPORT_UNAVAILABLE", "view de reporting de plataforma ausente ou defasada", nil)
	default:
		h.fail(w, r, http.StatusServiceUnavailable, "PLATFORM_REPORT_UNAVAILABLE", "leitura de plataforma indisponível", nil)
	}
}

func (h *AdminHandler) failEntitlement(w http.ResponseWriter, r *http.Request, err error) {
	(&Handler{}).writeEntitlementError(w, r, err)
}

// audit registra ator, tenant alvo, ação e resultado de toda resposta
// (ADR 0040 §5). Sem PII: só ids opacos. Para escritas, o evento durável com
// ator e origin=admin fica em entitlement.entitlement_event/quota_event.
func (h *AdminHandler) audit(r *http.Request, principal platformauth.PlatformPrincipal, action string, tenantID uuid.UUID, status int) {
	actor := ""
	if principal.UserID != uuid.Nil {
		actor = principal.UserID.String()
	}
	target := ""
	if tenantID != uuid.Nil {
		target = tenantID.String()
	}
	result := "ok"
	if status >= 400 {
		result = "error"
	}
	h.logger.LogAttrs(r.Context(), slog.LevelInfo, "platform_audit",
		slog.String("event", "platform_audit"), slog.String("scope", "platform"),
		slog.String("actor_id", actor), slog.String("target_tenant_id", target),
		slog.String("action", action), slog.Int("status", status), slog.String("result", result),
		slog.String("trace_id", observability.TraceIDFromContext(r.Context())))
}

func tenantSummaryResponse(item TenantSummary) map[string]any {
	response := map[string]any{
		"tenantId": item.TenantID.String(), "modulesWithQuota": item.ModulesWithQuota, "quotaTotal": item.QuotaTotal,
		"assignmentsTotal": item.AssignmentsTotal, "assignmentsActive": item.AssignmentsActive,
		"userSeats": nil, "userUsed": nil, "lastEventAt": timeResponse(item.LastEventAt),
	}
	if item.UserSeats != nil {
		response["userSeats"] = *item.UserSeats
	}
	if item.UserUsed != nil {
		response["userUsed"] = *item.UserUsed
	}
	return response
}
