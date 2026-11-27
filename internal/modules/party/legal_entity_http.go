package party

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

// DevelopmentLegalEntityService é o contrato dev-only de pessoa jurídica,
// representante, contatos e endereços. Reusa exatamente a mesma validação e
// repositório da fatia autenticada /v1, mas nunca grava auditoria nem exige
// política de privacidade (fixture sintética de prévia de homologação).
type DevelopmentLegalEntityService interface {
	CreateLegalEntity(ctx context.Context, input LegalEntityCreate) (LegalEntity, error)
	SearchLegalEntities(ctx context.Context, query string, limit int) ([]LegalEntity, error)
	GetLegalEntityAggregate(ctx context.Context, id uuid.UUID) (LegalEntityAggregate, error)
	CreateContact(ctx context.Context, input ContactCreate) (Contact, error)
	CreateAddress(ctx context.Context, input AddressCreate) (Address, error)
	CreateRepresentative(ctx context.Context, input RepresentativeCreate) (Representative, error)
}

type developmentLegalEntityPayload struct {
	LegalName             *string `json:"legalName"`
	CNPJ                  *string `json:"cnpj"`
	TradeName             *string `json:"tradeName"`
	StateRegistration     *string `json:"stateRegistration"`
	MunicipalRegistration *string `json:"municipalRegistration"`
}

type developmentContactPayload struct {
	Kind    *string `json:"kind"`
	Value   *string `json:"value"`
	Primary *bool   `json:"primary"`
}

type developmentAddressPayload struct {
	Street  *string `json:"street"`
	Number  *string `json:"number"`
	City    *string `json:"city"`
	State   *string `json:"state"`
	Zip     *string `json:"zip"`
	Primary *bool   `json:"primary"`
}

type developmentRepresentativePayload struct {
	PersonID *string `json:"personId"`
	Role     *string `json:"role"`
}

// NewDevelopmentLegalEntityHTTPHandler cria os endpoints explícitos de
// desenvolvimento usados pela prévia de homologação (web staging):
//
//	POST /_dev/legal-entities                              cria pessoa jurídica
//	GET  /_dev/legal-entities                              busca pessoas jurídicas
//	GET  /_dev/legal-entities/{id}                         agregado da pessoa jurídica
//	POST /_dev/legal-entities/{id}/representatives         vincula representante
//	POST /_dev/parties/{id}/contacts                       cria contato
//	POST /_dev/parties/{id}/addresses                      cria endereço
//
// O tenant vem exclusivamente de X-Amparius-Development-Tenant-ID; tenant_id,
// companyId, kind e status no corpo são rejeitados como campos desconhecidos.
// Fixture sintética: sem autenticação de produção, sem auditoria e sem
// política de privacidade. Só deve ser registrado quando APP_ENV=development e
// ENABLE_DEV_ONBOARDING=true.
func NewDevelopmentLegalEntityHTTPHandler(service DevelopmentLegalEntityService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")

		tenantID, err := developmentTenantID(r)
		if err != nil {
			writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "cabeçalho "+developmentTenantHeader+" ausente ou inválido")
			return
		}

		switch {
		case strings.HasSuffix(r.URL.Path, "/representatives"):
			handleDevelopmentRepresentativeCreate(w, r, service, tenantID)
		case strings.HasSuffix(r.URL.Path, "/contacts"):
			handleDevelopmentContactCreate(w, r, service, tenantID)
		case strings.HasSuffix(r.URL.Path, "/addresses"):
			handleDevelopmentAddressCreate(w, r, service, tenantID)
		case r.PathValue("id") != "":
			handleDevelopmentLegalEntityGet(w, r, service, tenantID)
		case r.Method == http.MethodGet:
			handleDevelopmentLegalEntitySearch(w, r, service, tenantID)
		default:
			handleDevelopmentLegalEntityCreate(w, r, service, tenantID)
		}
	})
}

func handleDevelopmentLegalEntityCreate(w http.ResponseWriter, r *http.Request, service DevelopmentLegalEntityService, tenantID uuid.UUID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeDevelopmentLegalEntityError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	trimmed, err := decodeJSONObject(w, r)
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	var payload developmentLegalEntityPayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	input, err := legalEntityCreateFromRequest(createLegalEntityRequest{
		LegalName: payload.LegalName, CNPJ: payload.CNPJ, TradeName: payload.TradeName,
		StateRegistration: payload.StateRegistration, MunicipalRegistration: payload.MunicipalRegistration,
	})
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentLegalEntityError(w, http.StatusServiceUnavailable, "cadastro de pessoa jurídica indisponível")
		return
	}

	created, err := service.CreateLegalEntity(db.WithTenant(r.Context(), tenantID), input)
	if err != nil {
		writeDevelopmentLegalEntityServiceError(w, err)
		return
	}
	w.Header().Set("Location", "/_dev/legal-entities/"+created.ID.String())
	writeDevelopmentLegalEntityJSON(w, http.StatusCreated, legalEntityResponseFromModel(created))
}

func handleDevelopmentLegalEntitySearch(w http.ResponseWriter, r *http.Request, service DevelopmentLegalEntityService, tenantID uuid.UUID) {
	query, limit, err := legalEntitySearchParams(r)
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentLegalEntityError(w, http.StatusServiceUnavailable, "consulta de pessoa jurídica indisponível")
		return
	}
	found, err := service.SearchLegalEntities(db.WithTenant(r.Context(), tenantID), query, limit)
	if errors.Is(err, ErrInvalidSearch) {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "busca de pessoa jurídica inválida")
		return
	}
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusServiceUnavailable, "não foi possível buscar pessoas jurídicas")
		return
	}
	writeDevelopmentLegalEntityJSON(w, http.StatusOK, legalEntitySearchResponse(found))
}

func handleDevelopmentLegalEntityGet(w http.ResponseWriter, r *http.Request, service DevelopmentLegalEntityService, tenantID uuid.UUID) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeDevelopmentLegalEntityError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || id == uuid.Nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "id da pessoa jurídica inválido")
		return
	}
	if service == nil {
		writeDevelopmentLegalEntityError(w, http.StatusServiceUnavailable, "consulta de pessoa jurídica indisponível")
		return
	}

	found, err := service.GetLegalEntityAggregate(db.WithTenant(r.Context(), tenantID), id)
	if err != nil {
		writeDevelopmentLegalEntityServiceError(w, err)
		return
	}
	writeDevelopmentLegalEntityJSON(w, http.StatusOK, legalEntityAggregateResponseFromModel(found))
}

func handleDevelopmentRepresentativeCreate(w http.ResponseWriter, r *http.Request, service DevelopmentLegalEntityService, tenantID uuid.UUID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeDevelopmentLegalEntityError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	companyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || companyID == uuid.Nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "id da pessoa jurídica inválido")
		return
	}
	trimmed, err := decodeJSONObject(w, r)
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	var payload developmentRepresentativePayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	input, err := representativeCreateFromRequest(companyID, createRepresentativeRequest{PersonID: payload.PersonID, Role: payload.Role})
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentLegalEntityError(w, http.StatusServiceUnavailable, "vínculo de representante indisponível")
		return
	}

	created, err := service.CreateRepresentative(db.WithTenant(r.Context(), tenantID), input)
	if err != nil {
		writeDevelopmentLegalEntityServiceError(w, err)
		return
	}
	writeDevelopmentLegalEntityJSON(w, http.StatusCreated, representativeResponseFromModel(created))
}

func handleDevelopmentContactCreate(w http.ResponseWriter, r *http.Request, service DevelopmentLegalEntityService, tenantID uuid.UUID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeDevelopmentLegalEntityError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	partyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || partyID == uuid.Nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "id da party inválido")
		return
	}
	trimmed, err := decodeJSONObject(w, r)
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	var payload developmentContactPayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	input, err := contactCreateFromRequest(partyID, createContactRequest{Kind: payload.Kind, Value: payload.Value, Primary: payload.Primary})
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentLegalEntityError(w, http.StatusServiceUnavailable, "cadastro de contato indisponível")
		return
	}

	created, err := service.CreateContact(db.WithTenant(r.Context(), tenantID), input)
	if err != nil {
		writeDevelopmentLegalEntityServiceError(w, err)
		return
	}
	writeDevelopmentLegalEntityJSON(w, http.StatusCreated, contactResponseFromModel(created))
}

func handleDevelopmentAddressCreate(w http.ResponseWriter, r *http.Request, service DevelopmentLegalEntityService, tenantID uuid.UUID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeDevelopmentLegalEntityError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	partyID, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil || partyID == uuid.Nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "id da party inválido")
		return
	}
	trimmed, err := decodeJSONObject(w, r)
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	var payload developmentAddressPayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	input, err := addressCreateFromRequest(partyID, createAddressRequest{
		Street: payload.Street, Number: payload.Number, City: payload.City,
		State: payload.State, Zip: payload.Zip, Primary: payload.Primary,
	})
	if err != nil {
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentLegalEntityError(w, http.StatusServiceUnavailable, "cadastro de endereço indisponível")
		return
	}

	created, err := service.CreateAddress(db.WithTenant(r.Context(), tenantID), input)
	if err != nil {
		writeDevelopmentLegalEntityServiceError(w, err)
		return
	}
	writeDevelopmentLegalEntityJSON(w, http.StatusCreated, addressResponseFromModel(created))
}

func writeDevelopmentLegalEntityServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidCNPJ):
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "CNPJ inválido")
	case errors.Is(err, ErrLegalEntityAlreadyExists):
		writeDevelopmentLegalEntityError(w, http.StatusConflict, "CNPJ já cadastrado neste tenant")
	case errors.Is(err, ErrLegalEntityNotFound):
		writeDevelopmentLegalEntityError(w, http.StatusNotFound, "pessoa jurídica não encontrada")
	case errors.Is(err, ErrRepresentativePersonNotFound):
		writeDevelopmentLegalEntityError(w, http.StatusNotFound, "pessoa não encontrada neste tenant")
	case errors.Is(err, ErrPartyKindInvalid):
		writeDevelopmentLegalEntityError(w, http.StatusConflict, "pessoa jurídica não pode ser representante")
	case errors.Is(err, ErrRepresentativeRoleInvalid):
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "papel de representante inválido")
	case errors.Is(err, ErrRepresentativeAlreadyExists):
		writeDevelopmentLegalEntityError(w, http.StatusConflict, "representante já vinculado a esta pessoa jurídica com este papel")
	case errors.Is(err, ErrNotFound):
		writeDevelopmentLegalEntityError(w, http.StatusNotFound, "party não encontrada neste tenant")
	case errors.Is(err, db.ErrTenantRequired):
		writeDevelopmentLegalEntityError(w, http.StatusBadRequest, "tenant ausente no contexto")
	default:
		writeDevelopmentLegalEntityError(w, http.StatusInternalServerError, "não foi possível processar a solicitação")
	}
}

func writeDevelopmentLegalEntityError(w http.ResponseWriter, status int, message string) {
	writeDevelopmentLegalEntityJSON(w, status, map[string]string{"status": "error", "error": message})
}

func writeDevelopmentLegalEntityJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
