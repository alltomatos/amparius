package party

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/audit"
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
	// ErrDeceasedAlreadyExists means the referenced person already has a
	// deceased record in the same tenant.
	ErrDeceasedAlreadyExists = errors.New("pessoa já cadastrada como falecida")
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
	return r.createDeceased(ctx, input, nil)
}

// CreateDeceasedWithAudit registers the deceased and its privacy audit entry
// in the same tenant transaction.
func (r *Repository) CreateDeceasedWithAudit(ctx context.Context, input DeceasedCreate, provider audit.PolicyProvider) (Deceased, error) {
	const action = "party.deceased.register"
	policy, err := audit.ResolvePolicy(ctx, provider, action)
	if err != nil {
		return Deceased{}, err
	}
	return r.createDeceased(ctx, input, func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		_, err := audit.AppendTx(ctx, tx, audit.Entry{
			Action: action, ResourceType: "party.deceased", ResourceID: id,
			Outcome: audit.OutcomeSuccess, RetentionUntil: policy.RetentionUntil,
			Detail: []byte(fmt.Sprintf("{%q:%q,%q:%q}", "purpose_code", policy.PurposeCode, "legal_basis_code", policy.LegalBasisCode)),
		})
		return err
	})
}

func (r *Repository) createDeceased(ctx context.Context, input DeceasedCreate, appendAudit func(context.Context, pgx.Tx, uuid.UUID) error) (Deceased, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Deceased{}, err
	}
	if input.PersonID == uuid.Nil {
		return Deceased{}, ErrDeceasedPersonNotFound
	}

	var result Deceased
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = createDeceasedTx(ctx, tx, tenantID, input)
		if err == nil && appendAudit != nil {
			return appendAudit(ctx, tx, result.ID)
		}
		return err
	})
	if err != nil {
		return Deceased{}, fmt.Errorf("criar registro de falecido: %w", err)
	}
	return result, nil
}

func createDeceasedTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input DeceasedCreate) (Deceased, error) {
	person, err := findDeceasedPerson(ctx, tx, tenantID, input.PersonID)
	if err != nil {
		return Deceased{}, err
	}
	if err := lockPartyCreate(ctx, tx, "deceased", tenantID, input.PersonID); err != nil {
		return Deceased{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM party.deceased WHERE tenant_id = $1 AND person_id = $2)",
		tenantID, input.PersonID).Scan(&exists); err != nil {
		return Deceased{}, err
	}
	if exists {
		return Deceased{}, ErrDeceasedAlreadyExists
	}
	result := Deceased{
		Person:           person,
		TenantID:         tenantID,
		PersonID:         input.PersonID,
		DeathAt:          input.DeathAt,
		DeathCertificate: normalizeDeceasedText(input.DeathCertificate),
		BurialSite:       normalizeDeceasedText(input.BurialSite),
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO party.deceased (tenant_id, person_id, death_at, death_certificate, burial_site)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, tenantID, input.PersonID, input.DeathAt,
		result.DeathCertificate, result.BurialSite).Scan(&result.ID)
	if err != nil {
		return Deceased{}, err
	}
	return result, nil
}

// lockPartyCreate serializes writers for a tenant and aggregate through commit.
// With READ COMMITTED the following statement sees the previous writer's insert.
func lockPartyCreate(ctx context.Context, tx pgx.Tx, aggregate string, tenantID, recordID uuid.UUID) error {
	key := "party.create." + aggregate + ":" + tenantID.String() + ":" + recordID.String()
	var isolation string
	if err := tx.QueryRow(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0)), current_setting('transaction_isolation')",
		key).Scan(nil, &isolation); err != nil {
		return fmt.Errorf("serializar cadastro de party: %w", err)
	}
	if isolation != "read committed" {
		return errors.New("cadastro de party exige transação READ COMMITTED")
	}
	return nil
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
