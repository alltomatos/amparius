// Package funeral expõe a superfície mínima do módulo opcional de funerária.
package funeral

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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
func NewDevelopmentHTTPHandler(checker AccessChecker, contractServices ...ContractService) http.Handler {
	var contracts ContractService
	if len(contractServices) > 0 {
		contracts = contractServices[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == accessPath {
			serveAccess(w, r, checker)
			return
		}
		if contracts != nil && strings.HasPrefix(r.URL.Path, "/_dev/funeral/contracts") {
			if err := checkFuneralAccess(r, checker); err != nil {
				writeAccessError(w, err)
				return
			}
			serveContracts(w, r, contracts)
			return
		}
		writeError(w, http.StatusNotFound, "FUNERAL_ROUTE_NOT_FOUND", "rota não encontrada")
	})
}

func serveAccess(w http.ResponseWriter, r *http.Request, checker AccessChecker) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if err := checkFuneralAccess(r, checker); err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "module_code": entitlement.ModuleFuneral, "access": "granted",
	})
}

func checkFuneralAccess(r *http.Request, checker AccessChecker) error {
	if checker == nil {
		return entitlement.ErrEntitlementUnavailable
	}
	return checker.Check(r.Context(), entitlement.ModuleFuneral, time.Now().UTC())
}

func writeAccessError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, "ENTITLEMENT_UNAVAILABLE", "entitlement indisponível"
	if errors.Is(err, entitlement.ErrModuleDisabled) {
		status, code, message = http.StatusForbidden, "ENTITLEMENT_MODULE_NOT_LICENSED", "módulo funeral bloqueado"
	}
	writeError(w, status, code, message)
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
