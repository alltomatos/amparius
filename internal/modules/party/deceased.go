package party

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	// ErrDeceasedNotFound means that the deceased record is not visible in the
	// tenant from the context.
	ErrDeceasedNotFound = errors.New("falecido não encontrado")
	// ErrDeceasedPersonNotFound intentionally covers a missing person and a
	// person in another tenant, avoiding cross-tenant existence disclosure.
	ErrDeceasedPersonNotFound = errors.New("pessoa não encontrada")
	// ErrDeceasedPersonNotApplicable identifies a company in the current tenant.
	ErrDeceasedPersonNotApplicable = errors.New("pessoa jurídica não pode ser cadastrada como falecida")
)

// DeceasedCreate contains only references and attributes of the deceased
// record. The person is never implicitly created by this operation.
type DeceasedCreate struct {
	PersonID         uuid.UUID
	DeathAt          time.Time
	DeathCertificate *string
	BurialSite       *string
}

// Deceased is a deceased record plus the names resolved from its source party.
// No name is copied into party.deceased.
type Deceased struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	PersonID         uuid.UUID
	DeathAt          time.Time
	DeathCertificate *string
	BurialSite       *string
	Person           Party
}

// DeceasedService is the public contract used by the development HTTP layer.
type DeceasedService interface {
	CreateDeceased(context.Context, DeceasedCreate) (Deceased, error)
	GetDeceasedByID(context.Context, uuid.UUID) (Deceased, error)
}

// CreateDeceased creates a deceased record for an existing natural person in
// the tenant from the context.
func (r *Repository) CreateDeceased(ctx context.Context, input DeceasedCreate) (Deceased, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Deceased{}, err
	}
	if input.PersonID == uuid.Nil {
		return Deceased{}, ErrDeceasedPersonNotFound
	}

	var result Deceased
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		person, err := findDeceasedPerson(ctx, tx, tenantID, input.PersonID)
		if err != nil {
			return err
		}
		result.Person = person
		result.TenantID = tenantID
		result.PersonID = input.PersonID
		result.DeathAt = input.DeathAt
		result.DeathCertificate = normalizeDeceasedText(input.DeathCertificate)
		result.BurialSite = normalizeDeceasedText(input.BurialSite)
		return tx.QueryRow(ctx, `
			INSERT INTO party.deceased (tenant_id, person_id, death_at, death_certificate, burial_site)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id`, tenantID, input.PersonID, input.DeathAt,
			result.DeathCertificate, result.BurialSite).Scan(&result.ID)
	})
	if errors.Is(err, ErrDeceasedPersonNotFound) || errors.Is(err, ErrDeceasedPersonNotApplicable) {
		return Deceased{}, err
	}
	if err != nil {
		return Deceased{}, fmt.Errorf("criar registro de falecido: %w", err)
	}
	return result, nil
}

// GetDeceasedByID loads a deceased record and resolves names from party.party
// and party.person in the same tenant-scoped transaction.
func (r *Repository) GetDeceasedByID(ctx context.Context, id uuid.UUID) (Deceased, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Deceased{}, err
	}
	if id == uuid.Nil {
		return Deceased{}, ErrDeceasedNotFound
	}

	var result Deceased
	var socialName, deathCertificate, burialSite pgtype.Text
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT d.id, d.tenant_id, d.person_id, d.death_at,
			       d.death_certificate, d.burial_site,
			       p.kind, p.name, pe.social_name
			FROM party.deceased d
			JOIN party.party p
			  ON p.tenant_id = d.tenant_id AND p.id = d.person_id
			JOIN party.person pe
			  ON pe.tenant_id = d.tenant_id AND pe.party_id = d.person_id
			WHERE d.tenant_id = $1 AND d.id = $2`, tenantID, id).Scan(
			&result.ID, &result.TenantID, &result.PersonID, &result.DeathAt,
			&deathCertificate, &burialSite, &result.Person.Kind,
			&result.Person.Name, &socialName)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Deceased{}, ErrDeceasedNotFound
	}
	if err != nil {
		return Deceased{}, fmt.Errorf("buscar registro de falecido: %w", err)
	}
	result.Person.ID = result.PersonID
	result.Person.TenantID = result.TenantID
	result.Person.SocialName = socialNameFromText(socialName)
	result.DeathCertificate = deceasedTextFromText(deathCertificate)
	result.BurialSite = deceasedTextFromText(burialSite)
	return result, nil
}

func findDeceasedPerson(ctx context.Context, tx pgx.Tx, tenantID, personID uuid.UUID) (Party, error) {
	var result Party
	var personPartyID pgtype.UUID
	var socialName pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.tenant_id, p.kind, p.name, pe.social_name
		FROM party.party p
		LEFT JOIN party.person pe
		  ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
		WHERE p.tenant_id = $1 AND p.id = $2`, tenantID, personID).Scan(
		&result.ID, &result.TenantID, &result.Kind, &result.Name, &socialName)
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrDeceasedPersonNotFound
	}
	if err != nil {
		return Party{}, fmt.Errorf("buscar pessoa do falecido: %w", err)
	}
	if result.Kind != KindPerson {
		return Party{}, ErrDeceasedPersonNotApplicable
	}
	// A natural person must have the companion party.person row; this is a
	// defensive check for an inconsistent database state.
	if err := tx.QueryRow(ctx, `
		SELECT party_id FROM party.person WHERE tenant_id = $1 AND party_id = $2`, tenantID, personID).
		Scan(&personPartyID); errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrDeceasedPersonNotFound
	} else if err != nil {
		return Party{}, fmt.Errorf("validar pessoa do falecido: %w", err)
	}
	result.SocialName = socialNameFromText(socialName)
	return result, nil
}

func normalizeDeceasedText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func deceasedTextFromText(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return normalizeDeceasedText(&value.String)
}
