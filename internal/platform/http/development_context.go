package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

const (
	DevelopmentTenantHeader  = "X-Amparius-Development-Tenant-ID"
	DevelopmentCompanyHeader = "X-Amparius-Development-Company-ID"
)

var ErrDevelopmentScopeUnavailable = errors.New("escopo de desenvolvimento indisponível")

// DevelopmentCompanyResolver é o contrato mínimo da autenticação dev-only.
// A implementação deve consultar identity.company sob RLS; o middleware não
// aceita tenant_id ou company_id do corpo da requisição.
type DevelopmentCompanyResolver interface {
	CompanyBelongsToTenant(context.Context, uuid.UUID, uuid.UUID) (bool, error)
}

// DevelopmentTenantCompanyContext compõe o escopo confiável para rotas dev-only.
// Em produção, esta fronteira deve ser chamada pelo adaptador de autenticação
// real, depois de validar a sessão; os headers de desenvolvimento não são
// autenticação e nunca devem ser registrados fora do opt-in dev-only.
func DevelopmentTenantCompanyContext(next http.Handler, resolver DevelopmentCompanyResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")

		tenantID, err := parseDevelopmentUUID(r.Header.Get(DevelopmentTenantHeader))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"code": "INVALID_DEVELOPMENT_SCOPE", "message": "escopo de desenvolvimento inválido"},
			})
			return
		}
		companyID, err := parseDevelopmentUUID(r.Header.Get(DevelopmentCompanyHeader))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]string{"code": "INVALID_DEVELOPMENT_SCOPE", "message": "escopo de desenvolvimento inválido"},
			})
			return
		}
		if resolver == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]string{"code": "IDENTITY_UNAVAILABLE", "message": "identidade indisponível"},
			})
			return
		}

		ctx := db.WithTenant(r.Context(), tenantID)
		belongs, err := resolver.CompanyBelongsToTenant(ctx, tenantID, companyID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]string{"code": "IDENTITY_UNAVAILABLE", "message": "identidade indisponível"},
			})
			return
		}
		if !belongs {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": map[string]string{"code": "SCOPE_NOT_FOUND", "message": "escopo não encontrado"},
			})
			return
		}
		next.ServeHTTP(w, r.WithContext(db.WithCompany(ctx, companyID)))
	})
}

func parseDevelopmentUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrDevelopmentScopeUnavailable
	}
	return id, nil
}
