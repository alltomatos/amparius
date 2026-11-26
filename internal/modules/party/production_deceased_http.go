package party

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alltomatos/amparius/internal/platform/audit"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

// ProductionDeceasedService is the authenticated deceased contract.
type ProductionDeceasedService interface {
	CreateDeceasedWithAudit(context.Context, DeceasedCreate, audit.PolicyProvider) (Deceased, error)
	GetDeceasedByID(context.Context, uuid.UUID) (Deceased, error)
}

// ProductionDeceasedHandler serves authenticated registration and lookup.
type ProductionDeceasedHandler struct {
	service ProductionDeceasedService
	policy  audit.PolicyProvider
}

func NewProductionDeceasedHTTPHandler(service ProductionDeceasedService, policy audit.PolicyProvider) *ProductionDeceasedHandler {
	return &ProductionDeceasedHandler{service: service, policy: policy}
}

type createDeceasedRequest struct {
	PersonID         *string `json:"personId"`
	DeathAt          *string `json:"deathAt"`
	DeathCertificate *string `json:"deathCertificate"`
	BurialSite       *string `json:"burialSite"`
}

type deceasedResponse struct {
	ID               string  `json:"id"`
	PersonID         string  `json:"personId"`
	DeathAt          string  `json:"deathAt"`
	DeathCertificate *string `json:"deathCertificate"`
	BurialSite       *string `json:"burialSite"`
	CivilName        string  `json:"civilName"`
	SocialName       *string `json:"socialName"`
	DisplayName      string  `json:"displayName"`
}

// Create atende POST /v1/deceased: cria um registro de falecido referenciando
// uma pessoa física já cadastrada no tenant do principal. O corpo não aceita
// tenantId, companyId nem personId de outro tenant (tratado como não
// encontrado pelo repositório).
func (h *ProductionDeceasedHandler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.policy == nil {
		writeProductionDeceasedServiceError(w, r, audit.ErrPolicyUnavailable)
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "cadastro de falecido indisponível")
		return
	}

	raw, err := decodeJSONObject(w, r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}
	var request createDeceasedRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}

	input, err := deceasedCreateFromRequest(request)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	created, err := h.service.CreateDeceasedWithAudit(r.Context(), input, h.policy)
	if err != nil {
		writeProductionDeceasedServiceError(w, r, err)
		return
	}

	writeProductionJSON(w, http.StatusCreated, deceasedResponseFromModel(created))
}

// Get atende GET /v1/deceased/{id}: consulta por id no tenant do principal.
// Uma referência de outro tenant é tratada como não encontrada (ADR 0042 §8).
func (h *ProductionDeceasedHandler) Get(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "consulta de falecido indisponível")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "id do falecido inválido")
		return
	}

	found, err := h.service.GetDeceasedByID(r.Context(), id)
	if err != nil {
		writeProductionDeceasedServiceError(w, r, err)
		return
	}
	writeProductionJSON(w, http.StatusOK, deceasedResponseFromModel(found))
}

// productionScope garante que tenant e empresa fixados no contexto HTTP
// (compostos por auth.Require a partir do principal autenticado) estão
// presentes e consistentes. Defesa redundante: auth.Require já garante isso
// antes de chamar este handler (mesmo padrão de production_http.go).
func productionScope(w http.ResponseWriter, r *http.Request) bool {
	principal, err := platformauth.PrincipalFromContext(r.Context())
	if err != nil {
		writeProductionError(w, r, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "autenticação necessária")
		return false
	}
	tenantID, err := db.TenantFromContext(r.Context())
	if err != nil || tenantID != principal.TenantID {
		writeProductionError(w, r, http.StatusForbidden, "SCOPE_FORBIDDEN", "tenant ausente no contexto autenticado")
		return false
	}
	companyID, err := db.CompanyFromContext(r.Context())
	if err != nil || companyID != principal.CompanyID {
		writeProductionError(w, r, http.StatusForbidden, "SCOPE_FORBIDDEN", "empresa ausente no contexto autenticado")
		return false
	}
	return true
}

func deceasedCreateFromRequest(request createDeceasedRequest) (DeceasedCreate, error) {
	personID, err := requiredUUIDField("personId", request.PersonID)
	if err != nil {
		return DeceasedCreate{}, err
	}
	if request.DeathAt == nil || strings.TrimSpace(*request.DeathAt) == "" {
		return DeceasedCreate{}, errors.New("deathAt é obrigatório")
	}
	deathAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*request.DeathAt))
	if err != nil {
		return DeceasedCreate{}, errors.New("deathAt deve estar em RFC3339")
	}
	deathCertificate, err := validateProductionDeceasedText("deathCertificate", request.DeathCertificate)
	if err != nil {
		return DeceasedCreate{}, err
	}
	burialSite, err := validateProductionDeceasedText("burialSite", request.BurialSite)
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

func validateProductionDeceasedText(field string, value *string) (*string, error) {
	normalized := normalizeDeceasedText(value)
	if normalized != nil && utf8.RuneCountInString(*normalized) > maxDeceasedTextLength {
		return nil, fmt.Errorf("%s excede %d caracteres", field, maxDeceasedTextLength)
	}
	return normalized, nil
}

func deceasedResponseFromModel(model Deceased) deceasedResponse {
	return deceasedResponse{
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

func writeProductionDeceasedServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, audit.ErrPolicyUnavailable):
		writeProductionError(w, r, http.StatusServiceUnavailable, "PRIVACY_POLICY_UNAVAILABLE", "política de privacidade do tenant indisponível")
	case errors.Is(err, ErrDeceasedPersonNotFound):
		writeProductionError(w, r, http.StatusNotFound, "NOT_FOUND", "pessoa não encontrada neste tenant")
	case errors.Is(err, ErrDeceasedPersonNotApplicable):
		writeProductionError(w, r, http.StatusConflict, "PARTY_KIND_INVALID", "pessoa jurídica não pode ser cadastrada como falecida")
	case errors.Is(err, ErrDeceasedAlreadyExists):
		writeProductionError(w, r, http.StatusConflict, "DECEASED_ALREADY_EXISTS", "pessoa já cadastrada como falecida")
	case errors.Is(err, ErrDeceasedNotFound):
		writeProductionError(w, r, http.StatusNotFound, "NOT_FOUND", "falecido não encontrado")
	default:
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "não foi possível processar o falecido")
	}
}
