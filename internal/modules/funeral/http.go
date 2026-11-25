// Package funeral expõe a superfície mínima do módulo opcional de funerária.
package funeral

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alltomatos/amparius/internal/modules/entitlement"
)

const accessPath = "/_dev/funeral/access"

// AccessChecker é o contrato mínimo do enforcement transversal.
type AccessChecker interface {
	Check(context.Context, string, time.Time) error
}

// NewDevelopmentHTTPHandler cria o endpoint dev-only que representa o ponto
// de entrada do módulo opcional. O contexto tenant+empresa deve ter sido
// composto pelo middleware de autenticação antes deste handler.
func NewDevelopmentHTTPHandler(checker AccessChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != accessPath {
			writeError(w, http.StatusNotFound, "FUNERAL_ROUTE_NOT_FOUND", "rota não encontrada")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
			return
		}
		if checker == nil {
			writeError(w, http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível")
			return
		}
		if err := checker.Check(r.Context(), entitlement.ModuleFuneral, time.Now().UTC()); err != nil {
			status := http.StatusServiceUnavailable
			code := "ENTITLEMENT_UNAVAILABLE"
			if errors.Is(err, entitlement.ErrModuleDisabled) {
				status = http.StatusForbidden
				code = "ENTITLEMENT_MODULE_NOT_LICENSED"
			}
			writeError(w, status, code, "módulo funeral bloqueado")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"module_code": entitlement.ModuleFuneral,
			"access":      "granted",
		})
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
