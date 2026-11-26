package party

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alltomatos/amparius/internal/platform/audit"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	// ErrFamilyLinkNotFound means that the link is not visible in the tenant
	// from the context.
	ErrFamilyLinkNotFound = errors.New("vínculo familiar não encontrado")
	// ErrFamilyLinkDeceasedNotFound intentionally covers a missing deceased
	// and a deceased in another tenant, avoiding cross-tenant disclosure.
	ErrFamilyLinkDeceasedNotFound = errors.New("falecido não encontrado")
	// ErrFamilyLinkPersonNotFound intentionally covers a missing relative and
	// a relative in another tenant.
	ErrFamilyLinkPersonNotFound = errors.New("familiar não encontrado")
	// ErrFamilyLinkPersonNotApplicable identifies a company in the tenant.
	ErrFamilyLinkPersonNotApplicable = errors.New("pessoa jurídica não pode ser vinculada como familiar")
	// ErrFamilyLinkRelationInvalid means the relation is not an active value of
	// the party_family_relation domain in the database.
	ErrFamilyLinkRelationInvalid = errors.New("relação familiar inválida")
	// ErrFamilyLinkAlreadyExists means the relative is already linked to the
	// same deceased in this tenant.
	ErrFamilyLinkAlreadyExists = errors.New("familiar já vinculado a este falecido")
	// ErrFamilyLinkEvidenceRequired means the caller did not supply the opaque
	// consent evidence reference required by ADR 0042 before granting consent.
	ErrFamilyLinkEvidenceRequired = errors.New("evidência de consentimento é obrigatória")
	ErrFamilyLinkSelf             = errors.New("a pessoa falecida não pode ser vinculada a si mesma")
)

// policyPurposeFamilyLinkConsent identifies, for audit.PolicyProvider, the
// DPO-approved policy that must exist before a family link can be created
// (ADR 0042 §3/§4). It is a lookup key, not a legal basis or purpose code by
// itself — those come only from the resolved audit.Policy.
const policyPurposeFamilyLinkConsent = "party.family_link.register"

// FamilyLinkCreate references an existing deceased and an existing natural
// person. Neither record is created implicitly by this operation.
type FamilyLinkCreate struct {
	DeceasedID uuid.UUID
	PersonID   uuid.UUID
	Relation   string
}

// FamilyLink is a family link plus the names resolved from the source parties.
// No name is copied into party.family_link.
type FamilyLink struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	DeceasedID uuid.UUID
	PersonID   uuid.UUID
	Relation   string
	// Deceased is the person behind the deceased record.
	Deceased Party
	// Person is the relative linked to the deceased.
	Person Party
}

// FamilyLinkService is the public contract used by the development HTTP layer.
type FamilyLinkService interface {
	CreateFamilyLink(context.Context, FamilyLinkCreate) (FamilyLink, error)
	GetFamilyLinkByID(context.Context, uuid.UUID) (FamilyLink, error)
}

// CreateFamilyLink links an existing deceased to an existing natural person of
// the same tenant with a relation validated against the database domain.
func (r *Repository) CreateFamilyLink(ctx context.Context, input FamilyLinkCreate) (FamilyLink, error) {
	return r.createFamilyLink(ctx, input, nil)
}

// CreateFamilyLinkWithConsent records the link, consent and audit in one transaction.
func (r *Repository) CreateFamilyLinkWithConsent(ctx context.Context, input FamilyLinkCreate, evidenceRef uuid.UUID, provider audit.PolicyProvider) (FamilyLink, error) {
	if evidenceRef == uuid.Nil {
		return FamilyLink{}, ErrFamilyLinkEvidenceRequired
	}
	policy, err := audit.ResolvePolicy(ctx, provider, policyPurposeFamilyLinkConsent)
	if err != nil {
		return FamilyLink{}, err
	}
	return r.createFamilyLink(ctx, input, func(ctx context.Context, tx pgx.Tx) error {
		_, err := audit.RecordConsentTx(ctx, tx, audit.Consent{
			SubjectType:    "party.person",
			SubjectID:      input.PersonID,
			PurposeCode:    policy.PurposeCode,
			LegalBasisCode: policy.LegalBasisCode,
			Status:         "granted",
			EvidenceRef:    evidenceRef.String(),
			RetentionUntil: policy.RetentionUntil,
		})
		return err
	})
}

func (r *Repository) createFamilyLink(ctx context.Context, input FamilyLinkCreate, recordConsent func(context.Context, pgx.Tx) error) (FamilyLink, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return FamilyLink{}, err
	}
	relation := strings.TrimSpace(input.Relation)
	if input.DeceasedID == uuid.Nil {
		return FamilyLink{}, ErrFamilyLinkDeceasedNotFound
	}
	if input.PersonID == uuid.Nil {
		return FamilyLink{}, ErrFamilyLinkPersonNotFound
	}
	if relation == "" {
		return FamilyLink{}, ErrFamilyLinkRelationInvalid
	}

	var result FamilyLink
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = createFamilyLinkTx(ctx, tx, tenantID, input, relation)
		if err == nil && recordConsent != nil {
			return recordConsent(ctx, tx)
		}
		return err
	})
	if err != nil {
		return FamilyLink{}, fmt.Errorf("criar vínculo familiar: %w", err)
	}
	return result, nil
}

func createFamilyLinkTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input FamilyLinkCreate, relation string) (FamilyLink, error) {
	valid, err := familyRelationExists(ctx, tx, relation)
	if err != nil {
		return FamilyLink{}, err
	}
	if !valid {
		return FamilyLink{}, ErrFamilyLinkRelationInvalid
	}
	deceased, err := findDeceasedPersonForLink(ctx, tx, tenantID, input.DeceasedID)
	if err != nil {
		return FamilyLink{}, err
	}
	if deceased.ID == input.PersonID {
		return FamilyLink{}, ErrFamilyLinkSelf
	}
	relative, err := findPersonForLink(ctx, tx, tenantID, input.PersonID)
	if err != nil {
		return FamilyLink{}, err
	}

	if err := lockPartyCreate(ctx, tx, "family_link", tenantID, input.DeceasedID); err != nil {
		return FamilyLink{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM party.family_link WHERE tenant_id = $1 AND deceased_id = $2 AND person_id = $3)",
		tenantID, input.DeceasedID, input.PersonID).Scan(&exists); err != nil {
		return FamilyLink{}, err
	}
	if exists {
		return FamilyLink{}, ErrFamilyLinkAlreadyExists
	}
	result := FamilyLink{
		TenantID:   tenantID,
		DeceasedID: input.DeceasedID,
		PersonID:   input.PersonID,
		Relation:   relation,
		Deceased:   deceased,
		Person:     relative,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO party.family_link (tenant_id, deceased_id, person_id, relation)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, tenantID, input.DeceasedID, input.PersonID, relation).Scan(&result.ID)
	if err != nil {
		return FamilyLink{}, err
	}
	return result, nil
}

// GetFamilyLinkByID loads a link and resolves names from the deceased person
// and from the relative in the same tenant-scoped transaction.
func (r *Repository) GetFamilyLinkByID(ctx context.Context, id uuid.UUID) (FamilyLink, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return FamilyLink{}, err
	}
	if id == uuid.Nil {
		return FamilyLink{}, ErrFamilyLinkNotFound
	}

	var result FamilyLink
	var deceasedSocial, relativeSocial pgtype.Text
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT fl.id, fl.tenant_id, fl.deceased_id, fl.person_id, fl.relation,
			       dp.kind, dp.name, dpe.social_name,
			       rp.kind, rp.name, rpe.social_name
			FROM party.family_link fl
			JOIN party.deceased d
			  ON d.tenant_id = fl.tenant_id AND d.id = fl.deceased_id
			JOIN party.party dp
			  ON dp.tenant_id = d.tenant_id AND dp.id = d.person_id
			JOIN party.person dpe
			  ON dpe.tenant_id = d.tenant_id AND dpe.party_id = d.person_id
			JOIN party.party rp
			  ON rp.tenant_id = fl.tenant_id AND rp.id = fl.person_id
			JOIN party.person rpe
			  ON rpe.tenant_id = fl.tenant_id AND rpe.party_id = fl.person_id
			WHERE fl.tenant_id = $1 AND fl.id = $2`, tenantID, id).Scan(
			&result.ID, &result.TenantID, &result.DeceasedID, &result.PersonID, &result.Relation,
			&result.Deceased.Kind, &result.Deceased.Name, &deceasedSocial,
			&result.Person.Kind, &result.Person.Name, &relativeSocial)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return FamilyLink{}, ErrFamilyLinkNotFound
	}
	if err != nil {
		return FamilyLink{}, fmt.Errorf("buscar vínculo familiar: %w", err)
	}
	result.Deceased.ID = result.DeceasedID
	result.Deceased.TenantID = result.TenantID
	result.Deceased.SocialName = socialNameFromText(deceasedSocial)
	result.Person.ID = result.PersonID
	result.Person.TenantID = result.TenantID
	result.Person.SocialName = socialNameFromText(relativeSocial)
	return result, nil
}

func findDeceasedPersonForLink(ctx context.Context, tx pgx.Tx, tenantID, deceasedID uuid.UUID) (Party, error) {
	var result Party
	var social pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.tenant_id, p.kind, p.name, pe.social_name
		FROM party.deceased d
		JOIN party.party p
		  ON p.tenant_id = d.tenant_id AND p.id = d.person_id
		JOIN party.person pe
		  ON pe.tenant_id = d.tenant_id AND pe.party_id = d.person_id
		WHERE d.tenant_id = $1 AND d.id = $2`, tenantID, deceasedID).Scan(
		&result.ID, &result.TenantID, &result.Kind, &result.Name, &social)
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrFamilyLinkDeceasedNotFound
	}
	if err != nil {
		return Party{}, fmt.Errorf("buscar falecido do vínculo: %w", err)
	}
	result.SocialName = socialNameFromText(social)
	return result, nil
}

func findPersonForLink(ctx context.Context, tx pgx.Tx, tenantID, personID uuid.UUID) (Party, error) {
	var result Party
	var social pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.tenant_id, p.kind, p.name, pe.social_name
		FROM party.party p
		LEFT JOIN party.person pe
		  ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
		WHERE p.tenant_id = $1 AND p.id = $2`, tenantID, personID).Scan(
		&result.ID, &result.TenantID, &result.Kind, &result.Name, &social)
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrFamilyLinkPersonNotFound
	}
	if err != nil {
		return Party{}, fmt.Errorf("buscar familiar do vínculo: %w", err)
	}
	if result.Kind != KindPerson {
		return Party{}, ErrFamilyLinkPersonNotApplicable
	}
	// A natural person must have the companion party.person row; a missing row
	// is an inconsistent database state treated as not found.
	var personRowExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM party.person WHERE tenant_id = $1 AND party_id = $2)`,
		tenantID, personID).Scan(&personRowExists); err != nil {
		return Party{}, fmt.Errorf("validar familiar do vínculo: %w", err)
	}
	if !personRowExists {
		return Party{}, ErrFamilyLinkPersonNotFound
	}
	result.SocialName = socialNameFromText(social)
	return result, nil
}

func familyRelationExists(ctx context.Context, tx pgx.Tx, relation string) (bool, error) {
	var valid bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM identity.domain_value
			WHERE domain_code = 'party_family_relation' AND code = $1 AND active
		)`, relation).Scan(&valid); err != nil {
		return false, fmt.Errorf("validar relação familiar: %w", err)
	}
	return valid, nil
}
