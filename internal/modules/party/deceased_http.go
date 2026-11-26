package party

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

const maxDeceasedTextLength = 200

type developmentDeceasedPayload struct {
	PersonID         *string `json:"person_id"`
	DeathAt          *string `json:"death_at"`
	DeathCertificate *string `json:"death_certificate"`
	BurialSite       *string `json:"burial_site"`
}

type developmentDeceasedResponse struct {
	ID               string  `json:"id"`
	PersonID         string  `json:"person_id"`
	DeathAt          string  `json:"death_at"`
	DeathCertificate *string `json:"death_certificate"`
	BurialSite       *string `json:"burial_site"`
	CivilName        string  `json:"civil_name"`
	SocialName       *string `json:"social_name"`
	DisplayName      string  `json:"display_name"`
}

// NewDevelopmentDeceasedHTTPHandler creates the explicit development-only
// deceased endpoints. The person must already exist in the selected tenant.
func NewDevelopmentDeceasedHTTPHandler(service DeceasedService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")

		tenantID, err := developmentTenantID(r)
		if err != nil {
			writeDevelopmentDeceasedError(w, http.StatusBadRequest, "cabeçalho "+developmentTenantHeader+" ausente ou inválido")
			return
		}

		if idText := r.PathValue("id"); idText != "" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				writeDevelopmentDeceasedError(w, http.StatusMethodNotAllowed, "método não permitido")
				return
			}
			handleDevelopmentDeceasedGet(w, r, service, tenantID, idText)
			return
		}

		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeDevelopmentDeceasedError(w, http.StatusMethodNotAllowed, "método não permitido")
			return
		}
		handleDevelopmentDeceasedCreate(w, r, service, tenantID)
	})
}

func handleDevelopmentDeceasedCreate(w http.ResponseWriter, r *http.Request, service DeceasedService, tenantID uuid.UUID) {
	input, err := decodeDevelopmentDeceasedBody(w, r)
	if err != nil {
		if errors.Is(err, ErrInvalidPartyRequest) {
			writeDevelopmentDeceasedError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeDevelopmentDeceasedError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if service == nil {
		writeDevelopmentDeceasedError(w, http.StatusServiceUnavailable, "cadastro de falecido indisponível")
		return
	}

	created, err := service.CreateDeceased(db.WithTenant(r.Context(), tenantID), input)
	if err != nil {
		writeDevelopmentDeceasedServiceError(w, err, true)
		return
	}

	w.Header().Set("Location", "/_dev/deceased/"+created.ID.String())
	writeDevelopmentDeceasedJSON(w, http.StatusCreated, map[string]any{
		"status":   "ok",
		"deceased": developmentDeceasedFromModel(created),
	})
}

func handleDevelopmentDeceasedGet(w http.ResponseWriter, r *http.Request, service DeceasedService, tenantID uuid.UUID, idText string) {
	id, err := uuid.Parse(strings.TrimSpace(idText))
	if err != nil || id == uuid.Nil {
		writeDevelopmentDeceasedError(w, http.StatusBadRequest, "id do falecido inválido")
		return
	}
	if service == nil {
		writeDevelopmentDeceasedError(w, http.StatusServiceUnavailable, "consulta de falecido indisponível")
		return
	}

	found, err := service.GetDeceasedByID(db.WithTenant(r.Context(), tenantID), id)
	if err != nil {
		writeDevelopmentDeceasedServiceError(w, err, false)
		return
	}
	writeDevelopmentDeceasedJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"deceased": developmentDeceasedFromModel(found),
	})
}

func decodeDevelopmentDeceasedBody(w http.ResponseWriter, r *http.Request) (DeceasedCreate, error) {
	trimmed, err := decodeJSONObject(w, r)
	if err != nil {
		return DeceasedCreate{}, err
	}
	var payload developmentDeceasedPayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return DeceasedCreate{}, err
	}
	if payload.PersonID == nil || strings.TrimSpace(*payload.PersonID) == "" {
		return DeceasedCreate{}, fmt.Errorf("%w: person_id é obrigatório", ErrInvalidPartyRequest)
	}
	personID, err := uuid.Parse(strings.TrimSpace(*payload.PersonID))
	if err != nil || personID == uuid.Nil {
		return DeceasedCreate{}, fmt.Errorf("%w: person_id deve ser UUID válido", ErrInvalidPartyRequest)
	}
	if payload.DeathAt == nil || strings.TrimSpace(*payload.DeathAt) == "" {
		return DeceasedCreate{}, fmt.Errorf("%w: death_at é obrigatório", ErrInvalidPartyRequest)
	}
	deathAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*payload.DeathAt))
	if err != nil {
		return DeceasedCreate{}, fmt.Errorf("%w: death_at deve estar em RFC3339", ErrInvalidPartyRequest)
	}
	deathCertificate, err := validateDevelopmentDeceasedText("death_certificate", payload.DeathCertificate)
	if err != nil {
		return DeceasedCreate{}, err
	}
	burialSite, err := validateDevelopmentDeceasedText("burial_site", payload.BurialSite)
	if err != nil {
		return DeceasedCreate{}, err
	}
	return DeceasedCreate{
		PersonID:         personID,
		DeathAt:          deathAt,
		DeathCertificate: deathCertificate,
		BurialSite:       burialSite,
	}, nil
}

func validateDevelopmentDeceasedText(field string, value *string) (*string, error) {
	normalized := normalizeDeceasedText(value)
	if normalized != nil && utf8.RuneCountInString(*normalized) > maxDeceasedTextLength {
		return nil, fmt.Errorf("%w: %s excede %d caracteres", ErrInvalidPartyRequest, field, maxDeceasedTextLength)
	}
	return normalized, nil
}

func developmentDeceasedFromModel(model Deceased) developmentDeceasedResponse {
	return developmentDeceasedResponse{
		ID:               model.ID.String(),
		PersonID:         model.PersonID.String(),
		DeathAt:          model.DeathAt.Format(time.RFC3339Nano),
		DeathCertificate: model.DeathCertificate,
		BurialSite:       model.BurialSite,
		CivilName:        model.Person.CivilName(),
		SocialName:       model.Person.SocialName,
		DisplayName:      model.Person.DisplayName(),
	}
}

func writeDevelopmentDeceasedServiceError(w http.ResponseWriter, err error, creating bool) {
	switch {
	case errors.Is(err, ErrDeceasedPersonNotFound):
		writeDevelopmentDeceasedError(w, http.StatusNotFound, "pessoa não encontrada neste tenant")
	case errors.Is(err, ErrDeceasedPersonNotApplicable):
		writeDevelopmentDeceasedError(w, http.StatusConflict, "pessoa jurídica não pode ser cadastrada como falecida")
	case errors.Is(err, ErrDeceasedNotFound):
		writeDevelopmentDeceasedError(w, http.StatusNotFound, "falecido não encontrado")
	case errors.Is(err, db.ErrTenantRequired):
		writeDevelopmentDeceasedError(w, http.StatusBadRequest, "tenant ausente no contexto")
	default:
		if creating {
			writeDevelopmentDeceasedError(w, http.StatusInternalServerError, "não foi possível cadastrar o falecido")
		} else {
			writeDevelopmentDeceasedError(w, http.StatusInternalServerError, "não foi possível consultar o falecido")
		}
	}
}

func writeDevelopmentDeceasedError(w http.ResponseWriter, status int, message string) {
	writeDevelopmentDeceasedJSON(w, status, map[string]string{"status": "error", "error": message})
}

func writeDevelopmentDeceasedJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
