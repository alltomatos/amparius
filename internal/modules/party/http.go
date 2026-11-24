package party

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

const (
	developmentTenantHeader = "X-Amparius-Development-Tenant-ID"
	maxDevelopmentBodyBytes = 8 << 10
	maxPartyNameLength      = 200
	maxPartyDocumentLength  = 64
)

// ErrInvalidPartyRequest classifica cargas de cadastro rejeitadas antes do
// repositório.
var ErrInvalidPartyRequest = errors.New("cadastro de party inválido")

// ErrInvalidDevelopmentTenant classifica o cabeçalho de tenant dev-only.
var ErrInvalidDevelopmentTenant = errors.New("tenant de desenvolvimento inválido")

// Creator é o contrato mínimo de escrita desta fatia.
type Creator interface {
	Create(context.Context, Party) (Party, error)
}

// Getter é o contrato mínimo de leitura desta fatia.
type Getter interface {
	GetByID(context.Context, uuid.UUID) (Party, error)
}

// Updater é o contrato de alteração do nome social desta fatia.
type Updater interface {
	UpdateSocialName(context.Context, uuid.UUID, *string) (Party, error)
}

// Service agrega os contratos usados pelo handler de desenvolvimento.
type Service interface {
	Creator
	Getter
	Updater
}

type developmentPartyPayload struct {
	Kind       *string `json:"kind"`
	Name       *string `json:"name"`
	Document   *string `json:"document"`
	Status     *string `json:"status"`
	SocialName *string `json:"social_name"`
}

// developmentPartyResponse mantém name (nome civil, compatível) e adiciona
// civil_name, social_name e display_name do ADR 0013 sem quebrar consumidores.
type developmentPartyResponse struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	CivilName   string  `json:"civil_name"`
	SocialName  *string `json:"social_name"`
	DisplayName string  `json:"display_name"`
	Document    string  `json:"document"`
	Status      string  `json:"status"`
}

// NewDevelopmentHTTPHandler cria os endpoints explícitos de desenvolvimento:
//
//	POST  /_dev/parties        cria uma party com kind=person e social_name opcional
//	GET   /_dev/parties/{id}   consulta a party no tenant do cabeçalho
//	PATCH /_dev/parties/{id}   altera somente o social_name da pessoa
//
// O tenant vem exclusivamente do cabeçalho X-Amparius-Development-Tenant-ID,
// que precisa ser um UUID válido; tenant_id no corpo e campos desconhecidos são
// rejeitados. name permanece o nome civil (também exposto como civil_name);
// social_name vazio ou nulo é normalizado para ausência e display_name segue o
// ADR 0013. O PATCH só aceita social_name e recusa pessoa jurídica.
// Não há autenticação de produção neste handler, portanto ele só deve ser
// registrado quando APP_ENV=development e ENABLE_DEV_ONBOARDING=true.
func NewDevelopmentHTTPHandler(service Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")

		tenantID, err := developmentTenantID(r)
		if err != nil {
			writeDevelopmentPartyError(w, http.StatusBadRequest, "cabeçalho "+developmentTenantHeader+" ausente ou inválido")
			return
		}

		if idText := r.PathValue("id"); idText != "" {
			switch r.Method {
			case http.MethodGet:
				handleDevelopmentPartyGet(w, r, service, tenantID, idText)
			case http.MethodPatch:
				handleDevelopmentPartyPatch(w, r, service, tenantID, idText)
			default:
				w.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch)
				writeDevelopmentPartyError(w, http.StatusMethodNotAllowed, "método não permitido")
			}
			return
		}
		handleDevelopmentPartyCreate(w, r, service, tenantID)
	})
}

func handleDevelopmentPartyCreate(w http.ResponseWriter, r *http.Request, service Service, tenantID uuid.UUID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeDevelopmentPartyError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	payload, err := decodeDevelopmentPartyBody(w, r)
	if err != nil {
		writeDevelopmentPartyError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	party, err := partyFromDevelopmentPayload(payload)
	if err != nil {
		writeDevelopmentPartyError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentPartyError(w, http.StatusServiceUnavailable, "cadastro de party indisponível")
		return
	}

	created, err := service.Create(db.WithTenant(r.Context(), tenantID), party)
	if err != nil {
		switch {
		case errors.Is(err, ErrTenantNotFound):
			writeDevelopmentPartyError(w, http.StatusNotFound, "tenant não encontrado")
		case errors.Is(err, db.ErrTenantRequired):
			writeDevelopmentPartyError(w, http.StatusBadRequest, "tenant ausente no contexto")
		default:
			writeDevelopmentPartyError(w, http.StatusInternalServerError, "não foi possível cadastrar a party")
		}
		return
	}

	w.Header().Set("Location", "/_dev/parties/"+created.ID.String())
	writeDevelopmentPartyJSON(w, http.StatusCreated, map[string]any{
		"status": "ok",
		"party":  developmentPartyFromModel(created),
	})
}

func handleDevelopmentPartyGet(w http.ResponseWriter, r *http.Request, service Service, tenantID uuid.UUID, idText string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeDevelopmentPartyError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(idText))
	if err != nil || id == uuid.Nil {
		writeDevelopmentPartyError(w, http.StatusBadRequest, "id da party inválido")
		return
	}
	if service == nil {
		writeDevelopmentPartyError(w, http.StatusServiceUnavailable, "consulta de party indisponível")
		return
	}

	found, err := service.GetByID(db.WithTenant(r.Context(), tenantID), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeDevelopmentPartyError(w, http.StatusNotFound, "party não encontrada")
		case errors.Is(err, db.ErrTenantRequired):
			writeDevelopmentPartyError(w, http.StatusBadRequest, "tenant ausente no contexto")
		default:
			writeDevelopmentPartyError(w, http.StatusInternalServerError, "não foi possível consultar a party")
		}
		return
	}

	writeDevelopmentPartyJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"party":  developmentPartyFromModel(found),
	})
}

func handleDevelopmentPartyPatch(w http.ResponseWriter, r *http.Request, service Service, tenantID uuid.UUID, idText string) {
	id, err := uuid.Parse(strings.TrimSpace(idText))
	if err != nil || id == uuid.Nil {
		writeDevelopmentPartyError(w, http.StatusBadRequest, "id da party inválido")
		return
	}
	if service == nil {
		writeDevelopmentPartyError(w, http.StatusServiceUnavailable, "alteração de party indisponível")
		return
	}
	socialName, err := decodeDevelopmentSocialNamePayload(w, r)
	if err != nil {
		if errors.Is(err, ErrInvalidPartyRequest) {
			writeDevelopmentPartyError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeDevelopmentPartyError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	updated, err := service.UpdateSocialName(db.WithTenant(r.Context(), tenantID), id, socialName)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeDevelopmentPartyError(w, http.StatusNotFound, "party não encontrada")
		case errors.Is(err, ErrSocialNameNotApplicable):
			writeDevelopmentPartyError(w, http.StatusConflict, "nome social não se aplica a pessoa jurídica")
		case errors.Is(err, db.ErrTenantRequired):
			writeDevelopmentPartyError(w, http.StatusBadRequest, "tenant ausente no contexto")
		default:
			writeDevelopmentPartyError(w, http.StatusInternalServerError, "não foi possível alterar o nome social")
		}
		return
	}

	writeDevelopmentPartyJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"party":  developmentPartyFromModel(updated),
	})
}

func developmentTenantID(r *http.Request) (uuid.UUID, error) {
	raw := strings.TrimSpace(r.Header.Get(developmentTenantHeader))
	if raw == "" {
		return uuid.Nil, ErrInvalidDevelopmentTenant
	}
	tenantID, err := uuid.Parse(raw)
	if err != nil || tenantID == uuid.Nil {
		return uuid.Nil, ErrInvalidDevelopmentTenant
	}
	return tenantID, nil
}

// decodeDevelopmentJSONObject lê exatamente um objeto JSON do corpo e devolve
// os bytes normalizados para uma segunda decodificação estrita.
func decodeDevelopmentJSONObject(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDevelopmentBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("o corpo deve ser um objeto JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("corpo contém mais de um JSON")
		}
		return nil, err
	}
	return trimmed, nil
}

func decodeDevelopmentPartyBody(w http.ResponseWriter, r *http.Request) (developmentPartyPayload, error) {
	trimmed, err := decodeDevelopmentJSONObject(w, r)
	if err != nil {
		return developmentPartyPayload{}, err
	}
	var payload developmentPartyPayload
	payloadDecoder := json.NewDecoder(bytes.NewReader(trimmed))
	payloadDecoder.DisallowUnknownFields()
	if err := payloadDecoder.Decode(&payload); err != nil {
		return developmentPartyPayload{}, err
	}
	return payload, nil
}

// decodeDevelopmentSocialNamePayload exige o campo social_name (texto ou null)
// e rejeita qualquer outro campo. Devolve nil para null, vazio ou só espaços.
func decodeDevelopmentSocialNamePayload(w http.ResponseWriter, r *http.Request) (*string, error) {
	trimmed, err := decodeDevelopmentJSONObject(w, r)
	if err != nil {
		return nil, err
	}
	var payload struct {
		SocialName json.RawMessage `json:"social_name"`
	}
	payloadDecoder := json.NewDecoder(bytes.NewReader(trimmed))
	payloadDecoder.DisallowUnknownFields()
	if err := payloadDecoder.Decode(&payload); err != nil {
		return nil, err
	}
	if payload.SocialName == nil {
		return nil, fmt.Errorf("%w: social_name é obrigatório no PATCH", ErrInvalidPartyRequest)
	}
	var value *string
	if err := json.Unmarshal(payload.SocialName, &value); err != nil {
		return nil, fmt.Errorf("%w: social_name deve ser texto ou null", ErrInvalidPartyRequest)
	}
	normalized := normalizeSocialName(KindPerson, value)
	if normalized != nil && utf8.RuneCountInString(*normalized) > maxPartyNameLength {
		return nil, fmt.Errorf("%w: social_name excede %d caracteres", ErrInvalidPartyRequest, maxPartyNameLength)
	}
	return normalized, nil
}

func partyFromDevelopmentPayload(payload developmentPartyPayload) (Party, error) {
	kind := KindPerson
	if payload.Kind != nil {
		kind = strings.TrimSpace(*payload.Kind)
		if kind != KindPerson {
			return Party{}, fmt.Errorf("%w: somente kind=person nesta fatia", ErrInvalidPartyRequest)
		}
	}

	name := ""
	if payload.Name != nil {
		name = strings.TrimSpace(*payload.Name)
	}
	if name == "" {
		return Party{}, fmt.Errorf("%w: name é obrigatório", ErrInvalidPartyRequest)
	}
	if utf8.RuneCountInString(name) > maxPartyNameLength {
		return Party{}, fmt.Errorf("%w: name excede %d caracteres", ErrInvalidPartyRequest, maxPartyNameLength)
	}

	document := ""
	if payload.Document != nil {
		document = strings.TrimSpace(*payload.Document)
	}
	if utf8.RuneCountInString(document) > maxPartyDocumentLength {
		return Party{}, fmt.Errorf("%w: document excede %d caracteres", ErrInvalidPartyRequest, maxPartyDocumentLength)
	}

	status := StatusActive
	if payload.Status != nil {
		status = strings.TrimSpace(*payload.Status)
		if status != StatusActive && status != StatusInactive {
			return Party{}, fmt.Errorf("%w: status deve ser active ou inactive", ErrInvalidPartyRequest)
		}
	}

	socialName := normalizeSocialName(kind, payload.SocialName)
	if socialName != nil && utf8.RuneCountInString(*socialName) > maxPartyNameLength {
		return Party{}, fmt.Errorf("%w: social_name excede %d caracteres", ErrInvalidPartyRequest, maxPartyNameLength)
	}

	return Party{Kind: kind, Name: name, Document: document, Status: status, SocialName: socialName}, nil
}

func developmentPartyFromModel(model Party) developmentPartyResponse {
	return developmentPartyResponse{
		ID:          model.ID.String(),
		Kind:        model.Kind,
		Name:        model.Name,
		CivilName:   model.CivilName(),
		SocialName:  model.SocialName,
		DisplayName: model.DisplayName(),
		Document:    model.Document,
		Status:      model.Status,
	}
}

func writeDevelopmentPartyError(w http.ResponseWriter, status int, message string) {
	writeDevelopmentPartyJSON(w, status, map[string]string{"status": "error", "error": message})
}

func writeDevelopmentPartyJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
