package catalog

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/jackc/pgx/v5"
)

// SearchActiveServices busca serviços ativos e não excluídos no tenant.
func (r *Repository) SearchActiveServices(ctx context.Context, query string, limit int) ([]CatalogItem, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	query, err = normalizeDiscoverySearch(query)
	if err != nil {
		return nil, err
	}

	var items []CatalogItem
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, unit
			FROM catalog.catalog_item
			WHERE tenant_id = $1 AND kind = 'service' AND active AND deleted_at IS NULL
			  AND ($2 = '' OR strpos(lower(name), lower($2)) > 0)
			ORDER BY lower(name), id
			LIMIT $3`, tenantID, query, clampListLimit(limit))
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (CatalogItem, error) {
			item := CatalogItem{TenantID: tenantID, Kind: KindService, Active: true}
			err := row.Scan(&item.ID, &item.Name, &item.Unit)
			return item, err
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("buscar serviços ativos: %w", err)
	}
	return items, nil
}

// SearchAvailablePriceLists busca tabelas iniciadas até a data de referência.
// A vigência do preço de cada serviço é verificada por ResolveServiceOffer.
func (r *Repository) SearchAvailablePriceLists(ctx context.Context, query, on string, limit int) ([]PriceList, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateReferenceDate(on); err != nil {
		return nil, err
	}
	query, err = normalizeDiscoverySearch(query)
	if err != nil {
		return nil, err
	}

	var lists []PriceList
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, to_char(valid_from, 'YYYY-MM-DD')
			FROM catalog.price_list
			WHERE tenant_id = $1 AND valid_from <= $2::text::date
			  AND ($3 = '' OR strpos(lower(name), lower($3)) > 0)
			ORDER BY lower(name), id
			LIMIT $4`, tenantID, on, query, clampListLimit(limit))
		if err != nil {
			return err
		}
		lists, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (PriceList, error) {
			list := PriceList{TenantID: tenantID}
			err := row.Scan(&list.ID, &list.Name, &list.ValidFrom)
			return list, err
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("buscar tabelas de preços disponíveis: %w", err)
	}
	return lists, nil
}

func normalizeDiscoverySearch(query string) (string, error) {
	query = strings.TrimSpace(query)
	if !utf8.ValidString(query) || strings.ContainsRune(query, 0) || utf8.RuneCountInString(query) > maxCatalogNameLength {
		return "", ErrInvalidCatalogRequest
	}
	return query, nil
}
