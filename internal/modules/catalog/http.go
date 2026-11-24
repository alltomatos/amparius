package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

const (
	developmentTenantHeader = "X-Amparius-Development-Tenant-ID"
	maxDevelopmentBodyBytes = 16 << 10
	maxCatalogNameLength    = 200
)

// ErrInvalidCatalogRequest classifica cargas de catálogo rejeitadas antes do
// repositório.
var ErrInvalidCatalogRequest = errors.New("carga de catálogo inválida")

// ErrInvalidDevelopmentTenant classifica o cabeçalho de tenant dev-only.
var ErrInvalidDevelopmentTenant = errors.New("tenant de desenvolvimento inválido")

// DevelopmentService é o contrato usado pelo handler dev-only. Não inclui
// tax_classification: classificação fiscal oficial fica fora desta fatia.
type DevelopmentService interface {
	CreateItem(context.Context, CatalogItem) (CatalogItem, error)
	GetItemByID(context.Context, uuid.UUID) (CatalogItem, error)
	ListItems(context.Context, int) ([]CatalogItem, error)

	CreatePriceList(context.Context, PriceList) (PriceList, error)
	GetPriceListByID(context.Context, uuid.UUID) (PriceList, error)
	ListPriceLists(context.Context, int) ([]PriceList, error)

	CreatePrice(context.Context, Price) (Price, error)
	GetPriceByID(context.Context, uuid.UUID) (Price, error)
	ListPrices(context.Context, int) ([]Price, error)
}

type itemPayload struct {
	Kind   *string `json:"kind"`
	Name   *string `json:"name"`
	Unit   *string `json:"unit"`
	Active *bool   `json:"active"`
}

type priceListPayload struct {
	Name      *string `json:"name"`
	ValidFrom *string `json:"valid_from"`
}

type pricePayload struct {
	ItemID      *string         `json:"item_id"`
	ListID      *string         `json:"list_id"`
	AmountCents json.RawMessage `json:"amount_cents"`
	ValidFrom   *string         `json:"valid_from"`
	ValidTo     *string         `json:"valid_to"`
}

type itemResponse struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Unit   string `json:"unit"`
	Active bool   `json:"active"`
}

type priceListResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ValidFrom string `json:"valid_from"`
}

type priceResponse struct {
	ID          string  `json:"id"`
	ItemID      string  `json:"item_id"`
	ListID      string  `json:"list_id"`
	AmountCents int64   `json:"amount_cents"`
	ValidFrom   string  `json:"valid_from"`
	ValidTo     *string `json:"valid_to"`
}

// NewDevelopmentHTTPHandler cria os endpoints dev-only de catálogo:
//
//	POST   /_dev/catalog/items
//	GET    /_dev/catalog/items
//	GET    /_dev/catalog/items/{id}
//	POST   /_dev/catalog/price-lists
//	GET    /_dev/catalog/price-lists
//	GET    /_dev/catalog/price-lists/{id}
//	POST   /_dev/catalog/prices
//	GET    /_dev/catalog/prices
//	GET    /_dev/catalog/prices/{id}
//
// O tenant vem exclusivamente do cabeçalho X-Amparius-Development-Tenant-ID.
// `tenant_id` no corpo e campos desconhecidos são rejeitados. Respostas usam
// X-Amparius-Development-Only: true e Cache-Control: no-store. Não há
// autenticação de produção; o handler só deve ser registrado quando
// APP_ENV=development e ENABLE_DEV_ONBOARDING=true.
func NewDevelopmentHTTPHandler(service DevelopmentService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amparius-Development-Only", "true")
		w.Header().Set("Cache-Control", "no-store")

		tenantID, err := developmentTenantID(r)
		if err != nil {
			writeDevelopmentCatalogError(w, http.StatusBadRequest,
				"cabeçalho "+developmentTenantHeader+" ausente ou inválido")
			return
		}

		switch {
		case r.URL.Path == "/_dev/catalog/items" || strings.HasPrefix(r.URL.Path, "/_dev/catalog/items/"):
			handleItems(w, r, service, tenantID)
		case r.URL.Path == "/_dev/catalog/price-lists" || strings.HasPrefix(r.URL.Path, "/_dev/catalog/price-lists/"):
			handlePriceLists(w, r, service, tenantID)
		case r.URL.Path == "/_dev/catalog/prices" || strings.HasPrefix(r.URL.Path, "/_dev/catalog/prices/"):
			handlePrices(w, r, service, tenantID)
		default:
			writeDevelopmentCatalogError(w, http.StatusNotFound, "rota não encontrada")
		}
	})
}

func handleItems(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	idText := r.PathValue("id")
	if idText == "" {
		switch r.Method {
		case http.MethodGet:
			listItems(w, r, service, tenantID)
		case http.MethodPost:
			createItem(w, r, service, tenantID)
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			writeDevelopmentCatalogError(w, http.StatusMethodNotAllowed, "método não permitido")
		}
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeDevelopmentCatalogError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	getItem(w, r, service, tenantID, idText)
}

func handlePriceLists(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	idText := r.PathValue("id")
	if idText == "" {
		switch r.Method {
		case http.MethodGet:
			listPriceLists(w, r, service, tenantID)
		case http.MethodPost:
			createPriceList(w, r, service, tenantID)
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			writeDevelopmentCatalogError(w, http.StatusMethodNotAllowed, "método não permitido")
		}
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeDevelopmentCatalogError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	getPriceList(w, r, service, tenantID, idText)
}

func handlePrices(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	idText := r.PathValue("id")
	if idText == "" {
		switch r.Method {
		case http.MethodGet:
			listPrices(w, r, service, tenantID)
		case http.MethodPost:
			createPrice(w, r, service, tenantID)
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
			writeDevelopmentCatalogError(w, http.StatusMethodNotAllowed, "método não permitido")
		}
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeDevelopmentCatalogError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}
	getPrice(w, r, service, tenantID, idText)
}

func createItem(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	item, err := decodeItemBody(w, r)
	if err != nil {
		writeDevelopmentCatalogError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}

	created, err := service.CreateItem(db.WithTenant(r.Context(), tenantID), item)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "cadastrar item")
		return
	}

	w.Header().Set("Location", "/_dev/catalog/items/"+created.ID.String())
	writeDevelopmentCatalogJSON(w, http.StatusCreated, map[string]any{
		"status": "ok",
		"item":   developmentItemFromModel(created),
	})
}

func getItem(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID, idText string) {
	id, ok := parseDevelopmentID(w, idText, "item")
	if !ok {
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}
	found, err := service.GetItemByID(db.WithTenant(r.Context(), tenantID), id)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "consultar item")
		return
	}
	writeDevelopmentCatalogJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"item":   developmentItemFromModel(found),
	})
}

func listItems(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	limit, ok := parseListLimit(w, r)
	if !ok {
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}
	items, err := service.ListItems(db.WithTenant(r.Context(), tenantID), limit)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "listar itens")
		return
	}
	response := make([]itemResponse, 0, len(items))
	for _, item := range items {
		response = append(response, developmentItemFromModel(item))
	}
	writeDevelopmentCatalogJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"items":  response,
	})
}

func createPriceList(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	list, err := decodePriceListBody(w, r)
	if err != nil {
		writeDevelopmentCatalogError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}
	created, err := service.CreatePriceList(db.WithTenant(r.Context(), tenantID), list)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "cadastrar lista de preços")
		return
	}
	w.Header().Set("Location", "/_dev/catalog/price-lists/"+created.ID.String())
	writeDevelopmentCatalogJSON(w, http.StatusCreated, map[string]any{
		"status":     "ok",
		"price_list": developmentPriceListFromModel(created),
	})
}

func getPriceList(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID, idText string) {
	id, ok := parseDevelopmentID(w, idText, "lista de preços")
	if !ok {
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}
	found, err := service.GetPriceListByID(db.WithTenant(r.Context(), tenantID), id)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "consultar lista de preços")
		return
	}
	writeDevelopmentCatalogJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"price_list": developmentPriceListFromModel(found),
	})
}

func listPriceLists(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	limit, ok := parseListLimit(w, r)
	if !ok {
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}
	lists, err := service.ListPriceLists(db.WithTenant(r.Context(), tenantID), limit)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "listar listas de preços")
		return
	}
	response := make([]priceListResponse, 0, len(lists))
	for _, list := range lists {
		response = append(response, developmentPriceListFromModel(list))
	}
	writeDevelopmentCatalogJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"price_lists": response,
	})
}

func createPrice(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	price, err := decodePriceBody(w, r)
	if err != nil {
		writeDevelopmentCatalogError(w, http.StatusBadRequest, err.Error())
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}
	created, err := service.CreatePrice(db.WithTenant(r.Context(), tenantID), price)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "cadastrar preço")
		return
	}
	w.Header().Set("Location", "/_dev/catalog/prices/"+created.ID.String())
	writeDevelopmentCatalogJSON(w, http.StatusCreated, map[string]any{
		"status": "ok",
		"price":  developmentPriceFromModel(created),
	})
}

func getPrice(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID, idText string) {
	id, ok := parseDevelopmentID(w, idText, "preço")
	if !ok {
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}
	found, err := service.GetPriceByID(db.WithTenant(r.Context(), tenantID), id)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "consultar preço")
		return
	}
	writeDevelopmentCatalogJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"price":  developmentPriceFromModel(found),
	})
}

func listPrices(w http.ResponseWriter, r *http.Request, service DevelopmentService, tenantID uuid.UUID) {
	limit, ok := parseListLimit(w, r)
	if !ok {
		return
	}
	if service == nil {
		writeDevelopmentCatalogError(w, http.StatusServiceUnavailable, "catálogo indisponível")
		return
	}
	prices, err := service.ListPrices(db.WithTenant(r.Context(), tenantID), limit)
	if err != nil {
		writeDevelopmentCatalogServiceError(w, err, "listar preços")
		return
	}
	response := make([]priceResponse, 0, len(prices))
	for _, price := range prices {
		response = append(response, developmentPriceFromModel(price))
	}
	writeDevelopmentCatalogJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"prices": response,
	})
}

func decodeItemBody(w http.ResponseWriter, r *http.Request) (CatalogItem, error) {
	trimmed, err := decodeDevelopmentJSONObject(w, r)
	if err != nil {
		return CatalogItem{}, fmt.Errorf("JSON inválido")
	}
	var payload itemPayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return CatalogItem{}, fmt.Errorf("%w: campos desconhecidos ou tipos inválidos", ErrInvalidCatalogRequest)
	}

	kind := stringField(payload.Kind)
	if kind == "" {
		return CatalogItem{}, fmt.Errorf("%w: kind é obrigatório", ErrInvalidCatalogRequest)
	}
	name, err := validateCatalogName(payload.Name)
	if err != nil {
		return CatalogItem{}, err
	}
	unit := stringField(payload.Unit)
	if unit == "" {
		return CatalogItem{}, fmt.Errorf("%w: unit é obrigatório", ErrInvalidCatalogRequest)
	}
	active := true
	if payload.Active != nil {
		active = *payload.Active
	}
	return CatalogItem{Kind: kind, Name: name, Unit: unit, Active: active}, nil
}

func decodePriceListBody(w http.ResponseWriter, r *http.Request) (PriceList, error) {
	trimmed, err := decodeDevelopmentJSONObject(w, r)
	if err != nil {
		return PriceList{}, fmt.Errorf("JSON inválido")
	}
	var payload priceListPayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return PriceList{}, fmt.Errorf("%w: campos desconhecidos ou tipos inválidos", ErrInvalidCatalogRequest)
	}
	name, err := validateCatalogName(payload.Name)
	if err != nil {
		return PriceList{}, err
	}
	validFrom, err := parseCatalogDate("valid_from", payload.ValidFrom, true)
	if err != nil {
		return PriceList{}, err
	}
	return PriceList{Name: name, ValidFrom: validFrom}, nil
}

func decodePriceBody(w http.ResponseWriter, r *http.Request) (Price, error) {
	trimmed, err := decodeDevelopmentJSONObject(w, r)
	if err != nil {
		return Price{}, fmt.Errorf("JSON inválido")
	}
	var payload pricePayload
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return Price{}, fmt.Errorf("%w: campos desconhecidos ou tipos inválidos", ErrInvalidCatalogRequest)
	}

	itemID, err := parseRequiredUUID("item_id", payload.ItemID)
	if err != nil {
		return Price{}, err
	}
	listID, err := parseRequiredUUID("list_id", payload.ListID)
	if err != nil {
		return Price{}, err
	}
	amountCents, err := parseAmountCents(payload.AmountCents)
	if err != nil {
		return Price{}, err
	}
	validFrom, err := parseCatalogDate("valid_from", payload.ValidFrom, true)
	if err != nil {
		return Price{}, err
	}
	validTo, err := parseCatalogDate("valid_to", payload.ValidTo, false)
	if err != nil {
		return Price{}, err
	}
	if validTo != "" {
		from, _ := time.Parse("2006-01-02", validFrom)
		to, _ := time.Parse("2006-01-02", validTo)
		if to.Before(from) {
			return Price{}, fmt.Errorf("%w: valid_to não pode ser anterior a valid_from", ErrInvalidCatalogRequest)
		}
	}

	return Price{
		ItemID:      itemID,
		ListID:      listID,
		AmountCents: amountCents,
		ValidFrom:   validFrom,
		ValidTo:     validTo,
	}, nil
}

// parseAmountCents exige um literal JSON inteiro. String, decimal e expoente
// são rejeitados para nunca introduzir float no caminho do dinheiro.
func parseAmountCents(raw json.RawMessage) (int64, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || string(value) == "null" {
		return 0, fmt.Errorf("%w: amount_cents é obrigatório", ErrInvalidCatalogRequest)
	}
	if value[0] != '-' && (value[0] < '0' || value[0] > '9') {
		return 0, fmt.Errorf("%w: amount_cents deve ser inteiro em centavos", ErrInvalidCatalogRequest)
	}
	amountCents, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: amount_cents deve ser inteiro em centavos", ErrInvalidCatalogRequest)
	}
	if amountCents < 0 {
		return 0, fmt.Errorf("%w: amount_cents não pode ser negativo", ErrInvalidCatalogRequest)
	}
	return amountCents, nil
}

func parseRequiredUUID(field string, raw *string) (uuid.UUID, error) {
	value := stringField(raw)
	if value == "" {
		return uuid.Nil, fmt.Errorf("%w: %s é obrigatório", ErrInvalidCatalogRequest, field)
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: %s deve ser UUID válido", ErrInvalidCatalogRequest, field)
	}
	return parsed, nil
}

func parseCatalogDate(field string, raw *string, required bool) (string, error) {
	value := stringField(raw)
	if value == "" {
		if required {
			return "", fmt.Errorf("%w: %s é obrigatório", ErrInvalidCatalogRequest, field)
		}
		return "", nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", fmt.Errorf("%w: %s deve estar no formato ISO date (AAAA-MM-DD)", ErrInvalidCatalogRequest, field)
	}
	return parsed.Format("2006-01-02"), nil
}

func validateCatalogName(raw *string) (string, error) {
	name := stringField(raw)
	if name == "" {
		return "", fmt.Errorf("%w: name é obrigatório", ErrInvalidCatalogRequest)
	}
	if utf8.RuneCountInString(name) > maxCatalogNameLength {
		return "", fmt.Errorf("%w: name excede %d caracteres", ErrInvalidCatalogRequest, maxCatalogNameLength)
	}
	return name, nil
}

func stringField(raw *string) string {
	if raw == nil {
		return ""
	}
	return strings.TrimSpace(*raw)
}

func parseListLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		writeDevelopmentCatalogError(w, http.StatusBadRequest, "limit deve ser inteiro positivo")
		return 0, false
	}
	return limit, true
}

func parseDevelopmentID(w http.ResponseWriter, raw, resource string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || id == uuid.Nil {
		writeDevelopmentCatalogError(w, http.StatusBadRequest, "id de "+resource+" inválido")
		return uuid.Nil, false
	}
	return id, true
}

func developmentItemFromModel(model CatalogItem) itemResponse {
	return itemResponse{
		ID:     model.ID.String(),
		Kind:   model.Kind,
		Name:   model.Name,
		Unit:   model.Unit,
		Active: model.Active,
	}
}

func developmentPriceListFromModel(model PriceList) priceListResponse {
	return priceListResponse{
		ID:        model.ID.String(),
		Name:      model.Name,
		ValidFrom: model.ValidFrom,
	}
}

func developmentPriceFromModel(model Price) priceResponse {
	response := priceResponse{
		ID:          model.ID.String(),
		ItemID:      model.ItemID.String(),
		ListID:      model.ListID.String(),
		AmountCents: model.AmountCents,
		ValidFrom:   model.ValidFrom,
	}
	if model.ValidTo != "" {
		validTo := model.ValidTo
		response.ValidTo = &validTo
	}
	return response
}

func writeDevelopmentCatalogServiceError(w http.ResponseWriter, err error, action string) {
	switch {
	case errors.Is(err, ErrNotFound),
		errors.Is(err, ErrItemNotFound),
		errors.Is(err, ErrPriceListNotFound):
		writeDevelopmentCatalogError(w, http.StatusNotFound, "registro não encontrado neste tenant")
	case errors.Is(err, ErrInvalidKind):
		writeDevelopmentCatalogError(w, http.StatusBadRequest, "kind inválido para o catálogo")
	case errors.Is(err, ErrInvalidUnit):
		writeDevelopmentCatalogError(w, http.StatusBadRequest, "unidade inválida para o catálogo")
	case errors.Is(err, ErrInvalidName):
		writeDevelopmentCatalogError(w, http.StatusBadRequest, "nome do catálogo é obrigatório")
	case errors.Is(err, ErrInvalidAmount):
		writeDevelopmentCatalogError(w, http.StatusBadRequest, "valor monetário não pode ser negativo")
	case errors.Is(err, db.ErrTenantRequired):
		writeDevelopmentCatalogError(w, http.StatusBadRequest, "tenant ausente no contexto")
	default:
		writeDevelopmentCatalogError(w, http.StatusInternalServerError, "não foi possível "+action)
	}
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

func writeDevelopmentCatalogError(w http.ResponseWriter, status int, message string) {
	writeDevelopmentCatalogJSON(w, status, map[string]string{"status": "error", "error": message})
}

func writeDevelopmentCatalogJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
