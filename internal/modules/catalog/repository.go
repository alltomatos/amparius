package catalog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	KindProduct = "product"
	KindService = "service"
	UnitUnit    = "unit"
	UnitHour    = "hour"
	UnitMonth   = "month"
	UnitService = "service"
)

var ErrNotFound = errors.New("registro de catálogo não encontrado")

type CatalogItem struct {
	ID, TenantID     uuid.UUID
	Kind, Name, Unit string
	Active           bool
}
type PriceList struct {
	ID, TenantID    uuid.UUID
	Name, ValidFrom string
}
type Price struct {
	ID, TenantID, ItemID, ListID uuid.UUID
	AmountCents                  int64
	ValidFrom, ValidTo           string
}
type Repository struct{ database *db.DB }

func NewRepository(database *db.DB) (*Repository, error) {
	if database == nil {
		return nil, errors.New("banco nulo")
	}
	return &Repository{database: database}, nil
}

func (r *Repository) CreateItem(ctx context.Context, item CatalogItem) (CatalogItem, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return CatalogItem{}, err
	}
	if item.TenantID != uuid.Nil && item.TenantID != tenantID {
		return CatalogItem{}, errors.New("tenant do item diverge do contexto")
	}
	item.TenantID = tenantID
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO catalog.catalog_item (tenant_id, kind, name, unit, active) VALUES ($1,$2,$3,$4,$5) RETURNING id`, tenantID, item.Kind, item.Name, item.Unit, item.Active).Scan(&item.ID)
	})
	if err != nil {
		return CatalogItem{}, fmt.Errorf("criar item: %w", err)
	}
	return item, nil
}

func (r *Repository) GetItemByID(ctx context.Context, id uuid.UUID) (CatalogItem, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return CatalogItem{}, err
	}
	var item CatalogItem
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, tenant_id, kind, name, unit, active FROM catalog.catalog_item WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan(&item.ID, &item.TenantID, &item.Kind, &item.Name, &item.Unit, &item.Active)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CatalogItem{}, ErrNotFound
	}
	if err != nil {
		return CatalogItem{}, fmt.Errorf("buscar item: %w", err)
	}
	return item, nil
}

func (r *Repository) ListItems(ctx context.Context, limit int) ([]CatalogItem, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var items []CatalogItem
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, tenant_id, kind, name, unit, active FROM catalog.catalog_item WHERE tenant_id=$1 ORDER BY id LIMIT $2`, tenantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item CatalogItem
			if err := rows.Scan(&item.ID, &item.TenantID, &item.Kind, &item.Name, &item.Unit, &item.Active); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar itens: %w", err)
	}
	return items, nil
}

func centsToDecimal(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}
func decimalToCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	neg := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return 0, errors.New("valor monetário inválido")
	}
	frac := "00"
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) > 2 {
			return 0, errors.New("valor monetário com mais de duas casas")
		}
	}
	frac += strings.Repeat("0", 2-len(frac))
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	result := whole * 100
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, err
	}
	result += f
	if neg {
		result = -result
	}
	return result, nil
}

func (r *Repository) CreatePriceList(ctx context.Context, list PriceList) (PriceList, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return PriceList{}, err
	}
	if list.TenantID != uuid.Nil && list.TenantID != tenantID {
		return PriceList{}, errors.New("tenant da lista diverge do contexto")
	}
	list.TenantID = tenantID
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO catalog.price_list (tenant_id, name, valid_from) VALUES ($1,$2,$3) RETURNING id`, tenantID, list.Name, list.ValidFrom).Scan(&list.ID)
	})
	if err != nil {
		return PriceList{}, fmt.Errorf("criar lista de preços: %w", err)
	}
	return list, nil
}

func (r *Repository) GetPriceListByID(ctx context.Context, id uuid.UUID) (PriceList, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return PriceList{}, err
	}
	var list PriceList
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, tenant_id, name, valid_from::text FROM catalog.price_list WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan(&list.ID, &list.TenantID, &list.Name, &list.ValidFrom)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return PriceList{}, ErrNotFound
	}
	if err != nil {
		return PriceList{}, fmt.Errorf("buscar lista de preços: %w", err)
	}
	return list, nil
}

func (r *Repository) ListPriceLists(ctx context.Context, limit int) ([]PriceList, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var lists []PriceList
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, tenant_id, name, valid_from::text FROM catalog.price_list WHERE tenant_id=$1 ORDER BY id LIMIT $2`, tenantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var list PriceList
			if err := rows.Scan(&list.ID, &list.TenantID, &list.Name, &list.ValidFrom); err != nil {
				return err
			}
			lists = append(lists, list)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar listas de preços: %w", err)
	}
	return lists, nil
}

func (r *Repository) CreatePrice(ctx context.Context, price Price) (Price, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Price{}, err
	}
	if price.TenantID != uuid.Nil && price.TenantID != tenantID {
		return Price{}, errors.New("tenant do preço diverge do contexto")
	}
	price.TenantID = tenantID
	amount := centsToDecimal(price.AmountCents)
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO catalog.price (tenant_id, item_id, list_id, amount, valid_from, valid_to) VALUES ($1,$2,$3,$4::numeric,$5,$6) RETURNING id`, tenantID, price.ItemID, price.ListID, amount, price.ValidFrom, nilIfEmpty(price.ValidTo)).Scan(&price.ID)
	})
	if err != nil {
		return Price{}, fmt.Errorf("criar preço: %w", err)
	}
	return price, nil
}

// Create, GetByID e List são a API curta do agregado catalog_item.
func (r *Repository) Create(ctx context.Context, item CatalogItem) (CatalogItem, error) {
	return r.CreateItem(ctx, item)
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (CatalogItem, error) {
	return r.GetItemByID(ctx, id)
}

func (r *Repository) List(ctx context.Context, limit int) ([]CatalogItem, error) {
	return r.ListItems(ctx, limit)
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (r *Repository) GetPriceByID(ctx context.Context, id uuid.UUID) (Price, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Price{}, err
	}
	var price Price
	var amount, validTo string
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,tenant_id,item_id,list_id,amount::text,valid_from::text,COALESCE(valid_to::text,'') FROM catalog.price WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan(&price.ID, &price.TenantID, &price.ItemID, &price.ListID, &amount, &price.ValidFrom, &validTo)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Price{}, ErrNotFound
	}
	if err != nil {
		return Price{}, fmt.Errorf("buscar preço: %w", err)
	}
	price.AmountCents, err = decimalToCents(amount)
	if err != nil {
		return Price{}, fmt.Errorf("converter preço: %w", err)
	}
	price.ValidTo = validTo
	return price, nil
}

func (r *Repository) ListPrices(ctx context.Context, limit int) ([]Price, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var prices []Price
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,tenant_id,item_id,list_id,amount::text,valid_from::text,COALESCE(valid_to::text,'') FROM catalog.price WHERE tenant_id=$1 ORDER BY id LIMIT $2`, tenantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p Price
			var amount string
			if err := rows.Scan(&p.ID, &p.TenantID, &p.ItemID, &p.ListID, &amount, &p.ValidFrom, &p.ValidTo); err != nil {
				return err
			}
			p.AmountCents, err = decimalToCents(amount)
			if err != nil {
				return err
			}
			prices = append(prices, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listar preços: %w", err)
	}
	return prices, nil
}
