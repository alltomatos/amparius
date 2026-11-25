// Package audit persiste evidências tenant-scoped sem transportar PII.
// Detalhes são códigos, referências opacas e valores operacionais pequenos;
// dados pessoais devem permanecer no módulo de origem.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	platformauth "github.com/alltomatos/amparius/internal/platform/auth"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/alltomatos/amparius/internal/platform/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidEntry  = errors.New("entrada de auditoria inválida")
	ErrPIIInDetail   = errors.New("detalhe de auditoria contém PII ou segredo")
	ErrActorConflict = errors.New("ator de auditoria diverge do principal autenticado")
	codePattern      = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{1,119}$`)
	keyPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	evidencePattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,255}$`)
)

type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeDenied  Outcome = "denied"
	OutcomeFailure Outcome = "failure"
)

// Entry é um evento de auditoria. Tenant, actor e trace são resolvidos do
// contexto sempre que possível; nenhum campo aceita texto livre de domínio.
type Entry struct {
	ActorUserID    uuid.UUID
	ActorKind      string
	CompanyID      uuid.UUID
	Action         string
	ResourceType   string
	ResourceID     uuid.UUID
	Outcome        Outcome
	Detail         json.RawMessage
	OccurredAt     time.Time
	RetentionUntil time.Time
}

type Consent struct {
	SubjectType    string
	SubjectID      uuid.UUID
	PurposeCode    string
	LegalBasisCode string
	Status         string
	EvidenceRef    string
	RecordedByUser uuid.UUID
	RecordedAt     time.Time
	RetentionUntil time.Time
}

type Repository struct{ database *db.DB }

func NewRepository(database *db.DB) (*Repository, error) {
	if database == nil {
		return nil, errors.New("banco de auditoria nulo")
	}
	return &Repository{database: database}, nil
}

func (r *Repository) Append(ctx context.Context, entry Entry) (uuid.UUID, error) {
	if r == nil || r.database == nil {
		return uuid.Nil, errors.New("repositório de auditoria indisponível")
	}
	var id uuid.UUID
	err := r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = AppendTx(ctx, tx, entry)
		return err
	})
	return id, err
}

func AppendTx(ctx context.Context, tx pgx.Tx, entry Entry) (uuid.UUID, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if principal, principalErr := platformauth.PrincipalFromContext(ctx); principalErr == nil && principal.TenantID != tenantID {
		return uuid.Nil, ErrActorConflict
	}
	entry, err = normalizeEntry(ctx, entry)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO audit.log (
			tenant_id, actor_user_id, actor_kind, company_id, action,
			resource_type, resource_id, outcome, correlation_id, trace_id,
			detail, occurred_at, retention_until
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id`, tenantID, nullableUUID(entry.ActorUserID), entry.ActorKind,
		nullableUUID(entry.CompanyID), entry.Action, entry.ResourceType,
		nullableUUID(entry.ResourceID), entry.Outcome,
		nullableString(observability.CorrelationIDFromContext(ctx)),
		nullableString(observability.TraceIDFromContext(ctx)), entry.Detail,
		entry.OccurredAt, entry.RetentionUntil).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("gravar auditoria: %w", err)
	}
	return id, nil
}

// RecordConsent grava uma evidência append-only e sua auditoria na mesma
// transação. Retenção e base legal são obrigatórias e não recebem default.
func (r *Repository) RecordConsent(ctx context.Context, consent Consent) (uuid.UUID, error) {
	if r == nil || r.database == nil {
		return uuid.Nil, errors.New("repositório de privacidade indisponível")
	}
	var id uuid.UUID
	err := r.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = RecordConsentTx(ctx, tx, consent)
		return err
	})
	return id, err
}

func RecordConsentTx(ctx context.Context, tx pgx.Tx, consent Consent) (uuid.UUID, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	consent, actorKind, err := normalizeConsent(ctx, consent)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO privacy.consent_record (
			tenant_id, subject_type, subject_id, purpose_code, legal_basis_code,
			status, evidence_ref, recorded_by_user_id, recorded_at, retention_until
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`, tenantID, consent.SubjectType, consent.SubjectID,
		consent.PurposeCode, consent.LegalBasisCode, consent.Status,
		nullableString(consent.EvidenceRef), nullableUUID(consent.RecordedByUser),
		consent.RecordedAt, consent.RetentionUntil).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("gravar consentimento: %w", err)
	}
	_, err = AppendTx(ctx, tx, Entry{
		ActorUserID: consent.RecordedByUser, ActorKind: actorKind,
		Action: "privacy.consent.recorded", ResourceType: "privacy.consent",
		ResourceID: id, Outcome: OutcomeSuccess,
		Detail:     json.RawMessage(fmt.Sprintf(`{"purpose_code":%q,"legal_basis_code":%q,"status":%q}`, consent.PurposeCode, consent.LegalBasisCode, consent.Status)),
		OccurredAt: consent.RecordedAt, RetentionUntil: consent.RetentionUntil,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func normalizeEntry(ctx context.Context, entry Entry) (Entry, error) {
	if !codePattern.MatchString(entry.Action) || !codePattern.MatchString(entry.ResourceType) {
		return Entry{}, fmt.Errorf("%w: ação ou recurso", ErrInvalidEntry)
	}
	if entry.Outcome != OutcomeSuccess && entry.Outcome != OutcomeDenied && entry.Outcome != OutcomeFailure {
		return Entry{}, fmt.Errorf("%w: resultado", ErrInvalidEntry)
	}
	if entry.ActorKind == "" {
		entry.ActorKind = "system"
	}
	if entry.ActorKind != "user" && entry.ActorKind != "system" && entry.ActorKind != "service" {
		return Entry{}, fmt.Errorf("%w: tipo de ator", ErrInvalidEntry)
	}
	principal, principalErr := platformauth.PrincipalFromContext(ctx)
	if principalErr == nil && principal.UserID != uuid.Nil {
		if entry.ActorUserID != uuid.Nil && entry.ActorUserID != principal.UserID {
			return Entry{}, ErrActorConflict
		}
		entry.ActorUserID = principal.UserID
		entry.ActorKind = "user"
	}
	if companyID, companyErr := db.CompanyFromContext(ctx); companyErr == nil {
		if principalErr == nil && principal.CompanyID != uuid.Nil && principal.CompanyID != companyID {
			return Entry{}, ErrActorConflict
		}
		if entry.CompanyID != uuid.Nil && entry.CompanyID != companyID {
			return Entry{}, ErrActorConflict
		}
		entry.CompanyID = companyID
	}
	if entry.ActorKind == "user" && entry.ActorUserID == uuid.Nil {
		return Entry{}, fmt.Errorf("%w: usuário ausente", ErrInvalidEntry)
	}
	if entry.ActorKind != "user" && entry.ActorUserID != uuid.Nil {
		return Entry{}, fmt.Errorf("%w: ator de sistema não pode ter usuário", ErrInvalidEntry)
	}
	if entry.Detail == nil || len(entry.Detail) == 0 {
		entry.Detail = json.RawMessage(`{}`)
	}
	if err := ValidateDetail(entry.Detail); err != nil {
		return Entry{}, err
	}
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = time.Now().UTC()
	} else {
		entry.OccurredAt = entry.OccurredAt.UTC()
	}
	if entry.RetentionUntil.IsZero() || !entry.RetentionUntil.After(entry.OccurredAt) {
		return Entry{}, fmt.Errorf("%w: retenção deve ser posterior à ocorrência", ErrInvalidEntry)
	}
	return entry, nil
}

func normalizeConsent(ctx context.Context, consent Consent) (Consent, string, error) {
	if consent.SubjectID == uuid.Nil || !codePattern.MatchString(consent.SubjectType) ||
		!codePattern.MatchString(consent.PurposeCode) || !codePattern.MatchString(consent.LegalBasisCode) {
		return Consent{}, "", fmt.Errorf("%w: sujeito, finalidade ou base legal", ErrInvalidEntry)
	}
	if consent.Status != "granted" && consent.Status != "withdrawn" && consent.Status != "denied" && consent.Status != "not_required" {
		return Consent{}, "", fmt.Errorf("%w: status de consentimento", ErrInvalidEntry)
	}
	if consent.EvidenceRef != "" && !evidencePattern.MatchString(consent.EvidenceRef) {
		return Consent{}, "", fmt.Errorf("%w: evidência", ErrInvalidEntry)
	}
	principal, principalErr := platformauth.PrincipalFromContext(ctx)
	if principalErr == nil && principal.UserID != uuid.Nil {
		if consent.RecordedByUser != uuid.Nil && consent.RecordedByUser != principal.UserID {
			return Consent{}, "", ErrActorConflict
		}
		consent.RecordedByUser = principal.UserID
	}
	actorKind := "system"
	if consent.RecordedByUser != uuid.Nil {
		actorKind = "user"
	}
	if consent.RecordedAt.IsZero() {
		consent.RecordedAt = time.Now().UTC()
	} else {
		consent.RecordedAt = consent.RecordedAt.UTC()
	}
	if consent.RetentionUntil.IsZero() || !consent.RetentionUntil.After(consent.RecordedAt) {
		return Consent{}, "", fmt.Errorf("%w: retenção deve ser posterior ao registro", ErrInvalidEntry)
	}
	return consent, actorKind, nil
}

// ValidateDetail aceita somente JSON operacional e rejeita chaves/valores
// pessoais. A trigger PostgreSQL repete uma guarda independente.
func ValidateDetail(raw json.RawMessage) error {
	if len(raw) > 16384 {
		return ErrPIIInDetail
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: JSON inválido", ErrInvalidEntry)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: JSON extra", ErrInvalidEntry)
	}
	if object, ok := value.(map[string]any); !ok || object == nil {
		return fmt.Errorf("%w: detalhe deve ser objeto", ErrInvalidEntry)
	} else if err := validateNode(object); err != nil {
		return err
	}
	return nil
}

func validateNode(value any) error {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if !keyPattern.MatchString(key) || forbiddenKey(key) {
				return ErrPIIInDetail
			}
			if err := validateNode(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := validateNode(child); err != nil {
				return err
			}
		}
	case string:
		if strings.Contains(value, "@") || len([]rune(value)) > 256 || digitCount(value) >= 10 && digitCount(value) <= 14 && !hasLetter(value) {
			return ErrPIIInDetail
		}
	}
	return nil
}

func forbiddenKey(key string) bool {
	for _, token := range []string{"name", "cpf", "cnpj", "email", "phone", "address", "birth", "note", "message", "password", "secret", "token"} {
		if strings.Contains(key, token) && !strings.HasSuffix(key, "_id") && !strings.HasSuffix(key, "_ref") {
			return true
		}
	}
	return false
}

func digitCount(value string) int {
	count := 0
	for _, r := range value {
		if unicode.IsDigit(r) {
			count++
		}
	}
	return count
}

func hasLetter(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func nullableUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
