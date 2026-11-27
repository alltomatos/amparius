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
	// ErrLegalEntityNotFound intentionally covers a missing legal entity and a
	// legal entity in another tenant, avoiding cross-tenant existence disclosure.
	ErrLegalEntityNotFound = errors.New("pessoa jurídica não encontrada")
	// ErrLegalEntityAlreadyExists means the CNPJ is already registered in this
	// tenant. There is no unique index backing this check: legacy data may
	// already contain a duplicate, so the check is advisory-lock + EXISTS.
	ErrLegalEntityAlreadyExists = errors.New("CNPJ já cadastrado neste tenant")
	// ErrPartyKindInvalid identifies a party of the wrong kind for the role it
	// was offered for (for example a company offered as a representative person).
	ErrPartyKindInvalid = errors.New("party do tipo informado não pode ocupar este papel")
	// ErrRepresentativePersonNotFound intentionally covers a missing person and
	// a person in another tenant.
	ErrRepresentativePersonNotFound = errors.New("pessoa não encontrada")
	// ErrRepresentativeRoleInvalid means the role is not an active value of the
	// party_representative_role domain in the database.
	ErrRepresentativeRoleInvalid = errors.New("papel de representante inválido")
	// ErrRepresentativeAlreadyExists means the same person already has this
	// role for this legal entity in the tenant.
	ErrRepresentativeAlreadyExists = errors.New("representante já vinculado a esta pessoa jurídica com este papel")
	// ErrContactValueInvalid means the contact value is malformed for its kind.
	ErrContactValueInvalid = errors.New("valor de contato inválido")
	// ErrAddressInvalid means a required address field is missing or malformed.
	ErrAddressInvalid = errors.New("endereço inválido")
)

const (
	policyPurposeContactAdd         = "party.contact.add"
	policyPurposeAddressAdd         = "party.address.add"
	policyPurposeRepresentativeLink = "party.representative.link"

	// ContactKindPhone, ContactKindEmail, ContactKindWhatsApp and
	// ContactKindOther mirror the party_contact_kind domain seeded in
	// migration 0002.
	ContactKindPhone    = "phone"
	ContactKindEmail    = "email"
	ContactKindWhatsApp = "whatsapp"
	ContactKindOther    = "other"

	// RepresentativeRoleLegalRepresentative, RepresentativeRoleAdministrator,
	// RepresentativeRoleAttorney and RepresentativeRoleContact mirror the
	// party_representative_role domain seeded in migration 0038.
	RepresentativeRoleLegalRepresentative = "legal_representative"
	RepresentativeRoleAdministrator       = "administrator"
	RepresentativeRoleAttorney            = "attorney"
	RepresentativeRoleContact             = "contact"
)

// LegalEntityCreate holds the attributes needed to register a new company.
type LegalEntityCreate struct {
	LegalName             string
	CNPJ                  string
	TradeName             *string
	StateRegistration     *string
	MunicipalRegistration *string
}

// LegalEntity is a company party plus its party.party_company attributes.
type LegalEntity struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	LegalName             string
	CNPJ                  string
	TradeName             *string
	StateRegistration     *string
	MunicipalRegistration *string
	Status                string
}

// ContactCreate references an existing party of any kind in the tenant.
type ContactCreate struct {
	PartyID uuid.UUID
	Kind    string
	Value   string
	Primary bool
}

// Contact is a phone, e-mail, WhatsApp or other contact channel of a party.
type Contact struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	PartyID  uuid.UUID
	Kind     string
	Value    string
	Primary  bool
}

// AddressCreate references an existing party of any kind in the tenant.
type AddressCreate struct {
	PartyID uuid.UUID
	Street  string
	Number  string
	City    string
	State   string
	Zip     string
	Primary bool
}

// Address is a postal address of a party.
type Address struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	PartyID  uuid.UUID
	Street   string
	Number   string
	City     string
	State    string
	Zip      string
	Primary  bool
}

// RepresentativeCreate links an existing company to an existing natural
// person. Neither record is created implicitly by this operation.
type RepresentativeCreate struct {
	CompanyID uuid.UUID
	PersonID  uuid.UUID
	Role      string
}

// Representative is a company-person link plus the person's resolved names.
// No name is copied into party.company_representative.
type Representative struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	PersonID  uuid.UUID
	Role      string
	Person    Party
}

// LegalEntityAggregate is the read model for GET /v1/legal-entities/{id}:
// the company plus every contact, address and representative in the tenant.
type LegalEntityAggregate struct {
	LegalEntity
	Contacts        []Contact
	Addresses       []Address
	Representatives []Representative
}

// CreateLegalEntity creates a company party and its party.party_company row
// atomically. A duplicate CNPJ in the tenant is rejected, even though no
// unique index backs it (legacy data may already contain a duplicate).
func (r *Repository) CreateLegalEntity(ctx context.Context, input LegalEntityCreate) (LegalEntity, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return LegalEntity{}, err
	}
	legalName := strings.TrimSpace(input.LegalName)
	if legalName == "" {
		return LegalEntity{}, ErrInvalidPartyRequest
	}

	input.CNPJ, err = NormalizeCNPJ(input.CNPJ)
	if err != nil {
		return LegalEntity{}, err
	}

	var result LegalEntity
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = createLegalEntityTx(ctx, tx, tenantID, input, legalName)
		return err
	})
	if err != nil {
		return LegalEntity{}, fmt.Errorf("criar pessoa jurídica: %w", err)
	}
	return result, nil
}

// Keep the SQL expression in sync with canonicalCNPJText. It strips only
// accepted formatting characters, so placeholders cannot become identifiers.
const canonicalCNPJDocumentSQL = "upper(translate(p.document, './- ', '') COLLATE \"C\")"

func createLegalEntityTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input LegalEntityCreate, legalName string) (LegalEntity, error) {
	if err := lockPartyCreate(ctx, tx, "legal_entity", tenantID, input.CNPJ); err != nil {
		return LegalEntity{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM party.party p WHERE p.tenant_id = $1 AND p.kind = $2
			  AND `+canonicalCNPJDocumentSQL+` = $3)`,
		tenantID, KindCompany, input.CNPJ).Scan(&exists); err != nil {
		return LegalEntity{}, err
	}
	if exists {
		return LegalEntity{}, ErrLegalEntityAlreadyExists
	}
	result := LegalEntity{
		TenantID:              tenantID,
		LegalName:             legalName,
		CNPJ:                  input.CNPJ,
		TradeName:             normalizeDeceasedText(input.TradeName),
		StateRegistration:     normalizeDeceasedText(input.StateRegistration),
		MunicipalRegistration: normalizeDeceasedText(input.MunicipalRegistration),
		Status:                StatusActive,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO party.party (tenant_id, kind, name, document, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, tenantID, KindCompany, legalName, input.CNPJ, result.Status).Scan(&result.ID); err != nil {
		return LegalEntity{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO party.party_company (tenant_id, party_id, trade_name, state_registration, municipal_registration)
		VALUES ($1, $2, $3, $4, $5)`, tenantID, result.ID,
		result.TradeName, result.StateRegistration, result.MunicipalRegistration); err != nil {
		return LegalEntity{}, err
	}
	return result, nil
}

// SearchLegalEntities restricts the search to companies (kind=company), by
// legal name, trade name or document, filtered in SQL before ORDER/LIMIT —
// mirroring the issue #72 fix applied to SearchPersons.
func (r *Repository) SearchLegalEntities(ctx context.Context, query string, limit int) ([]LegalEntity, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > maxPartyNameLength {
		return nil, ErrInvalidSearch
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	documentQuery := canonicalCNPJText(query)
	var result []LegalEntity
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.tenant_id, p.name, COALESCE(p.document, ''), p.status,
			       pc.trade_name, pc.state_registration, pc.municipal_registration
			FROM party.party p
			LEFT JOIN party.party_company pc ON pc.tenant_id = p.tenant_id AND pc.party_id = p.id
			WHERE p.tenant_id = $1 AND p.kind = $4
			  AND (p.name ILIKE '%' || $2 || '%'
			       OR COALESCE(pc.trade_name, '') ILIKE '%' || $2 || '%'
			       OR ($5 <> '' AND `+canonicalCNPJDocumentSQL+` ~ '^[0-9A-Z]{12}[0-9]{2}$'
			           AND strpos(`+canonicalCNPJDocumentSQL+`, $5) > 0))
			ORDER BY p.updated_at DESC, p.id LIMIT $3`, tenantID, query, limit, KindCompany, documentQuery)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item LegalEntity
			var tradeName, stateRegistration, municipalRegistration pgtype.Text
			if err := rows.Scan(&item.ID, &item.TenantID, &item.LegalName, &item.CNPJ, &item.Status,
				&tradeName, &stateRegistration, &municipalRegistration); err != nil {
				return err
			}
			item.TradeName = deceasedTextFromText(tradeName)
			item.StateRegistration = deceasedTextFromText(stateRegistration)
			item.MunicipalRegistration = deceasedTextFromText(municipalRegistration)
			result = append(result, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("buscar pessoas jurídicas: %w", err)
	}
	return result, nil
}

// GetLegalEntityAggregate loads a company and every contact, address and
// representative linked to it, in the same tenant-scoped transaction so the
// aggregate reflects one consistent snapshot.
func (r *Repository) GetLegalEntityAggregate(ctx context.Context, id uuid.UUID) (LegalEntityAggregate, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return LegalEntityAggregate{}, err
	}
	if id == uuid.Nil {
		return LegalEntityAggregate{}, ErrLegalEntityNotFound
	}

	var result LegalEntityAggregate
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var tradeName, stateRegistration, municipalRegistration pgtype.Text
		if err := tx.QueryRow(ctx, `
			SELECT p.id, p.tenant_id, p.name, COALESCE(p.document, ''), p.status,
			       pc.trade_name, pc.state_registration, pc.municipal_registration
			FROM party.party p
			LEFT JOIN party.party_company pc ON pc.tenant_id = p.tenant_id AND pc.party_id = p.id
			WHERE p.tenant_id = $1 AND p.id = $2 AND p.kind = $3`, tenantID, id, KindCompany).Scan(
			&result.ID, &result.TenantID, &result.LegalName, &result.CNPJ, &result.Status,
			&tradeName, &stateRegistration, &municipalRegistration); err != nil {
			return err
		}
		result.TradeName = deceasedTextFromText(tradeName)
		result.StateRegistration = deceasedTextFromText(stateRegistration)
		result.MunicipalRegistration = deceasedTextFromText(municipalRegistration)

		contacts, err := listContactsTx(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		result.Contacts = contacts

		addresses, err := listAddressesTx(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		result.Addresses = addresses

		representatives, err := listRepresentativesTx(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		result.Representatives = representatives
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return LegalEntityAggregate{}, ErrLegalEntityNotFound
	}
	if err != nil {
		return LegalEntityAggregate{}, fmt.Errorf("buscar pessoa jurídica: %w", err)
	}
	return result, nil
}

// CreateContact creates a contact without an audit trail. Used only by the
// development-only surface (synthetic fixtures); production always goes
// through CreateContactWithAudit.
func (r *Repository) CreateContact(ctx context.Context, input ContactCreate) (Contact, error) {
	return r.createContact(ctx, input, nil)
}

// CreateContactWithAudit registers a contact for any party of the tenant and
// its privacy audit entry in the same transaction. Fails closed (no record is
// written) when provider is nil or no DPO-approved policy resolves.
func (r *Repository) CreateContactWithAudit(ctx context.Context, input ContactCreate, provider audit.PolicyProvider) (Contact, error) {
	policy, err := audit.ResolvePolicy(ctx, provider, policyPurposeContactAdd)
	if err != nil {
		return Contact{}, err
	}
	return r.createContact(ctx, input, func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		_, err := audit.AppendTx(ctx, tx, audit.Entry{
			Action: policyPurposeContactAdd, ResourceType: "party.contact", ResourceID: id,
			Outcome: audit.OutcomeSuccess, RetentionUntil: policy.RetentionUntil,
			Detail: []byte(fmt.Sprintf("{%q:%q,%q:%q}", "purpose_code", policy.PurposeCode, "legal_basis_code", policy.LegalBasisCode)),
		})
		return err
	})
}

func (r *Repository) createContact(ctx context.Context, input ContactCreate, appendAudit func(context.Context, pgx.Tx, uuid.UUID) error) (Contact, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Contact{}, err
	}

	var result Contact
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = createContactTx(ctx, tx, tenantID, input)
		if err == nil && appendAudit != nil {
			return appendAudit(ctx, tx, result.ID)
		}
		return err
	})
	if err != nil {
		return Contact{}, fmt.Errorf("criar contato: %w", err)
	}
	return result, nil
}

func createContactTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input ContactCreate) (Contact, error) {
	if _, err := findPartyForAttachmentTx(ctx, tx, tenantID, input.PartyID); err != nil {
		return Contact{}, err
	}
	if err := lockPartyCreate(ctx, tx, "contact", tenantID, input.PartyID.String()+":"+input.Kind); err != nil {
		return Contact{}, err
	}
	if input.Primary {
		if _, err := tx.Exec(ctx, `
			UPDATE party.contact SET "primary" = false, version = version + 1, updated_at = now()
			WHERE tenant_id = $1 AND party_id = $2 AND kind = $3 AND "primary"`,
			tenantID, input.PartyID, input.Kind); err != nil {
			return Contact{}, err
		}
	}
	result := Contact{TenantID: tenantID, PartyID: input.PartyID, Kind: input.Kind, Value: input.Value, Primary: input.Primary}
	if err := tx.QueryRow(ctx, `
		INSERT INTO party.contact (tenant_id, party_id, kind, value, "primary")
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, tenantID, input.PartyID, input.Kind, input.Value, input.Primary).Scan(&result.ID); err != nil {
		return Contact{}, err
	}
	return result, nil
}

// ListContacts lists every contact of a party in the tenant from the context.
func (r *Repository) ListContacts(ctx context.Context, partyID uuid.UUID) ([]Contact, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var result []Contact
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := findPartyForAttachmentTx(ctx, tx, tenantID, partyID); err != nil {
			return err
		}
		result, err = listContactsTx(ctx, tx, tenantID, partyID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("listar contatos: %w", err)
	}
	return result, nil
}

func listContactsTx(ctx context.Context, tx pgx.Tx, tenantID, partyID uuid.UUID) ([]Contact, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, party_id, kind, value, "primary"
		FROM party.contact WHERE tenant_id = $1 AND party_id = $2 ORDER BY kind, id`, tenantID, partyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Contact
	for rows.Next() {
		var item Contact
		if err := rows.Scan(&item.ID, &item.TenantID, &item.PartyID, &item.Kind, &item.Value, &item.Primary); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// CreateAddress creates an address without an audit trail. Used only by the
// development-only surface (synthetic fixtures); production always goes
// through CreateAddressWithAudit.
func (r *Repository) CreateAddress(ctx context.Context, input AddressCreate) (Address, error) {
	return r.createAddress(ctx, input, nil)
}

// CreateAddressWithAudit registers an address for any party of the tenant and
// its privacy audit entry in the same transaction. Fails closed (no record is
// written) when provider is nil or no DPO-approved policy resolves.
func (r *Repository) CreateAddressWithAudit(ctx context.Context, input AddressCreate, provider audit.PolicyProvider) (Address, error) {
	policy, err := audit.ResolvePolicy(ctx, provider, policyPurposeAddressAdd)
	if err != nil {
		return Address{}, err
	}
	return r.createAddress(ctx, input, func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		_, err := audit.AppendTx(ctx, tx, audit.Entry{
			Action: policyPurposeAddressAdd, ResourceType: "party.address", ResourceID: id,
			Outcome: audit.OutcomeSuccess, RetentionUntil: policy.RetentionUntil,
			Detail: []byte(fmt.Sprintf("{%q:%q,%q:%q}", "purpose_code", policy.PurposeCode, "legal_basis_code", policy.LegalBasisCode)),
		})
		return err
	})
}

func (r *Repository) createAddress(ctx context.Context, input AddressCreate, appendAudit func(context.Context, pgx.Tx, uuid.UUID) error) (Address, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Address{}, err
	}

	var result Address
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = createAddressTx(ctx, tx, tenantID, input)
		if err == nil && appendAudit != nil {
			return appendAudit(ctx, tx, result.ID)
		}
		return err
	})
	if err != nil {
		return Address{}, fmt.Errorf("criar endereço: %w", err)
	}
	return result, nil
}

func createAddressTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input AddressCreate) (Address, error) {
	if _, err := findPartyForAttachmentTx(ctx, tx, tenantID, input.PartyID); err != nil {
		return Address{}, err
	}
	if err := lockPartyCreate(ctx, tx, "address", tenantID, input.PartyID.String()); err != nil {
		return Address{}, err
	}
	if input.Primary {
		if _, err := tx.Exec(ctx, `
			UPDATE party.address SET "primary" = false, version = version + 1, updated_at = now()
			WHERE tenant_id = $1 AND party_id = $2 AND "primary"`,
			tenantID, input.PartyID); err != nil {
			return Address{}, err
		}
	}
	result := Address{
		TenantID: tenantID, PartyID: input.PartyID, Street: input.Street, Number: input.Number,
		City: input.City, State: input.State, Zip: input.Zip, Primary: input.Primary,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO party.address (tenant_id, party_id, street, number, city, state, zip, "primary")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`, tenantID, input.PartyID, input.Street, input.Number, input.City, input.State, input.Zip, input.Primary).
		Scan(&result.ID); err != nil {
		return Address{}, err
	}
	return result, nil
}

// ListAddresses lists every address of a party in the tenant from the context.
func (r *Repository) ListAddresses(ctx context.Context, partyID uuid.UUID) ([]Address, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var result []Address
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := findPartyForAttachmentTx(ctx, tx, tenantID, partyID); err != nil {
			return err
		}
		result, err = listAddressesTx(ctx, tx, tenantID, partyID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("listar endereços: %w", err)
	}
	return result, nil
}

func listAddressesTx(ctx context.Context, tx pgx.Tx, tenantID, partyID uuid.UUID) ([]Address, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, party_id, street, number, city, state, zip, "primary"
		FROM party.address WHERE tenant_id = $1 AND party_id = $2 ORDER BY id`, tenantID, partyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Address
	for rows.Next() {
		var item Address
		if err := rows.Scan(&item.ID, &item.TenantID, &item.PartyID, &item.Street, &item.Number,
			&item.City, &item.State, &item.Zip, &item.Primary); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// CreateRepresentative links an existing natural person to an existing
// company without an audit trail. Used only by the development-only surface
// (synthetic fixtures); production always goes through
// CreateRepresentativeWithAudit.
func (r *Repository) CreateRepresentative(ctx context.Context, input RepresentativeCreate) (Representative, error) {
	return r.createRepresentative(ctx, input, nil)
}

// CreateRepresentativeWithAudit links an existing natural person to an
// existing company and writes its privacy audit entry in the same
// transaction. Fails closed (no record is written) when provider is nil or no
// DPO-approved policy resolves.
func (r *Repository) CreateRepresentativeWithAudit(ctx context.Context, input RepresentativeCreate, provider audit.PolicyProvider) (Representative, error) {
	policy, err := audit.ResolvePolicy(ctx, provider, policyPurposeRepresentativeLink)
	if err != nil {
		return Representative{}, err
	}
	return r.createRepresentative(ctx, input, func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		_, err := audit.AppendTx(ctx, tx, audit.Entry{
			Action: policyPurposeRepresentativeLink, ResourceType: "party.company_representative", ResourceID: id,
			Outcome: audit.OutcomeSuccess, RetentionUntil: policy.RetentionUntil,
			Detail: []byte(fmt.Sprintf("{%q:%q,%q:%q}", "purpose_code", policy.PurposeCode, "legal_basis_code", policy.LegalBasisCode)),
		})
		return err
	})
}

func (r *Repository) createRepresentative(ctx context.Context, input RepresentativeCreate, appendAudit func(context.Context, pgx.Tx, uuid.UUID) error) (Representative, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Representative{}, err
	}
	role := strings.TrimSpace(input.Role)

	var result Representative
	err = r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = createRepresentativeTx(ctx, tx, tenantID, input, role)
		if err == nil && appendAudit != nil {
			return appendAudit(ctx, tx, result.ID)
		}
		return err
	})
	if err != nil {
		return Representative{}, fmt.Errorf("vincular representante: %w", err)
	}
	return result, nil
}

func createRepresentativeTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input RepresentativeCreate, role string) (Representative, error) {
	if err := validateRepresentativeCompanyTx(ctx, tx, tenantID, input.CompanyID); err != nil {
		return Representative{}, err
	}
	person, err := findRepresentativePersonTx(ctx, tx, tenantID, input.PersonID)
	if err != nil {
		return Representative{}, err
	}
	valid, err := representativeRoleExists(ctx, tx, role)
	if err != nil {
		return Representative{}, err
	}
	if !valid {
		return Representative{}, ErrRepresentativeRoleInvalid
	}

	if err := lockPartyCreate(ctx, tx, "representative", tenantID, input.CompanyID.String()); err != nil {
		return Representative{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM party.company_representative
			WHERE tenant_id = $1 AND company_party_id = $2 AND person_party_id = $3 AND role = $4)`,
		tenantID, input.CompanyID, input.PersonID, role).Scan(&exists); err != nil {
		return Representative{}, err
	}
	if exists {
		return Representative{}, ErrRepresentativeAlreadyExists
	}

	// Existing companies may lack the extension referenced by the link's FK.
	if _, err := tx.Exec(ctx, `
		INSERT INTO party.party_company (tenant_id, party_id)
		VALUES ($1, $2) ON CONFLICT (party_id) DO NOTHING`, tenantID, input.CompanyID); err != nil {
		return Representative{}, err
	}

	result := Representative{TenantID: tenantID, CompanyID: input.CompanyID, PersonID: input.PersonID, Role: role, Person: person}
	if err := tx.QueryRow(ctx, `
		INSERT INTO party.company_representative (tenant_id, company_party_id, person_party_id, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, tenantID, input.CompanyID, input.PersonID, role).Scan(&result.ID); err != nil {
		return Representative{}, err
	}
	return result, nil
}

func listRepresentativesTx(ctx context.Context, tx pgx.Tx, tenantID, companyID uuid.UUID) ([]Representative, error) {
	rows, err := tx.Query(ctx, `
		SELECT cr.id, cr.tenant_id, cr.company_party_id, cr.person_party_id, cr.role,
		       p.kind, p.name, pe.social_name
		FROM party.company_representative cr
		JOIN party.party p ON p.tenant_id = cr.tenant_id AND p.id = cr.person_party_id
		LEFT JOIN party.person pe ON pe.tenant_id = cr.tenant_id AND pe.party_id = cr.person_party_id
		WHERE cr.tenant_id = $1 AND cr.company_party_id = $2
		ORDER BY cr.id`, tenantID, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Representative
	for rows.Next() {
		var item Representative
		var social pgtype.Text
		if err := rows.Scan(&item.ID, &item.TenantID, &item.CompanyID, &item.PersonID, &item.Role,
			&item.Person.Kind, &item.Person.Name, &social); err != nil {
			return nil, err
		}
		item.Person.ID = item.PersonID
		item.Person.TenantID = item.TenantID
		item.Person.SocialName = socialNameFromText(social)
		result = append(result, item)
	}
	return result, rows.Err()
}

// findPartyForAttachmentTx confirms a party of any kind exists in the tenant,
// for contacts and addresses that apply equally to natural persons and
// companies. A missing party or one in another tenant both surface as
// ErrNotFound, avoiding cross-tenant existence disclosure.
func findPartyForAttachmentTx(ctx context.Context, tx pgx.Tx, tenantID, partyID uuid.UUID) (Party, error) {
	if partyID == uuid.Nil {
		return Party{}, ErrNotFound
	}
	var result Party
	err := tx.QueryRow(ctx, `
		SELECT id, tenant_id, kind, name, status FROM party.party WHERE tenant_id = $1 AND id = $2`,
		tenantID, partyID).Scan(&result.ID, &result.TenantID, &result.Kind, &result.Name, &result.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrNotFound
	}
	if err != nil {
		return Party{}, fmt.Errorf("buscar party: %w", err)
	}
	return result, nil
}

func validateRepresentativeCompanyTx(ctx context.Context, tx pgx.Tx, tenantID, companyID uuid.UUID) error {
	var document string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(document, '') FROM party.party
		WHERE tenant_id = $1 AND id = $2 AND kind = $3`, tenantID, companyID, KindCompany).Scan(&document)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLegalEntityNotFound
	}
	if err != nil {
		return fmt.Errorf("buscar pessoa jurídica: %w", err)
	}
	_, err = NormalizeCNPJ(document)
	return err
}

// findRepresentativePersonTx confirms a natural person exists in the tenant.
// A missing person or one in another tenant both surface as
// ErrRepresentativePersonNotFound, avoiding cross-tenant existence
// disclosure. A company offered as a person surfaces as ErrPartyKindInvalid.
func findRepresentativePersonTx(ctx context.Context, tx pgx.Tx, tenantID, personID uuid.UUID) (Party, error) {
	if personID == uuid.Nil {
		return Party{}, ErrRepresentativePersonNotFound
	}
	var result Party
	var social pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.tenant_id, p.kind, p.name, pe.social_name
		FROM party.party p
		LEFT JOIN party.person pe ON pe.tenant_id = p.tenant_id AND pe.party_id = p.id
		WHERE p.tenant_id = $1 AND p.id = $2`, tenantID, personID).Scan(
		&result.ID, &result.TenantID, &result.Kind, &result.Name, &social)
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrRepresentativePersonNotFound
	}
	if err != nil {
		return Party{}, fmt.Errorf("buscar pessoa representante: %w", err)
	}
	if result.Kind != KindPerson {
		return Party{}, ErrPartyKindInvalid
	}
	var personRowExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM party.person WHERE tenant_id = $1 AND party_id = $2)`,
		tenantID, personID).Scan(&personRowExists); err != nil {
		return Party{}, fmt.Errorf("validar pessoa representante: %w", err)
	}
	if !personRowExists {
		return Party{}, ErrRepresentativePersonNotFound
	}
	result.SocialName = socialNameFromText(social)
	return result, nil
}

func representativeRoleExists(ctx context.Context, tx pgx.Tx, role string) (bool, error) {
	var valid bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM identity.domain_value
			WHERE domain_code = 'party_representative_role' AND code = $1 AND active
		)`, role).Scan(&valid); err != nil {
		return false, fmt.Errorf("validar papel de representante: %w", err)
	}
	return valid, nil
}
