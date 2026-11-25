package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrContextNotFound = errors.New("contexto não encontrado")
	ErrInvalidCursor   = errors.New("cursor inválido")
)

type Tenant struct {
	ID     uuid.UUID
	Name   string
	Status string
}

type Company struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Document  string
	Name      string
	Kind      string
	Status    string
	CreatedAt time.Time
}

type CompanyPage struct {
	Items      []Company
	NextCursor string
}

type companyCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

// GetTenant lê somente o tenant já fixado no contexto. O tenant nunca é
// aceito como parâmetro HTTP.
func (r *Repository) GetTenant(ctx context.Context) (Tenant, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Tenant{}, err
	}
	var tenant Tenant
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, name, status
			  FROM identity.tenant
			 WHERE id = $1 AND deleted_at IS NULL`, tenantID).
			Scan(&tenant.ID, &tenant.Name, &tenant.Status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrContextNotFound
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("consultar tenant do contexto: %w", err)
	}
	return tenant, nil
}

// UserCanAccessCompany valida o vínculo persistido do usuário com o CNPJ e o
// tenant. A consulta é protegida por RLS e não revela existência fora do
// contexto autenticado.
func (r *Repository) UserCanAccessCompany(ctx context.Context, userID, companyID uuid.UUID) (bool, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return false, err
	}
	if userID == uuid.Nil || companyID == uuid.Nil {
		return false, nil
	}
	var allowed bool
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM identity.user_company uc
				  JOIN identity.company c
				    ON c.tenant_id = uc.tenant_id AND c.id = uc.company_id
				 WHERE uc.tenant_id = $1
				   AND uc.user_id = $2
				   AND uc.company_id = $3
				   AND c.deleted_at IS NULL
			)`, tenantID, userID, companyID).Scan(&allowed)
	})
	if err != nil {
		return false, fmt.Errorf("validar acesso à empresa: %w", err)
	}
	return allowed, nil
}

func (r *Repository) GetUserCompany(ctx context.Context, userID, companyID uuid.UUID) (Company, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Company{}, err
	}
	if userID == uuid.Nil || companyID == uuid.Nil {
		return Company{}, ErrContextNotFound
	}
	var company Company
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT c.id, c.tenant_id, c.document, c.name, c.kind, c.status, c.created_at
			  FROM identity.user_company uc
			  JOIN identity.company c
			    ON c.tenant_id = uc.tenant_id AND c.id = uc.company_id
			 WHERE uc.tenant_id = $1
			   AND uc.user_id = $2
			   AND uc.company_id = $3
			   AND c.deleted_at IS NULL`, tenantID, userID, companyID).
			Scan(&company.ID, &company.TenantID, &company.Document, &company.Name, &company.Kind, &company.Status, &company.CreatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Company{}, ErrContextNotFound
	}
	if err != nil {
		return Company{}, fmt.Errorf("consultar empresa do contexto: %w", err)
	}
	return company, nil
}

// ListUserCompanies lista apenas os CNPJs vinculados ao usuário autenticado.
// A ordenação (created_at, id) é estável e o cursor não expõe SQL ou tenant.
func (r *Repository) ListUserCompanies(ctx context.Context, userID uuid.UUID, cursor string, limit int) (CompanyPage, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return CompanyPage{}, err
	}
	if userID == uuid.Nil {
		return CompanyPage{}, fmt.Errorf("%w: usuário ausente", ErrContextNotFound)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	after, err := decodeCompanyCursor(cursor)
	if err != nil {
		return CompanyPage{}, err
	}

	args := []any{tenantID, userID}
	whereCursor := ""
	if after != nil {
		whereCursor = " AND (c.created_at, c.id) > ($3, $4)"
		args = append(args, after.CreatedAt, after.ID)
	}
	args = append(args, limit+1)
	query := `
		SELECT c.id, c.tenant_id, c.document, c.name, c.kind, c.status, c.created_at
		  FROM identity.user_company uc
		  JOIN identity.company c
		    ON c.tenant_id = uc.tenant_id AND c.id = uc.company_id
		 WHERE uc.tenant_id = $1
		   AND uc.user_id = $2
		   AND c.deleted_at IS NULL` + whereCursor + `
		 ORDER BY c.created_at ASC, c.id ASC
		 LIMIT $` + fmt.Sprint(len(args))

	companies := make([]Company, 0, limit)
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var company Company
			if err := rows.Scan(&company.ID, &company.TenantID, &company.Document, &company.Name, &company.Kind, &company.Status, &company.CreatedAt); err != nil {
				return err
			}
			companies = append(companies, company)
		}
		return rows.Err()
	})
	if err != nil {
		return CompanyPage{}, fmt.Errorf("listar empresas do usuário: %w", err)
	}

	page := CompanyPage{Items: companies}
	if len(companies) > limit {
		last := companies[limit-1]
		page.Items = companies[:limit]
		page.NextCursor, err = encodeCompanyCursor(companyCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return CompanyPage{}, fmt.Errorf("gerar cursor de empresas: %w", err)
		}
	}
	return page, nil
}

func encodeCompanyCursor(cursor companyCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeCompanyCursor(value string) (*companyCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var cursor companyCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return nil, ErrInvalidCursor
	}
	return &cursor, nil
}

// MaskDocument expõe somente os quatro últimos caracteres do CNPJ/documento
// na listagem. O valor integral permanece restrito ao caso de uso autorizado.
func MaskDocument(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return strings.Repeat("*", len(value))
	}
	return strings.Repeat("*", len(value)-4) + value[len(value)-4:]
}
