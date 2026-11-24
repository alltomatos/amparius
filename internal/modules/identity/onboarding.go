// Package identity persiste o bootstrap de identidade do tenant.
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidOnboardingRequest = errors.New("solicitação de onboarding inválida")
	// ErrIdempotencyConflict classifica a reutilização da chave com payload diferente.
	ErrIdempotencyConflict = errors.New("conflito de idempotência do onboarding")
	// ErrOnboardingIdempotencyConflict é um nome explícito para o mesmo sentinel.
	ErrOnboardingIdempotencyConflict = ErrIdempotencyConflict
)

// OnboardingRequest não contém tenant_id: o banco gera o tenant no bootstrap.
// Os valores de DevelopmentRequest são sintéticos e não representam cadastro
// fiscal oficial.
type OnboardingRequest struct {
	OnboardingKey         string
	TenantName            string
	TenantDocument        string
	CompanyDocument       string
	CompanyName           string
	CompanyKind           string
	EstablishmentDocument string
	EstablishmentName     string
	Street                string
	Number                string
	City                  string
	State                 string
	Zip                   string
	OwnerEmail            string
	OwnerName             string
}

type OnboardingResult struct {
	TenantID        uuid.UUID
	CompanyID       uuid.UUID
	EstablishmentID uuid.UUID
	OwnerUserID     uuid.UUID
}

// Onboarder é o contrato mínimo usado por adaptadores de entrada, como a API.
// A regra transacional e a idempotência continuam centralizadas no repositório.
type Onboarder interface {
	Onboard(context.Context, OnboardingRequest) (OnboardingResult, error)
}

// DevelopmentRequest fornece a carga sintética usada para o primeiro tenant
// local. O chamador ainda deve fornecer uma chave de idempotência.
func DevelopmentRequest(onboardingKey string) OnboardingRequest {
	onboardingKey = strings.TrimSpace(onboardingKey)
	suffix := syntheticSuffix(onboardingKey)
	return OnboardingRequest{
		OnboardingKey:         onboardingKey,
		TenantName:            "Amparius Desenvolvimento",
		TenantDocument:        "DEV-TENANT-AMPARIUS-" + suffix,
		CompanyDocument:       "00.000.000/0000-DEV-" + suffix,
		CompanyName:           "Amparius Funerária Desenvolvimento",
		CompanyKind:           "funeral",
		EstablishmentDocument: "00.000.000/0001-DEV-" + suffix,
		EstablishmentName:     "Amparius Fortaleza Desenvolvimento",
		Street:                "Rua de Desenvolvimento",
		Number:                "000",
		City:                  "Fortaleza",
		State:                 "CE",
		Zip:                   "60000-000",
		OwnerEmail:            "owner.dev@amparius.invalid",
		OwnerName:             "Proprietário de Desenvolvimento",
	}
}

func syntheticSuffix(onboardingKey string) string {
	digest := sha256.Sum256([]byte(onboardingKey))
	return hex.EncodeToString(digest[:])[:16]
}

type Repository struct {
	database *db.DB
}

func NewRepository(database *db.DB) (*Repository, error) {
	if database == nil {
		return nil, errors.New("banco de identidade nulo")
	}
	return &Repository{database: database}, nil
}

// Onboard é o único caminho pré-autenticação deste slice. Não deve ser
// exposto diretamente a um cliente até existir autenticação de produção.
func (r *Repository) Onboard(ctx context.Context, request OnboardingRequest) (OnboardingResult, error) {
	if err := validateRequest(request); err != nil {
		return OnboardingResult{}, err
	}

	var result OnboardingResult
	err := r.database.WithSystemTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT tenant_id, company_id, establishment_id, owner_user_id
			  FROM identity.create_onboarding(
				$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
			  )`,
			request.OnboardingKey,
			request.TenantName,
			request.TenantDocument,
			request.CompanyDocument,
			request.CompanyName,
			request.CompanyKind,
			request.EstablishmentDocument,
			request.EstablishmentName,
			request.Street,
			request.Number,
			request.City,
			request.State,
			request.Zip,
			request.OwnerEmail,
			request.OwnerName,
		).Scan(&result.TenantID, &result.CompanyID, &result.EstablishmentID, &result.OwnerUserID)
	})
	if err != nil {
		if isIdempotencyConflict(err) {
			return OnboardingResult{}, fmt.Errorf("%w: chave reutilizada com payload diferente", ErrIdempotencyConflict)
		}
		return OnboardingResult{}, fmt.Errorf("executar onboarding: %w", err)
	}
	return result, nil
}

func isIdempotencyConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && strings.Contains(pgErr.Message, "onboarding_key ja usada com payload diferente")
}

func validateRequest(request OnboardingRequest) error {
	fields := map[string]string{
		"onboarding_key":         request.OnboardingKey,
		"tenant_name":            request.TenantName,
		"tenant_document":        request.TenantDocument,
		"company_document":       request.CompanyDocument,
		"company_name":           request.CompanyName,
		"company_kind":           request.CompanyKind,
		"establishment_document": request.EstablishmentDocument,
		"establishment_name":     request.EstablishmentName,
		"street":                 request.Street,
		"number":                 request.Number,
		"city":                   request.City,
		"state":                  request.State,
		"zip":                    request.Zip,
		"owner_email":            request.OwnerEmail,
		"owner_name":             request.OwnerName,
	}
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s vazio", ErrInvalidOnboardingRequest, name)
		}
	}
	if request.OwnerEmail != strings.ToLower(strings.TrimSpace(request.OwnerEmail)) {
		return fmt.Errorf("%w: owner_email deve estar em minúsculas", ErrInvalidOnboardingRequest)
	}
	if len(strings.TrimSpace(request.OnboardingKey)) > 200 {
		return fmt.Errorf("%w: onboarding_key excede 200 caracteres", ErrInvalidOnboardingRequest)
	}
	return nil
}
