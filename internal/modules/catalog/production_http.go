package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/alltomatos/amparius/internal/platform/auth"
	httpserver "github.com/alltomatos/amparius/internal/platform/http"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
)

const maxCatalogAmountCents int64 = 999_999_999_999_999_999

// OperationalService é o contrato do catálogo autenticado.
type OperationalService interface {
	CreateItem(context.Context, CatalogItem) (CatalogItem, error)
	CreatePriceList(context.Context, PriceList) (PriceList, error)
	CreatePrice(context.Context, Price) (Price, error)
	ResolveServiceOffer(context.Context, uuid.UUID, uuid.UUID, string) (ServiceOffer, error)
	SearchActiveServices(context.Context, string, int) ([]CatalogItem, error)
	SearchAvailablePriceLists(context.Context, string, string, int) ([]PriceList, error)
}

type operationalHandler struct{ service OperationalService }

// AuthenticatedRoutes monta as rotas com o escopo de tenant fornecido pelo
// middleware, conforme as permissões do catálogo.
func AuthenticatedRoutes(service OperationalService) []httpserver.AuthenticatedRoute {
	h := operationalHandler{service: service}
	return []httpserver.AuthenticatedRoute{
		{Pattern: "POST /v1/catalog/items", Permission: auth.PermissionCatalogWrite, Handler: h.available(h.createService)},
		{Pattern: "POST /v1/catalog/price-lists", Permission: auth.PermissionCatalogWrite, Handler: h.available(h.createPriceList)},
		{Pattern: "POST /v1/catalog/prices", Permission: auth.PermissionCatalogWrite, Handler: h.available(h.createPrice)},
		{Pattern: "GET /v1/catalog/items", Permission: auth.PermissionCatalogRead, Handler: h.available(h.searchServices)},
		{Pattern: "GET /v1/catalog/price-lists", Permission: auth.PermissionCatalogRead, Handler: h.available(h.searchPriceLists)},
		{Pattern: "GET /v1/catalog/offers", Permission: auth.PermissionCatalogRead, Handler: h.available(h.getOffer)},
	}
}

func (h operationalHandler) available(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if h.service == nil {
			writeOperationalError(w, r, http.StatusServiceUnavailable, "CATALOG_UNAVAILABLE", "catálogo indisponível")
			return
		}
		next(w, r)
	})
}

func (h operationalHandler) createService(w http.ResponseWriter, r *http.Request) {
	item, err := decodeItemBody(w, r)
	if err != nil || item.Kind != KindService {
		writeOperationalError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "informe um serviço com kind=service, name e unit válidos")
		return
	}
	created, err := h.service.CreateItem(r.Context(), item)
	if err != nil {
		writeOperationalServiceError(w, r, err)
		return
	}
	writeCatalogJSON(w, http.StatusCreated, itemFromModel(created))
}

func (h operationalHandler) createPriceList(w http.ResponseWriter, r *http.Request) {
	list, err := decodePriceListBody(w, r)
	if err != nil || !supportedCatalogDate(list.ValidFrom) {
		writeOperationalError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "informe name e valid_from válidos")
		return
	}
	created, err := h.service.CreatePriceList(r.Context(), list)
	if err != nil {
		writeOperationalServiceError(w, r, err)
		return
	}
	writeCatalogJSON(w, http.StatusCreated, priceListFromModel(created))
}

func (h operationalHandler) createPrice(w http.ResponseWriter, r *http.Request) {
	price, err := decodePriceBody(w, r)
	if err != nil || price.AmountCents > maxCatalogAmountCents ||
		!supportedCatalogDate(price.ValidFrom) || (price.ValidTo != "" && !supportedCatalogDate(price.ValidTo)) {
		writeOperationalError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "informe referências, valor em centavos e período válidos")
		return
	}
	created, err := h.service.CreatePrice(r.Context(), price)
	if err != nil {
		writeOperationalServiceError(w, r, err)
		return
	}
	writeCatalogJSON(w, http.StatusCreated, priceFromModel(created))
}

func (h operationalHandler) searchServices(w http.ResponseWriter, r *http.Request) {
	query, limit, err := parseDiscoveryQuery(r.URL.RawQuery, false)
	if err != nil {
		writeOperationalServiceError(w, r, err)
		return
	}
	items, err := h.service.SearchActiveServices(r.Context(), query.Get("q"), limit)
	if err != nil {
		writeOperationalServiceError(w, r, err)
		return
	}
	response := make([]itemResponse, 0, len(items))
	for _, item := range items {
		response = append(response, itemFromModel(item))
	}
	writeCatalogJSON(w, http.StatusOK, struct {
		Items []itemResponse `json:"items"`
	}{response})
}

func (h operationalHandler) searchPriceLists(w http.ResponseWriter, r *http.Request) {
	query, limit, err := parseDiscoveryQuery(r.URL.RawQuery, true)
	if err != nil {
		writeOperationalServiceError(w, r, err)
		return
	}
	lists, err := h.service.SearchAvailablePriceLists(r.Context(), query.Get("q"), query.Get("on"), limit)
	if err != nil {
		writeOperationalServiceError(w, r, err)
		return
	}
	response := make([]priceListResponse, 0, len(lists))
	for _, list := range lists {
		response = append(response, priceListFromModel(list))
	}
	writeCatalogJSON(w, http.StatusOK, struct {
		Items []priceListResponse `json:"items"`
	}{response})
}

func parseDiscoveryQuery(rawQuery string, requireDate bool) (url.Values, int, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil || (requireDate && len(query["on"]) != 1) {
		return nil, 0, ErrInvalidCatalogRequest
	}
	for key, values := range query {
		if len(values) != 1 || (key != "q" && key != "limit" && !(requireDate && key == "on")) {
			return nil, 0, ErrInvalidCatalogRequest
		}
	}
	limit := DefaultListLimit
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > MaxListLimit {
			return nil, 0, ErrInvalidCatalogRequest
		}
	}
	return query, limit, nil
}

func (h operationalHandler) getOffer(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 3 || len(query["item_id"]) != 1 || len(query["list_id"]) != 1 || len(query["on"]) != 1 {
		writeOperationalError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "informe item_id, list_id e on uma vez cada")
		return
	}
	itemID, itemErr := parseRequiredUUID("item_id", &query["item_id"][0])
	listID, listErr := parseRequiredUUID("list_id", &query["list_id"][0])
	if itemErr != nil || listErr != nil {
		writeOperationalError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "informe item_id e list_id válidos")
		return
	}

	offer, err := h.service.ResolveServiceOffer(r.Context(), itemID, listID, query.Get("on"))
	if err != nil {
		writeOperationalServiceError(w, r, err)
		return
	}
	writeCatalogJSON(w, http.StatusOK, struct {
		Item      itemResponse      `json:"item"`
		PriceList priceListResponse `json:"price_list"`
		Price     priceResponse     `json:"price"`
	}{itemFromModel(offer.Item), priceListFromModel(offer.PriceList), priceFromModel(offer.Price)})
}

func writeOperationalServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidCatalogRequest), errors.Is(err, ErrInvalidName), errors.Is(err, ErrInvalidUnit), errors.Is(err, ErrInvalidKind):
		writeOperationalError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "entrada inválida para o catálogo")
	case errors.Is(err, ErrItemNotFound), errors.Is(err, ErrPriceListNotFound):
		writeOperationalError(w, r, http.StatusNotFound, "CATALOG_REFERENCE_NOT_FOUND", "referência de catálogo não encontrada")
	case errors.Is(err, ErrOfferNotFound):
		writeOperationalError(w, r, http.StatusNotFound, "OFFER_NOT_FOUND", "oferta vigente não encontrada")
	case errors.Is(err, ErrOfferAmbiguous):
		writeOperationalError(w, r, http.StatusConflict, "OFFER_AMBIGUOUS", "mais de um preço vigente para o item e a tabela")
	default:
		writeOperationalError(w, r, http.StatusServiceUnavailable, "CATALOG_UNAVAILABLE", "catálogo indisponível")
	}
}

func writeOperationalError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeCatalogJSON(w, status, map[string]any{
		"code":    code,
		"message": message,
		"details": map[string]any{},
		"traceId": observability.TraceIDFromContext(r.Context()),
	})
}
