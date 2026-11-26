package party

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

const maxFamilyRelationLength = 40

type developmentFamilyLinkPayload struct {
	DeceasedID *string `json:"deceased_id"`
	PersonID   *string `json:"person_id"`
	Relation   *string `json:"relation"`
}

// developmentFamilyLinkPerson carries only the identity and names resolved from
// the source party; names are never copied into party.family_link.
type developmentFamilyLinkPerson struct {
	ID          string  `json:"id"`
	CivilName   string  `json:"civil_name"`
	SocialName  *string `json:"social_name"`
	DisplayName string  `json:"display_name"`
}

type developmentFamilyLinkResponse struct {
	ID       string                      `json:"id"`
	Relation string                      `json:"relation"`
	Deceased developmentFamilyLinkPerson `json:"deceased"`
	Person   developmentFamilyLinkPerson `json:"person"`
}

// NewDevelopmentFamilyLinkHTTPHandler creates the explicit development-only
// family link endpoints:
//
//	POST /_dev/family-links       creates a link between an existing deceased
//	                              and an existing natural person
//	GET  /_dev/family-links/{id}  reads the link in the tenant from the header
//
// The tenant comes exclusively from X-Amparius-Development-Tenant-ID; tenant_id
// in the body and unknown fields are rejected. Neither the deceased nor the
// person is created implicitly. There is no production authentication here, so
// it must only be registered when APP_ENV=development and
// ENABLE_DEV_ONBOARDING=true.
func NewDevelopmentFamilyLinkHTTPHandler(service FamilyLinkService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")

		tenantID, err := developmentTenantID(r)
		if err != nil {
			writeDevelopmentFamilyLinkError(w, http.StatusBadRequest, "cabeçalho "+developmentTenantHeader+" ausente ou inválido")
			return
		}

		if idText := r.PathValue("id"); idText != "" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				writeDevelopmentFamilyLinkError(w, http.StatusMethodNotAllowed, "método não permitido")
				return
			}
			handleDevelopmentFamilyLinkGet(w, r, service, tenantID, idText)
			return
		}

		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeDevelopmentFamilyLinkError(w, http.StatusMethodNotAllowed, "método não permitido")
			return
		}
		handleDevelopmentFamilyLinkCreate(w, r, service, tenantID)
	})
}

func handleDevelopmentFamilyLinkCreate(w http.ResponseWriter, r *http.Request, service FamilyLinkService, tenantID uuid.UUID) {
	input, err := decodeDevelopmentFamilyLinkBody(w, r)
	if err != nil {
		if errors.Is(err, ErrInvalidPartyRequest) {
			writeDevelopmentFamilyLinkError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeDevelopmentFamilyLinkError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if service == nil {
		writeDevelopmentFamilyLinkError(w, http.StatusServiceUnavailable, "vínculo familiar indisponível")
		return
	}

	created, err := service.CreateFamilyLink(db.WithTenant(r.Context(), tenantID), input)
	if err != nil {
		writeDevelopmentFamilyLinkServiceError(w, err, true)
		return
	}

	w.Header().Set("Location", "/_dev/family-links/"+created.ID.String())
	writeDevelopmentFamilyLinkJSON(w, http.StatusCreated, map[string]any{
		"status":      "ok",
		"family_link": developmentFamilyLinkFromModel(created),
	})
}

func handleDevelopmentFamilyLinkGet(w http.ResponseWriter, r *http.Request, service FamilyLinkService, tenantID uuid.UUID, idText string) {
	id, err := uuid.Parse(strings.TrimSpace(idText))
	if err != nil || id == uuid.Nil {
		writeDevelopmentFamilyLinkError(w, http.StatusBadRequest, "id do vínculo familiar inválido")
		return
	}
	if service == nil {
		writeDevelopmentFamilyLinkError(w, http.StatusServiceUnavailable, "consulta de vínculo familiar indisponível")
		return
	}

	found, err := service.GetFamilyLinkByID(db.WithTenant(r.Context(), tenantID), id)
	if err != nil {
		writeDevelopmentFamilyLinkServiceError(w, err, false)
		return
	}
	writeDevelopmentFamilyLinkJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"family_link": developmentFamilyLinkFromModel(found),
	})
}

func decodeDevelopmentFamilyLinkBody(w http.ResponseWriter, r *http.Request) (FamilyLinkCreate, error) {
	trimmed, err := decodeJSONObject(w, r)
	if err != nil {
		return FamilyLinkCreate{}, err
	}
	var payload developmentFamilyLinkPayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return FamilyLinkCreate{}, err
	}
	deceasedID, err := requiredUUIDField("deceased_id", payload.DeceasedID)
	if err != nil {
		return FamilyLinkCreate{}, err
	}
	personID, err := requiredUUIDField("person_id", payload.PersonID)
	if err != nil {
		return FamilyLinkCreate{}, err
	}
	if payload.Relation == nil || strings.TrimSpace(*payload.Relation) == "" {
		return FamilyLinkCreate{}, fmt.Errorf("%w: relation é obrigatório", ErrInvalidPartyRequest)
	}
	relation := strings.TrimSpace(*payload.Relation)
	if len([]rune(relation)) > maxFamilyRelationLength {
		return FamilyLinkCreate{}, fmt.Errorf("%w: relation excede %d caracteres", ErrInvalidPartyRequest, maxFamilyRelationLength)
	}
	return FamilyLinkCreate{
		DeceasedID: deceasedID,
		PersonID:   personID,
		Relation:   relation,
	}, nil
}

func requiredUUIDField(field string, value *string) (uuid.UUID, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return uuid.Nil, fmt.Errorf("%w: %s é obrigatório", ErrInvalidPartyRequest, field)
	}
	parsed, err := uuid.Parse(strings.TrimSpace(*value))
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: %s deve ser UUID válido", ErrInvalidPartyRequest, field)
	}
	return parsed, nil
}

func developmentFamilyLinkFromModel(model FamilyLink) developmentFamilyLinkResponse {
	return developmentFamilyLinkResponse{
		ID:       model.ID.String(),
		Relation: model.Relation,
		Deceased: developmentFamilyLinkPersonFromParty(model.Deceased),
		Person:   developmentFamilyLinkPersonFromParty(model.Person),
	}
}

func developmentFamilyLinkPersonFromParty(model Party) developmentFamilyLinkPerson {
	return developmentFamilyLinkPerson{
		ID:          model.ID.String(),
		CivilName:   model.CivilName(),
		SocialName:  model.SocialName,
		DisplayName: model.DisplayName(),
	}
}

func writeDevelopmentFamilyLinkServiceError(w http.ResponseWriter, err error, creating bool) {
	switch {
	case errors.Is(err, ErrFamilyLinkDeceasedNotFound):
		writeDevelopmentFamilyLinkError(w, http.StatusNotFound, "falecido não encontrado neste tenant")
	case errors.Is(err, ErrFamilyLinkPersonNotFound):
		writeDevelopmentFamilyLinkError(w, http.StatusNotFound, "familiar não encontrado neste tenant")
	case errors.Is(err, ErrFamilyLinkPersonNotApplicable):
		writeDevelopmentFamilyLinkError(w, http.StatusConflict, "pessoa jurídica não pode ser vinculada como familiar")
	case errors.Is(err, ErrFamilyLinkRelationInvalid):
		writeDevelopmentFamilyLinkError(w, http.StatusBadRequest, "relação familiar inválida")
	case errors.Is(err, ErrFamilyLinkNotFound):
		writeDevelopmentFamilyLinkError(w, http.StatusNotFound, "vínculo familiar não encontrado")
	case errors.Is(err, db.ErrTenantRequired):
		writeDevelopmentFamilyLinkError(w, http.StatusBadRequest, "tenant ausente no contexto")
	default:
		if creating {
			writeDevelopmentFamilyLinkError(w, http.StatusInternalServerError, "não foi possível criar o vínculo familiar")
		} else {
			writeDevelopmentFamilyLinkError(w, http.StatusInternalServerError, "não foi possível consultar o vínculo familiar")
		}
	}
}

func writeDevelopmentFamilyLinkError(w http.ResponseWriter, status int, message string) {
	writeDevelopmentFamilyLinkJSON(w, status, map[string]string{"status": "error", "error": message})
}

func writeDevelopmentFamilyLinkJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
