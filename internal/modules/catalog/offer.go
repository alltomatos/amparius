package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrOfferNotFound cobre referências ausentes, isoladas por tenant ou inelegíveis.
var ErrOfferNotFound = errors.New("oferta vigente não encontrada")

// ErrOfferAmbiguous indica mais de um preço vigente para o mesmo item e tabela.
var ErrOfferAmbiguous = errors.New("mais de um preço vigente para o item e a tabela")

type ServiceOffer struct {
	Item      CatalogItem
	PriceList PriceList
	Price     Price
}

// ResolveServiceOffer retorna o valor persistido para um serviço ativo, no
// tenant do contexto. O chamador fornece a data civil de referência em
// AAAA-MM-DD. Os limites do período do preço são inclusivos.
func (r *Repository) ResolveServiceOffer(ctx context.Context, itemID, listID uuid.UUID, on string) (ServiceOffer, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return ServiceOffer{}, err
	}
	if itemID == uuid.Nil || listID == uuid.Nil {
		return ServiceOffer{}, ErrInvalidCatalogRequest
	}
	date, err := parseCatalogDate("on", &on, true)
	if err != nil {
		return ServiceOffer{}, err
	}
	if date != on || !supportedCatalogDate(date) {
		return ServiceOffer{}, fmt.Errorf("%w: on deve ser uma data AAAA-MM-DD entre 0001 e 9999", ErrInvalidCatalogRequest)
	}

	offer := ServiceOffer{
		Item:      CatalogItem{ID: itemID, TenantID: tenantID, Kind: KindService, Active: true},
		PriceList: PriceList{ID: listID, TenantID: tenantID},
		Price:     Price{TenantID: tenantID, ItemID: itemID, ListID: listID},
	}
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT i.name, i.unit, l.name, to_char(l.valid_from, 'YYYY-MM-DD'),
			       p.id, p.amount::text, to_char(p.valid_from, 'YYYY-MM-DD'),
			       COALESCE(to_char(p.valid_to, 'YYYY-MM-DD'), '')
			FROM catalog.price p
			JOIN catalog.catalog_item i ON i.tenant_id = p.tenant_id AND i.id = p.item_id
			JOIN catalog.price_list l ON l.tenant_id = p.tenant_id AND l.id = p.list_id
			WHERE p.tenant_id = $1 AND p.item_id = $2 AND p.list_id = $3
			  AND i.kind = 'service' AND i.active AND i.deleted_at IS NULL
			  AND l.valid_from <= $4::text::date
			  AND p.valid_from <= $4::text::date
			  AND (p.valid_to IS NULL OR p.valid_to >= $4::text::date)
			LIMIT 2`, tenantID, itemID, listID, date)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			return ErrOfferNotFound
		}
		var amount string
		if err := rows.Scan(&offer.Item.Name, &offer.Item.Unit, &offer.PriceList.Name,
			&offer.PriceList.ValidFrom, &offer.Price.ID, &amount,
			&offer.Price.ValidFrom, &offer.Price.ValidTo); err != nil {
			return err
		}
		if rows.Next() {
			return ErrOfferAmbiguous
		}
		if err := rows.Err(); err != nil {
			return err
		}
		cents, err := decimalToCents(amount)
		if err != nil || cents < 0 {
			return ErrInvalidAmount
		}
		offer.Price.AmountCents = cents
		return nil
	})
	if err != nil {
		return ServiceOffer{}, fmt.Errorf("resolver oferta de serviço: %w", err)
	}
	return offer, nil
}

// As datas recebidas aqui já foram normalizadas por parseCatalogDate.
func supportedCatalogDate(date string) bool {
	return date >= "0001-01-01"
}
