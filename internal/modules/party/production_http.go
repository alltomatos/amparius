package party

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/observability"
)

const defaultProductionSearchLimit = 25

// PersonSearcher restringe a busca ao kind=person — correção da issue #72,
// que exige que /v1/parties nunca devolva pessoa jurídica mesmo compartilhando
// o termo buscado com uma pessoa física do mesmo tenant.
type PersonSearcher interface {
	SearchPersons(ctx context.Context, query string, limit int) ([]Party, error)
}

// ProductionService é o contrato mínimo exigido pelo contrato autenticado
// /v1/parties: criação e busca de pessoa física no tenant do principal.
type ProductionService interface {
	Creator
	PersonSearcher
}

// ProductionHandler expõe somente criação e busca de pessoa física via
// /v1/parties (PRD RF-003, DER §1, ADR 0024, ADR 0041). O tenant e a empresa
// vêm exclusivamente do principal autenticado composto por auth.Require;
// nenhum cabeçalho ou campo do corpo substitui o contexto, e a rota é
// montada por httpserver.AuthenticatedRoute, que falha fechado (503) sem um
// auth.Gate configurado no bootstrap.
type ProductionHandler struct {
	service ProductionService
}

// NewProductionHTTPHandler cria o handler autenticado do contrato /v1 de
// cadastro de pessoa física.
func NewProductionHTTPHandler(service ProductionService) *ProductionHandler {
	return &ProductionHandler{service: service}
}

type createPersonRequest struct {
	Name       *string `json:"name"`
	Document   *string `json:"document"`
	SocialName *string `json:"social_name"`
}

type personResponse struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	CivilName   string  `json:"civilName"`
	SocialName  *string `json:"socialName"`
	DisplayName string  `json:"displayName"`
	Document    string  `json:"document"`
	Status      string  `json:"status"`
}

// Create atende POST /v1/parties: cria uma pessoa física no tenant do
// principal autenticado. Somente kind=person existe nesta fatia; o corpo não
// aceita tenant_id, company_id, kind nem status.
func (h *ProductionHandler) Create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if _, ok := h.scope(w, r); !ok {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "cadastro de pessoa física indisponível")
		return
	}

	raw, err := decodeJSONObject(w, r)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}
	var request createPersonRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "JSON inválido")
		return
	}

	person, err := personFromCreateRequest(request)
	if err != nil {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	created, err := h.service.Create(r.Context(), person)
	if err != nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "não foi possível cadastrar a pessoa física")
		return
	}

	// Sem cabeçalho Location: esta fatia não expõe GET /v1/parties/{id} (só
	// criação e busca); o id já está no corpo da resposta (ADR 0041 correção).
	writeProductionJSON(w, http.StatusCreated, personResponseFromModel(created))
}

// Search atende GET /v1/parties: busca pessoas físicas por nome civil, nome
// social ou documento no tenant do principal autenticado.
func (h *ProductionHandler) Search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeProductionError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "método não permitido")
		return
	}
	if _, ok := h.scope(w, r); !ok {
		return
	}
	if h.service == nil {
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "busca de pessoa física indisponível")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "q é obrigatório")
		return
	}
	limit := defaultProductionSearchLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 100 {
			writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "limit inválido")
			return
		}
		limit = parsed
	}

	found, err := h.service.SearchPersons(r.Context(), query, limit)
	if err != nil {
		if errors.Is(err, ErrInvalidSearch) {
			writeProductionError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "busca de pessoa física inválida")
			return
		}
		writeProductionError(w, r, http.StatusServiceUnavailable, "PARTY_UNAVAILABLE", "não foi possível buscar pessoas físicas")
		return
	}
	items := make([]personResponse, 0, len(found))
	for _, item := range found {
		items = append(items, personResponseFromModel(item))
	}
	writeProductionJSON(w, http.StatusOK, map[string]any{"items": items})
}

// scope garante que o tenant e a empresa fixados no contexto HTTP (compostos
// por auth.Require a partir do principal autenticado) estão presentes e
// casam com o principal. Nenhuma requisição alcança o repositório sem essa
// checagem; a negação é sempre fail-closed.
// scope é uma defesa redundante: auth.Require já garante principal, tenant e
// empresa consistentes no contexto antes de chamar este handler, então estes
// ramos não são alcançados por tráfego real. Os códigos seguem
// docs/contracts/auth-http-v1.md (AUTHENTICATION_REQUIRED/SCOPE_FORBIDDEN),
// não códigos inventados nesta fatia, para que qualquer regressão no
// middleware produza um envelope já documentado.
func (h *ProductionHandler) scope(w http.ResponseWriter, r *http.Request) (platformauth.Principal, bool) {
	principal, err := platformauth.PrincipalFromContext(r.Context())
	if err != nil {
		writeProductionError(w, r, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "autenticação necessária")
		return platformauth.Principal{}, false
	}
	tenantID, err := db.TenantFromContext(r.Context())
	if err != nil || tenantID != principal.TenantID {
		writeProductionError(w, r, http.StatusForbidden, "SCOPE_FORBIDDEN", "tenant ausente no contexto autenticado")
		return platformauth.Principal{}, false
	}
	companyID, err := db.CompanyFromContext(r.Context())
	if err != nil || companyID != principal.CompanyID {
		writeProductionError(w, r, http.StatusForbidden, "SCOPE_FORBIDDEN", "empresa ausente no contexto autenticado")
		return platformauth.Principal{}, false
	}
	return principal, true
}

func personFromCreateRequest(request createPersonRequest) (Party, error) {
	name := ""
	if request.Name != nil {
		name = strings.TrimSpace(*request.Name)
	}
	if name == "" {
		return Party{}, fmt.Errorf("%w: name é obrigatório", ErrInvalidPartyRequest)
	}
	if utf8.RuneCountInString(name) > maxPartyNameLength {
		return Party{}, fmt.Errorf("%w: name excede %d caracteres", ErrInvalidPartyRequest, maxPartyNameLength)
	}

	document := ""
	if request.Document != nil {
		document = strings.TrimSpace(*request.Document)
	}
	if utf8.RuneCountInString(document) > maxPartyDocumentLength {
		return Party{}, fmt.Errorf("%w: document excede %d caracteres", ErrInvalidPartyRequest, maxPartyDocumentLength)
	}

	socialName := normalizeSocialName(KindPerson, request.SocialName)
	if socialName != nil && utf8.RuneCountInString(*socialName) > maxPartyNameLength {
		return Party{}, fmt.Errorf("%w: social_name excede %d caracteres", ErrInvalidPartyRequest, maxPartyNameLength)
	}

	return Party{Kind: KindPerson, Name: name, Document: document, Status: StatusActive, SocialName: socialName}, nil
}

func personResponseFromModel(model Party) personResponse {
	return personResponse{
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

func writeProductionError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeProductionJSON(w, status, map[string]any{
		"code":    code,
		"message": message,
		"details": map[string]any{},
		"traceId": observability.TraceIDFromContext(r.Context()),
	})
}

func writeProductionJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
