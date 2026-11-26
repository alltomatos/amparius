package party

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/audit"
	"github.com/google/uuid"
)

// ProductionFamilyLinkService é o contrato mínimo exigido pelo contrato
// autenticado /v1/deceased/{deceasedId}/family-links e /v1/family-links/{id}.
// CreateFamilyLinkWithConsent é o único caminho de escrita: grava o vínculo e
// o consentimento/auditoria exigidos pelo ADR 0042 na mesma transação.
type ProductionFamilyLinkService interface {
	CreateFamilyLinkWithConsent(ctx context.Context, input FamilyLinkCreate, evidenceRef uuid.UUID, policy audit.PolicyProvider) (FamilyLink, error)
	GetFamilyLinkByID(ctx context.Context, id uuid.UUID) (FamilyLink, error)
}

// ProductionFamilyLinkHandler serves authenticated family links with tenant privacy policy.
type ProductionFamilyLinkHandler struct {
	service ProductionFamilyLinkService
	policy  audit.PolicyProvider
}

// NewProductionFamilyLinkHTTPHandler cria o handler autenticado do contrato
// de vínculo familiar. policy pode ser nil: nesse caso Create responde sempre
// 503 PRIVACY_POLICY_UNAVAILABLE sem tocar o repositório.
func NewProductionFamilyLinkHTTPHandler(service ProductionFamilyLinkService, policy audit.PolicyProvider) *ProductionFamilyLinkHandler {
	return &ProductionFamilyLinkHandler{service: service, policy: policy}
}

type createFamilyLinkRequest struct {
	PersonID           *string `json:"personId"`
	Relation           *string `json:"relation"`
	ConsentEvidenceRef *string `json:"consentEvidenceRef"`
}

// familyLinkPersonResponse carrega só a identidade e os nomes resolvidos da
// party de origem; nomes nunca são copiados para party.family_link.
type familyLinkPersonResponse struct {
	ID          string  `json:"id"`
	CivilName   string  `json:"civilName"`
	SocialName  *string `json:"socialName"`
	DisplayName string  `json:"displayName"`
}

type familyLinkResponse struct {
	ID       string                   `json:"id"`
	Relation string                   `json:"relation"`
	Deceased familyLinkPersonResponse `json:"deceased"`
	Person   familyLinkPersonResponse `json:"person"`
}

// Create atende POST /v1/deceased/{deceasedId}/family-links: vincula um
// familiar (personId) ao falecido do path, exigindo consentEvidenceRef
// opaco. O corpo nunca aceita purposeCode, legalBasisCode ou retentionUntil —
// esses três valores só vêm da política resolvida pelo DPO (ADR 0042 §5).
func (h *ProductionFamilyLinkHandler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.policy == nil {
		writeProductionFamilyLinkServiceError(w, r, audit.ErrPolicyUnavailable)
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "vínculo familiar indisponível")
		return
	}

	deceasedID, err := uuid.Parse(strings.TrimSpace(r.PathValue("deceasedId")))
	if err != nil || deceasedID == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "deceasedId inválido")
		return
	}

	raw, err := decodeJSONObject(w, r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}
	var request createFamilyLinkRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}

	input, evidenceRef, err := familyLinkCreateFromRequest(deceasedID, request)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	created, err := h.service.CreateFamilyLinkWithConsent(r.Context(), input, evidenceRef, h.policy)
	if err != nil {
		writeProductionFamilyLinkServiceError(w, r, err)
		return
	}
	writeProductionJSON(w, http.StatusCreated, familyLinkResponseFromModel(created))
}

// Get atende GET /v1/family-links/{id}: consulta por id no tenant do
// principal. Uma referência de outro tenant é tratada como não encontrada.
func (h *ProductionFamilyLinkHandler) Get(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "consulta de vínculo familiar indisponível")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "id do vínculo familiar inválido")
		return
	}

	found, err := h.service.GetFamilyLinkByID(r.Context(), id)
	if err != nil {
		writeProductionFamilyLinkServiceError(w, r, err)
		return
	}
	writeProductionJSON(w, http.StatusOK, familyLinkResponseFromModel(found))
}

func familyLinkCreateFromRequest(deceasedID uuid.UUID, request createFamilyLinkRequest) (FamilyLinkCreate, uuid.UUID, error) {
	personID, err := requiredUUIDField("personId", request.PersonID)
	if err != nil {
		return FamilyLinkCreate{}, uuid.Nil, err
	}
	if request.Relation == nil || strings.TrimSpace(*request.Relation) == "" {
		return FamilyLinkCreate{}, uuid.Nil, errors.New("relation é obrigatório")
	}
	relation := strings.TrimSpace(*request.Relation)
	if len([]rune(relation)) > maxFamilyRelationLength {
		return FamilyLinkCreate{}, uuid.Nil, errors.New("relation excede limite de caracteres")
	}
	evidenceRef, err := requiredUUIDField("consentEvidenceRef", request.ConsentEvidenceRef)
	if err != nil {
		return FamilyLinkCreate{}, uuid.Nil, err
	}
	return FamilyLinkCreate{
		DeceasedID: deceasedID,
		PersonID:   personID,
		Relation:   relation,
	}, evidenceRef, nil
}

func familyLinkResponseFromModel(model FamilyLink) familyLinkResponse {
	return familyLinkResponse{
		ID:       model.ID.String(),
		Relation: model.Relation,
		Deceased: familyLinkPersonResponseFromParty(model.Deceased),
		Person:   familyLinkPersonResponseFromParty(model.Person),
	}
}

func familyLinkPersonResponseFromParty(model Party) familyLinkPersonResponse {
	return familyLinkPersonResponse{
		ID:          model.ID.String(),
		CivilName:   model.CivilName(),
		SocialName:  model.SocialName,
		DisplayName: model.DisplayName(),
	}
}

func writeProductionFamilyLinkServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, audit.ErrPolicyUnavailable):
		writeProductionError(w, r, http.StatusServiceUnavailable, "PRIVACY_POLICY_UNAVAILABLE", "política de privacidade do tenant indisponível")
	case errors.Is(err, ErrFamilyLinkEvidenceRequired):
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "consentEvidenceRef é obrigatório")
	case errors.Is(err, ErrFamilyLinkDeceasedNotFound):
		writeProductionError(w, r, http.StatusNotFound, "NOT_FOUND", "falecido não encontrado neste tenant")
	case errors.Is(err, ErrFamilyLinkPersonNotFound):
		writeProductionError(w, r, http.StatusNotFound, "NOT_FOUND", "familiar não encontrado neste tenant")
	case errors.Is(err, ErrFamilyLinkPersonNotApplicable):
		writeProductionError(w, r, http.StatusConflict, "PARTY_KIND_INVALID", "pessoa jurídica não pode ser vinculada como familiar")
	case errors.Is(err, ErrFamilyLinkSelf):
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", ErrFamilyLinkSelf.Error())
	case errors.Is(err, ErrFamilyLinkRelationInvalid):
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "relação familiar inválida")
	case errors.Is(err, ErrFamilyLinkAlreadyExists):
		writeProductionError(w, r, http.StatusConflict, "FAMILY_LINK_ALREADY_EXISTS", "familiar já vinculado a este falecido")
	case errors.Is(err, ErrFamilyLinkNotFound):
		writeProductionError(w, r, http.StatusNotFound, "NOT_FOUND", "vínculo familiar não encontrado")
	default:
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "não foi possível processar o vínculo familiar")
	}
}
