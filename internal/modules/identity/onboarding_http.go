package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	maxDevelopmentBodyBytes = 4 << 10
)

type developmentOnboardingPayload struct {
	TenantName            *string `json:"tenant_name"`
	TenantDocument        *string `json:"tenant_document"`
	CompanyDocument       *string `json:"company_document"`
	CompanyName           *string `json:"company_name"`
	CompanyKind           *string `json:"company_kind"`
	EstablishmentDocument *string `json:"establishment_document"`
	EstablishmentName     *string `json:"establishment_name"`
	Street                *string `json:"street"`
	Number                *string `json:"number"`
	OwnerEmail            *string `json:"owner_email"`
	OwnerName             *string `json:"owner_name"`
}

// NewDevelopmentHTTPHandler cria o endpoint explícito de desenvolvimento.
//
// A rota aceita um objeto JSON com sobreposições da carga sintética e a chave
// Idempotency-Key no cabeçalho. Cidade, estado e CEP são sempre os da fixture de
// Fortaleza; tenant_id, onboarding_key e qualquer outro campo são rejeitados.
// Não há autenticação de produção neste endpoint, portanto ele só deve ser
// registrado pelo servidor quando APP_ENV=development.
func NewDevelopmentHTTPHandler(service Onboarder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeOnboardingJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"status": "error",
				"error":  "método não permitido",
			})
			return
		}

		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" || len(key) > 200 {
			writeOnboardingJSON(w, http.StatusBadRequest, map[string]string{
				"status": "error",
				"error":  "Idempotency-Key inválida",
			})
			return
		}

		payload, err := decodeDevelopmentBody(w, r)
		if err != nil {
			writeOnboardingJSON(w, http.StatusBadRequest, map[string]string{
				"status": "error",
				"error":  "JSON inválido",
			})
			return
		}

		if service == nil {
			writeOnboardingJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "error",
				"error":  "onboarding indisponível",
			})
			return
		}

		request := DevelopmentRequest(key)
		applyDevelopmentPayload(&request, payload)
		result, err := service.Onboard(r.Context(), request)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, ErrIdempotencyConflict):
				status = http.StatusConflict
			case errors.Is(err, ErrInvalidOnboardingRequest):
				status = http.StatusBadRequest
			}
			writeOnboardingJSON(w, status, map[string]string{
				"status": "error",
				"error":  "não foi possível iniciar o onboarding",
			})
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeOnboardingJSON(w, http.StatusOK, map[string]string{
			"status":           "ok",
			"tenant_id":        result.TenantID.String(),
			"company_id":       result.CompanyID.String(),
			"establishment_id": result.EstablishmentID.String(),
			"owner_user_id":    result.OwnerUserID.String(),
		})
	})
}

func decodeDevelopmentBody(w http.ResponseWriter, r *http.Request) (developmentOnboardingPayload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDevelopmentBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return developmentOnboardingPayload{}, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return developmentOnboardingPayload{}, errors.New("o corpo deve ser um objeto JSON")
	}
	var payload developmentOnboardingPayload
	payloadDecoder := json.NewDecoder(bytes.NewReader(trimmed))
	payloadDecoder.DisallowUnknownFields()
	if err := payloadDecoder.Decode(&payload); err != nil {
		return developmentOnboardingPayload{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return developmentOnboardingPayload{}, errors.New("corpo contém mais de um JSON")
		}
		return developmentOnboardingPayload{}, err
	}
	return payload, nil
}

func applyDevelopmentPayload(request *OnboardingRequest, payload developmentOnboardingPayload) {
	apply := func(destination *string, value *string) {
		if value != nil {
			*destination = *value
		}
	}
	apply(&request.TenantName, payload.TenantName)
	apply(&request.TenantDocument, payload.TenantDocument)
	apply(&request.CompanyDocument, payload.CompanyDocument)
	apply(&request.CompanyName, payload.CompanyName)
	apply(&request.CompanyKind, payload.CompanyKind)
	apply(&request.EstablishmentDocument, payload.EstablishmentDocument)
	apply(&request.EstablishmentName, payload.EstablishmentName)
	apply(&request.Street, payload.Street)
	apply(&request.Number, payload.Number)
	apply(&request.OwnerEmail, payload.OwnerEmail)
	apply(&request.OwnerName, payload.OwnerName)
}

func writeOnboardingJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
