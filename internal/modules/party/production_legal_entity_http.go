package party

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alltomatos/amparius/internal/platform/audit"
	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/google/uuid"
)

const (
	maxLegalEntityTextLength    = 200
	maxContactValueLength       = 254
	maxAddressTextLength        = 120
	maxRepresentativeRoleLength = 40
	minPhoneDigits              = 10
	maxPhoneDigits              = 13
)

var contactEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// ProductionLegalEntityService é o contrato autenticado de cadastro
// operacional de pessoa jurídica, contatos, endereços e representante
// (issue #78 / E2E-01B7). O tenant e a empresa vêm exclusivamente do
// principal autenticado; o corpo nunca aceita tenantId, companyId, kind nem
// status.
type ProductionLegalEntityService interface {
	CreateLegalEntity(ctx context.Context, input LegalEntityCreate) (LegalEntity, error)
	SearchLegalEntities(ctx context.Context, query string, limit int) ([]LegalEntity, error)
	GetLegalEntityAggregate(ctx context.Context, id uuid.UUID) (LegalEntityAggregate, error)
	CreateRepresentativeWithAudit(ctx context.Context, input RepresentativeCreate, policy audit.PolicyProvider) (Representative, error)
	CreateContactWithAudit(ctx context.Context, input ContactCreate, policy audit.PolicyProvider) (Contact, error)
	ListContacts(ctx context.Context, partyID uuid.UUID) ([]Contact, error)
	CreateAddressWithAudit(ctx context.Context, input AddressCreate, policy audit.PolicyProvider) (Address, error)
	ListAddresses(ctx context.Context, partyID uuid.UUID) ([]Address, error)
}

// ProductionLegalEntityHandler serve o contrato autenticado /v1/legal-entities
// e /v1/parties/{partyId}/contacts|addresses. policy pode ser nil: nesse caso
// os três POSTs de dados pessoais (contatos, endereços, representante)
// respondem sempre 503 PRIVACY_POLICY_UNAVAILABLE sem tocar o repositório
// (fail-closed, ADR 0042 §4). A criação da pessoa jurídica em si não é
// auditada com política, igual à pessoa física da issue #72.
type ProductionLegalEntityHandler struct {
	service ProductionLegalEntityService
	policy  audit.PolicyProvider
}

// NewProductionLegalEntityHTTPHandler cria o handler autenticado do contrato
// de pessoa jurídica, contatos, endereços e representante.
func NewProductionLegalEntityHTTPHandler(service ProductionLegalEntityService, policy audit.PolicyProvider) *ProductionLegalEntityHandler {
	return &ProductionLegalEntityHandler{service: service, policy: policy}
}

// LegalEntityAuthenticatedRoutes monta as rotas /v1/legal-entities e
// /v1/parties/{partyId}/contacts|addresses com o escopo de tenant/empresa
// composto pelo middleware, conforme as permissões de party (issue #78).
func LegalEntityAuthenticatedRoutes(service ProductionLegalEntityService, policy audit.PolicyProvider) []httpserver.AuthenticatedRoute {
	h := NewProductionLegalEntityHTTPHandler(service, policy)
	return []httpserver.AuthenticatedRoute{
		{Pattern: "POST /v1/legal-entities", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(h.Create)},
		{Pattern: "GET /v1/legal-entities", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(h.Search)},
		{Pattern: "GET /v1/legal-entities/{id}", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(h.Get)},
		{Pattern: "POST /v1/legal-entities/{id}/representatives", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(h.CreateRepresentative)},
		{Pattern: "POST /v1/parties/{partyId}/contacts", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(h.CreateContact)},
		{Pattern: "GET /v1/parties/{partyId}/contacts", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(h.ListContacts)},
		{Pattern: "POST /v1/parties/{partyId}/addresses", Permission: platformauth.PermissionPartyWrite, Handler: http.HandlerFunc(h.CreateAddress)},
		{Pattern: "GET /v1/parties/{partyId}/addresses", Permission: platformauth.PermissionPartyRead, Handler: http.HandlerFunc(h.ListAddresses)},
	}
}

type createLegalEntityRequest struct {
	LegalName             *string `json:"legalName"`
	CNPJ                  *string `json:"cnpj"`
	TradeName             *string `json:"tradeName"`
	StateRegistration     *string `json:"stateRegistration"`
	MunicipalRegistration *string `json:"municipalRegistration"`
}

type legalEntityResponse struct {
	ID                    string  `json:"id"`
	Kind                  string  `json:"kind"`
	LegalName             string  `json:"legalName"`
	CNPJ                  string  `json:"cnpj"`
	TradeName             *string `json:"tradeName"`
	StateRegistration     *string `json:"stateRegistration"`
	MunicipalRegistration *string `json:"municipalRegistration"`
	Status                string  `json:"status"`
}

type legalEntityAggregateResponse struct {
	legalEntityResponse
	Contacts        []contactResponse        `json:"contacts"`
	Addresses       []addressResponse        `json:"addresses"`
	Representatives []representativeResponse `json:"representatives"`
}

type contactResponse struct {
	ID      string `json:"id"`
	PartyID string `json:"partyId"`
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
}

type addressResponse struct {
	ID      string `json:"id"`
	PartyID string `json:"partyId"`
	Street  string `json:"street"`
	Number  string `json:"number"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
	Primary bool   `json:"primary"`
}

type representativeResponse struct {
	ID          string  `json:"id"`
	CompanyID   string  `json:"companyId"`
	PersonID    string  `json:"personId"`
	Role        string  `json:"role"`
	CivilName   string  `json:"civilName"`
	SocialName  *string `json:"socialName"`
	DisplayName string  `json:"displayName"`
}

type createContactRequest struct {
	Kind    *string `json:"kind"`
	Value   *string `json:"value"`
	Primary *bool   `json:"primary"`
}

type createAddressRequest struct {
	Street  *string `json:"street"`
	Number  *string `json:"number"`
	City    *string `json:"city"`
	State   *string `json:"state"`
	Zip     *string `json:"zip"`
	Primary *bool   `json:"primary"`
}

type createRepresentativeRequest struct {
	PersonID *string `json:"personId"`
	Role     *string `json:"role"`
}

// Create atende POST /v1/legal-entities: cria uma pessoa jurídica no tenant
// do principal autenticado. O corpo não aceita tenantId, companyId, kind nem
// status. CNPJ é obrigatório, aceita numérico e alfanumérico (2026) e é
// normalizado antes de chegar ao repositório.
func (h *ProductionLegalEntityHandler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "cadastro de pessoa jurídica indisponível")
		return
	}

	raw, err := decodeJSONObject(w, r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}
	var request createLegalEntityRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}

	input, err := legalEntityCreateFromRequest(request)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	created, err := h.service.CreateLegalEntity(r.Context(), input)
	if err != nil {
		writeProductionLegalEntityServiceError(w, r, err)
		return
	}
	writeProductionJSON(w, http.StatusCreated, legalEntityResponseFromModel(created))
}

// Search atende GET /v1/legal-entities: busca somente pessoas jurídicas
// (kind=company) por nome, nome fantasia ou documento, no tenant do
// principal autenticado.
func (h *ProductionLegalEntityHandler) Search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "busca de pessoa jurídica indisponível")
		return
	}

	query, limit, err := legalEntitySearchParams(r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	found, err := h.service.SearchLegalEntities(r.Context(), query, limit)
	if err != nil {
		if errors.Is(err, ErrInvalidSearch) {
			writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "busca de pessoa jurídica inválida")
			return
		}
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "não foi possível buscar pessoas jurídicas")
		return
	}
	writeProductionJSON(w, http.StatusOK, legalEntitySearchResponse(found))
}

func legalEntitySearchParams(r *http.Request) (string, int, error) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		return "", 0, errors.New("q é obrigatório")
	}
	limit := defaultProductionSearchLimit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 || parsed > 100 {
			return "", 0, errors.New("limit inválido")
		}
		limit = parsed
	}
	return query, limit, nil
}

func legalEntitySearchResponse(found []LegalEntity) map[string]any {
	items := make([]legalEntityResponse, 0, len(found))
	for _, item := range found {
		items = append(items, legalEntityResponseFromModel(item))
	}
	return map[string]any{"items": items}
}

// Get atende GET /v1/legal-entities/{id}: devolve o agregado da pessoa
// jurídica com contatos, endereços e representantes. Uma referência de
// outro tenant ou inexistente é tratada como não encontrada, sem revelar
// existência.
func (h *ProductionLegalEntityHandler) Get(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "consulta de pessoa jurídica indisponível")
		return
	}

	id, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "id da pessoa jurídica inválido")
		return
	}

	found, err := h.service.GetLegalEntityAggregate(r.Context(), id)
	if err != nil {
		writeProductionLegalEntityServiceError(w, r, err)
		return
	}
	writeProductionJSON(w, http.StatusOK, legalEntityAggregateResponseFromModel(found))
}

// CreateRepresentative atende POST /v1/legal-entities/{id}/representatives:
// vincula uma pessoa física existente no tenant como representante da
// pessoa jurídica do path. Dado pessoal: exige política do DPO resolvida via
// audit.PolicyProvider e grava a auditoria na mesma transação do vínculo.
func (h *ProductionLegalEntityHandler) CreateRepresentative(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.policy == nil {
		writeProductionLegalEntityServiceError(w, r, audit.ErrPolicyUnavailable)
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "vínculo de representante indisponível")
		return
	}

	companyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || companyID == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "id da pessoa jurídica inválido")
		return
	}

	raw, err := decodeJSONObject(w, r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}
	var request createRepresentativeRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}

	input, err := representativeCreateFromRequest(companyID, request)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	created, err := h.service.CreateRepresentativeWithAudit(r.Context(), input, h.policy)
	if err != nil {
		writeProductionLegalEntityServiceError(w, r, err)
		return
	}
	writeProductionJSON(w, http.StatusCreated, representativeResponseFromModel(created))
}

// CreateContact atende POST /v1/parties/{partyId}/contacts: vale para
// qualquer party do tenant, física ou jurídica. Dado pessoal: exige política
// do DPO e grava a auditoria na mesma transação do contato.
func (h *ProductionLegalEntityHandler) CreateContact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.policy == nil {
		writeProductionLegalEntityServiceError(w, r, audit.ErrPolicyUnavailable)
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "cadastro de contato indisponível")
		return
	}

	partyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("partyId")))
	if err != nil || partyID == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "partyId inválido")
		return
	}

	raw, err := decodeJSONObject(w, r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}
	var request createContactRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}

	input, err := contactCreateFromRequest(partyID, request)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	created, err := h.service.CreateContactWithAudit(r.Context(), input, h.policy)
	if err != nil {
		writeProductionLegalEntityServiceError(w, r, err)
		return
	}
	writeProductionJSON(w, http.StatusCreated, contactResponseFromModel(created))
}

// ListContacts atende GET /v1/parties/{partyId}/contacts.
func (h *ProductionLegalEntityHandler) ListContacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "consulta de contatos indisponível")
		return
	}

	partyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("partyId")))
	if err != nil || partyID == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "partyId inválido")
		return
	}

	found, err := h.service.ListContacts(r.Context(), partyID)
	if err != nil {
		writeProductionLegalEntityServiceError(w, r, err)
		return
	}
	items := make([]contactResponse, 0, len(found))
	for _, item := range found {
		items = append(items, contactResponseFromModel(item))
	}
	writeProductionJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CreateAddress atende POST /v1/parties/{partyId}/addresses: vale para
// qualquer party do tenant, física ou jurídica. Dado pessoal: exige política
// do DPO e grava a auditoria na mesma transação do endereço.
func (h *ProductionLegalEntityHandler) CreateAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.policy == nil {
		writeProductionLegalEntityServiceError(w, r, audit.ErrPolicyUnavailable)
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "cadastro de endereço indisponível")
		return
	}

	partyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("partyId")))
	if err != nil || partyID == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "partyId inválido")
		return
	}

	raw, err := decodeJSONObject(w, r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}
	var request createAddressRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}

	input, err := addressCreateFromRequest(partyID, request)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	created, err := h.service.CreateAddressWithAudit(r.Context(), input, h.policy)
	if err != nil {
		writeProductionLegalEntityServiceError(w, r, err)
		return
	}
	writeProductionJSON(w, http.StatusCreated, addressResponseFromModel(created))
}

// ListAddresses atende GET /v1/parties/{partyId}/addresses.
func (h *ProductionLegalEntityHandler) ListAddresses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if !productionScope(w, r) {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "consulta de endereços indisponível")
		return
	}

	partyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("partyId")))
	if err != nil || partyID == uuid.Nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "partyId inválido")
		return
	}

	found, err := h.service.ListAddresses(r.Context(), partyID)
	if err != nil {
		writeProductionLegalEntityServiceError(w, r, err)
		return
	}
	items := make([]addressResponse, 0, len(found))
	for _, item := range found {
		items = append(items, addressResponseFromModel(item))
	}
	writeProductionJSON(w, http.StatusOK, map[string]any{"items": items})
}

func legalEntityCreateFromRequest(request createLegalEntityRequest) (LegalEntityCreate, error) {
	legalName := ""
	if request.LegalName != nil {
		legalName = strings.TrimSpace(*request.LegalName)
	}
	if legalName == "" {
		return LegalEntityCreate{}, fmt.Errorf("%w: legalName é obrigatório", ErrInvalidPartyRequest)
	}
	if utf8.RuneCountInString(legalName) > maxPartyNameLength {
		return LegalEntityCreate{}, fmt.Errorf("%w: legalName excede %d caracteres", ErrInvalidPartyRequest, maxPartyNameLength)
	}

	if request.CNPJ == nil || strings.TrimSpace(*request.CNPJ) == "" {
		return LegalEntityCreate{}, fmt.Errorf("%w: cnpj é obrigatório", ErrInvalidPartyRequest)
	}
	cnpj, err := NormalizeCNPJ(*request.CNPJ)
	if err != nil {
		return LegalEntityCreate{}, fmt.Errorf("%w: cnpj inválido", ErrInvalidPartyRequest)
	}

	tradeName, err := validateOptionalLegalEntityText("tradeName", request.TradeName)
	if err != nil {
		return LegalEntityCreate{}, err
	}
	stateRegistration, err := validateOptionalLegalEntityText("stateRegistration", request.StateRegistration)
	if err != nil {
		return LegalEntityCreate{}, err
	}
	municipalRegistration, err := validateOptionalLegalEntityText("municipalRegistration", request.MunicipalRegistration)
	if err != nil {
		return LegalEntityCreate{}, err
	}

	return LegalEntityCreate{
		LegalName:             legalName,
		CNPJ:                  cnpj,
		TradeName:             tradeName,
		StateRegistration:     stateRegistration,
		MunicipalRegistration: municipalRegistration,
	}, nil
}

func validateOptionalLegalEntityText(field string, value *string) (*string, error) {
	normalized := normalizeDeceasedText(value)
	if normalized != nil && utf8.RuneCountInString(*normalized) > maxLegalEntityTextLength {
		return nil, fmt.Errorf("%w: %s excede %d caracteres", ErrInvalidPartyRequest, field, maxLegalEntityTextLength)
	}
	return normalized, nil
}

func representativeCreateFromRequest(companyID uuid.UUID, request createRepresentativeRequest) (RepresentativeCreate, error) {
	personID, err := requiredUUIDField("personId", request.PersonID)
	if err != nil {
		return RepresentativeCreate{}, err
	}
	if request.Role == nil || strings.TrimSpace(*request.Role) == "" {
		return RepresentativeCreate{}, fmt.Errorf("%w: role é obrigatório", ErrInvalidPartyRequest)
	}
	role := strings.TrimSpace(*request.Role)
	if utf8.RuneCountInString(role) > maxRepresentativeRoleLength {
		return RepresentativeCreate{}, fmt.Errorf("%w: role excede %d caracteres", ErrInvalidPartyRequest, maxRepresentativeRoleLength)
	}
	return RepresentativeCreate{CompanyID: companyID, PersonID: personID, Role: role}, nil
}

func contactCreateFromRequest(partyID uuid.UUID, request createContactRequest) (ContactCreate, error) {
	if request.Kind == nil || strings.TrimSpace(*request.Kind) == "" {
		return ContactCreate{}, fmt.Errorf("%w: kind é obrigatório", ErrInvalidPartyRequest)
	}
	kind := strings.TrimSpace(*request.Kind)
	switch kind {
	case ContactKindPhone, ContactKindEmail, ContactKindWhatsApp, ContactKindOther:
	default:
		return ContactCreate{}, fmt.Errorf("%w: kind deve ser phone, email, whatsapp ou other", ErrInvalidPartyRequest)
	}

	if request.Value == nil || strings.TrimSpace(*request.Value) == "" {
		return ContactCreate{}, fmt.Errorf("%w: value é obrigatório", ErrInvalidPartyRequest)
	}
	value := strings.TrimSpace(*request.Value)
	if utf8.RuneCountInString(value) > maxContactValueLength {
		return ContactCreate{}, fmt.Errorf("%w: value excede %d caracteres", ErrInvalidPartyRequest, maxContactValueLength)
	}
	normalized, err := normalizeContactValue(kind, value)
	if err != nil {
		return ContactCreate{}, err
	}

	primary := false
	if request.Primary != nil {
		primary = *request.Primary
	}
	return ContactCreate{PartyID: partyID, Kind: kind, Value: normalized, Primary: primary}, nil
}

func normalizeContactValue(kind, value string) (string, error) {
	switch kind {
	case ContactKindEmail:
		if !contactEmailPattern.MatchString(value) {
			return "", fmt.Errorf("%w: value não é um e-mail válido", ErrInvalidPartyRequest)
		}
		return value, nil
	case ContactKindPhone, ContactKindWhatsApp:
		digits := digitsOnly(value)
		if len(digits) < minPhoneDigits || len(digits) > maxPhoneDigits {
			return "", fmt.Errorf("%w: value deve ter entre %d e %d dígitos", ErrInvalidPartyRequest, minPhoneDigits, maxPhoneDigits)
		}
		return digits, nil
	default:
		return value, nil
	}
}

func digitsOnly(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func addressCreateFromRequest(partyID uuid.UUID, request createAddressRequest) (AddressCreate, error) {
	street, err := requiredAddressField("street", request.Street)
	if err != nil {
		return AddressCreate{}, err
	}
	number, err := requiredAddressField("number", request.Number)
	if err != nil {
		return AddressCreate{}, err
	}
	city, err := requiredAddressField("city", request.City)
	if err != nil {
		return AddressCreate{}, err
	}
	if request.State == nil || strings.TrimSpace(*request.State) == "" {
		return AddressCreate{}, fmt.Errorf("%w: state é obrigatório", ErrInvalidPartyRequest)
	}
	state := strings.ToUpper(strings.TrimSpace(*request.State))
	if !addressStatePattern.MatchString(state) {
		return AddressCreate{}, fmt.Errorf("%w: state deve ser a UF em 2 letras", ErrInvalidPartyRequest)
	}
	if request.Zip == nil || strings.TrimSpace(*request.Zip) == "" {
		return AddressCreate{}, fmt.Errorf("%w: zip é obrigatório", ErrInvalidPartyRequest)
	}
	zip := digitsOnly(*request.Zip)
	if !addressZipPattern.MatchString(zip) {
		return AddressCreate{}, fmt.Errorf("%w: zip deve ter 8 dígitos", ErrInvalidPartyRequest)
	}

	primary := false
	if request.Primary != nil {
		primary = *request.Primary
	}
	return AddressCreate{
		PartyID: partyID, Street: street, Number: number, City: city,
		State: state, Zip: zip, Primary: primary,
	}, nil
}

var (
	addressStatePattern = regexp.MustCompile(`^[A-Z]{2}$`)
	addressZipPattern   = regexp.MustCompile(`^[0-9]{8}$`)
)

func requiredAddressField(field string, value *string) (string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "", fmt.Errorf("%w: %s é obrigatório", ErrInvalidPartyRequest, field)
	}
	trimmed := strings.TrimSpace(*value)
	if utf8.RuneCountInString(trimmed) > maxAddressTextLength {
		return "", fmt.Errorf("%w: %s excede %d caracteres", ErrInvalidPartyRequest, field, maxAddressTextLength)
	}
	return trimmed, nil
}

func legalEntityResponseFromModel(model LegalEntity) legalEntityResponse {
	return legalEntityResponse{
		ID:                    model.ID.String(),
		Kind:                  KindCompany,
		LegalName:             model.LegalName,
		CNPJ:                  model.CNPJ,
		TradeName:             model.TradeName,
		StateRegistration:     model.StateRegistration,
		MunicipalRegistration: model.MunicipalRegistration,
		Status:                model.Status,
	}
}

func legalEntityAggregateResponseFromModel(model LegalEntityAggregate) legalEntityAggregateResponse {
	contacts := make([]contactResponse, 0, len(model.Contacts))
	for _, item := range model.Contacts {
		contacts = append(contacts, contactResponseFromModel(item))
	}
	addresses := make([]addressResponse, 0, len(model.Addresses))
	for _, item := range model.Addresses {
		addresses = append(addresses, addressResponseFromModel(item))
	}
	representatives := make([]representativeResponse, 0, len(model.Representatives))
	for _, item := range model.Representatives {
		representatives = append(representatives, representativeResponseFromModel(item))
	}
	return legalEntityAggregateResponse{
		legalEntityResponse: legalEntityResponseFromModel(model.LegalEntity),
		Contacts:            contacts,
		Addresses:           addresses,
		Representatives:     representatives,
	}
}

func contactResponseFromModel(model Contact) contactResponse {
	return contactResponse{
		ID: model.ID.String(), PartyID: model.PartyID.String(),
		Kind: model.Kind, Value: model.Value, Primary: model.Primary,
	}
}

func addressResponseFromModel(model Address) addressResponse {
	return addressResponse{
		ID: model.ID.String(), PartyID: model.PartyID.String(),
		Street: model.Street, Number: model.Number, City: model.City,
		State: model.State, Zip: model.Zip, Primary: model.Primary,
	}
}

func representativeResponseFromModel(model Representative) representativeResponse {
	return representativeResponse{
		ID:          model.ID.String(),
		CompanyID:   model.CompanyID.String(),
		PersonID:    model.PersonID.String(),
		Role:        model.Role,
		CivilName:   model.Person.CivilName(),
		SocialName:  model.Person.SocialName,
		DisplayName: model.Person.DisplayName(),
	}
}

func writeProductionLegalEntityServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, audit.ErrPolicyUnavailable):
		writeProductionError(w, r, http.StatusServiceUnavailable, "PRIVACY_POLICY_UNAVAILABLE", "política de privacidade do tenant indisponível")
	case errors.Is(err, ErrInvalidCNPJ):
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "CNPJ inválido")
	case errors.Is(err, ErrLegalEntityAlreadyExists):
		writeProductionError(w, r, http.StatusConflict, "LEGAL_ENTITY_ALREADY_EXISTS", "CNPJ já cadastrado neste tenant")
	case errors.Is(err, ErrLegalEntityNotFound):
		writeProductionError(w, r, http.StatusNotFound, "NOT_FOUND", "pessoa jurídica não encontrada")
	case errors.Is(err, ErrRepresentativePersonNotFound):
		writeProductionError(w, r, http.StatusNotFound, "NOT_FOUND", "pessoa não encontrada neste tenant")
	case errors.Is(err, ErrPartyKindInvalid):
		writeProductionError(w, r, http.StatusConflict, "PARTY_KIND_INVALID", "pessoa jurídica não pode ser representante")
	case errors.Is(err, ErrRepresentativeRoleInvalid):
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "papel de representante inválido")
	case errors.Is(err, ErrRepresentativeAlreadyExists):
		writeProductionError(w, r, http.StatusConflict, "REPRESENTATIVE_ALREADY_EXISTS", "representante já vinculado a esta pessoa jurídica com este papel")
	case errors.Is(err, ErrNotFound):
		writeProductionError(w, r, http.StatusNotFound, "NOT_FOUND", "party não encontrada neste tenant")
	default:
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "não foi possível processar a solicitação")
	}
}
