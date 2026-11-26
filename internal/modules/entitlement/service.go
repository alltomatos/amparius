package entitlement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Acoes e origens persistidas em entitlement.entitlement_event. Os codigos
// espelham os catalogos semeados por migrations/control/0001_entitlement.sql.
const (
	ActionActivated   = "activated"
	ActionDeactivated = "deactivated"
	ActionRevoked     = "revoked"
)

const (
	OriginSelfService = "self_service"
	OriginAdmin       = "admin"
)

// Status de entitlement.license_assignment.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
	StatusRevoked  = "revoked"
)

var (
	// ErrAssignmentNotFound indica que nao existe licenca daquele modulo para a
	// empresa no tenant autenticado.
	ErrAssignmentNotFound = errors.New("licenca nao encontrada para o modulo e a empresa")
	// ErrQuotaExceeded indica que a operacao passaria da cota do tenant.
	ErrQuotaExceeded = errors.New("cota de entitlement excedida")
	// ErrQuotaNotConfigured indica ausencia de cota provisionada. A ausencia
	// bloqueia (fail-closed): sem cota nao ha licenca nem acesso.
	ErrQuotaNotConfigured = errors.New("cota de entitlement nao configurada")
	// ErrCoreModuleImmutable indica tentativa de ativar/desativar/revogar um
	// modulo nucleo, que e sempre ligado e fica fora da cota.
	ErrCoreModuleImmutable = errors.New("modulo nucleo nao participa da cota")
	// ErrInvalidTransition indica uma transicao de status nao permitida, como
	// desativar uma licenca revogada.
	ErrInvalidTransition = errors.New("transicao de status de licenca invalida")
	// ErrInvalidRequest indica parametro invalido na chamada do servico.
	ErrInvalidRequest = errors.New("requisicao de entitlement invalida")
	// ErrActorRequired indica que a mudanca chegou sem o autor autenticado.
	ErrActorRequired = errors.New("autor da mudanca de entitlement ausente")
	// ErrIdempotencyConflict indica reutilizacao da chave com outro pedido.
	ErrIdempotencyConflict = errors.New("chave de idempotencia reutilizada com outro pedido")
	// ErrStaleVersion indica concorrencia otimista detectada.
	ErrStaleVersion = errors.New("versao do entitlement obsoleta")
)

// Assignment e uma licenca de modulo para uma empresa (CNPJ) do tenant.
// Uma licenca = um modulo em uma empresa; a filial herda da empresa (ADR 0007).
type Assignment struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	ModuleCode string
	CompanyID  uuid.UUID
	ValidFrom  time.Time
	ValidTo    *time.Time
	Status     string
	Version    int64
	UpdatedAt  time.Time
}

// ActivateInput descreve uma ativacao ou reativacao.
//
// ValidFrom e ValidTo sao opcionais: na ativacao de uma licenca nova, a ausencia
// de ValidFrom usa a data corrente em UTC e a ausencia de ValidTo deixa a
// vigencia aberta. Na reativacao de uma licenca existente, campos nulos
// preservam as datas originais -- reativar nunca apaga a vigencia contratada.
// ClearValidTo remove explicitamente o fim de vigencia de uma licenca existente.
type ActivateInput struct {
	ModuleCode      string
	CompanyID       uuid.UUID
	Origin          string
	ValidFrom       *time.Time
	ValidTo         *time.Time
	ClearValidTo    bool
	ExpectedVersion int64
}

// TransitionInput descreve desativacao ou revogacao.
type TransitionInput struct {
	ModuleCode      string
	CompanyID       uuid.UUID
	Origin          string
	ExpectedVersion int64
}

// ModuleQuota e a fotografia da cota de um modulo no tenant.
type ModuleQuota struct {
	ModuleCode string
	Quota      int
	InUse      int
	Configured bool
	Version    int64
}

// UserQuota e a fotografia da cota de acessos do tenant.
type UserQuota struct {
	Seats      int
	Used       int
	Configured bool
}

// EventFilter restringe a leitura da auditoria.
type EventFilter struct {
	ModuleCode string
	CompanyID  *uuid.UUID
	Origin     string
	Limit      int
}

// Event e uma linha de entitlement.entitlement_event (append-only).
type Event struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	ModuleCode string
	CompanyID  uuid.UUID
	ActorID    uuid.UUID
	Action     string
	Origin     string
	At         time.Time
}

type EventPage struct {
	Items      []Event
	NextCursor string
}

// Service aplica as mudancas de licenca no control plane e grava a auditoria
// append-only na mesma transacao. O tenant vem do contexto; o autor e a origem
// vem de quem autenticou a chamada, nunca do corpo HTTP.
type Service struct {
	database        *db.DB
	companyResolver CompanyResolver
}

// NewService cria o servico sobre o PostgreSQL do control plane.
func NewService(database *db.DB, opts ...Option) (*Service, error) {
	if database == nil {
		return nil, errors.New("banco do control plane nulo")
	}
	o := newOptions(opts)
	return &Service{database: database, companyResolver: o.companyResolver}, nil
}

// Activate liga um modulo opcional para uma empresa. Se a licenca ja existir,
// reativa preservando id e vigencia nao informada. A ocupacao da cota e
// contada a partir das licencas ativas vigentes de outras empresas, nunca de
// uma coluna digitada.
func (s *Service) Activate(ctx context.Context, actorID uuid.UUID, in ActivateInput) (Assignment, error) {
	return s.activate(ctx, actorID, "", in)
}

// ActivateIdempotent aplica a concessao/reativacao com replay persistido.
func (s *Service) ActivateIdempotent(ctx context.Context, actorID uuid.UUID, key string, in ActivateInput) (Assignment, error) {
	return s.activate(ctx, actorID, key, in)
}

func (s *Service) activate(ctx context.Context, actorID uuid.UUID, key string, in ActivateInput) (Assignment, error) {
	if err := validateActorOrigin(actorID, in.Origin); err != nil {
		return Assignment{}, err
	}
	if strings.TrimSpace(in.ModuleCode) == "" || in.CompanyID == uuid.Nil {
		return Assignment{}, fmt.Errorf("%w: modulo e empresa sao obrigatorios", ErrInvalidRequest)
	}
	if in.ClearValidTo && in.ValidTo != nil {
		return Assignment{}, fmt.Errorf("%w: ClearValidTo e ValidTo sao mutuamente exclusivos", ErrInvalidRequest)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Assignment{}, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	if err := validateCompanyWith(ctx, s.companyResolver, tenantID, in.CompanyID); err != nil {
		return Assignment{}, err
	}

	var result Assignment
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		replayed, err := beginMutation(ctx, tx, tenantID, key, "activate", actorID, in)
		if err != nil {
			return err
		}
		if replayed != nil {
			result = *replayed
			return nil
		}
		kind, active, err := loadModule(ctx, tx, in.ModuleCode)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("%w: modulo %q inativo", ErrModuleDisabled, in.ModuleCode)
		}
		if kind != moduleKindOptional {
			return fmt.Errorf("%w: modulo %q e %s", ErrCoreModuleImmutable, in.ModuleCode, kind)
		}

		var quota int
		err = tx.QueryRow(ctx, `
			SELECT quota
			  FROM entitlement.license_pool
			 WHERE tenant_id = $1
			   AND module_code = $2
			 FOR UPDATE`, tenantID, in.ModuleCode).Scan(&quota)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: modulo %q", ErrQuotaNotConfigured, in.ModuleCode)
		}
		if err != nil {
			return fmt.Errorf("%w: consultar cota do modulo: %w", ErrEntitlementUnavailable, err)
		}

		existing, found, err := loadAssignment(ctx, tx, tenantID, in.ModuleCode, in.CompanyID)
		if err != nil {
			return err
		}
		if found && in.ExpectedVersion > 0 && existing.Version != in.ExpectedVersion {
			return fmt.Errorf("%w: esperado %d, atual %d", ErrStaleVersion, in.ExpectedVersion, existing.Version)
		}

		from, to, err := resolveDates(in, existing, found)
		if err != nil {
			return err
		}

		if found && existing.Status == StatusActive &&
			in.ValidFrom == nil && in.ValidTo == nil && !in.ClearValidTo {
			result = existing
			return finishMutation(ctx, tx, tenantID, key, "activate", actorID, in, result)
		}

		var occupied int
		err = tx.QueryRow(ctx, `
			SELECT count(*)
			  FROM entitlement.license_assignment
			 WHERE tenant_id = $1
			   AND module_code = $2
			   AND status = 'active'
			   AND company_id <> $3
			   AND valid_from <= COALESCE($5::date, 'infinity'::date)
			   AND (valid_to IS NULL OR valid_to >= $4::date)`,
			tenantID, in.ModuleCode, in.CompanyID, from, to).Scan(&occupied)
		if err != nil {
			return fmt.Errorf("%w: contar licencas em uso: %w", ErrEntitlementUnavailable, err)
		}
		if occupied >= quota {
			return fmt.Errorf("%w: modulo %q usa %d de %d licencas", ErrQuotaExceeded, in.ModuleCode, occupied, quota)
		}

		if found {
			err = tx.QueryRow(ctx, `
				UPDATE entitlement.license_assignment
				   SET status = 'active',
				       valid_from = $3,
				       valid_to = $4,
				       version = version + 1,
				       updated_at = now()
				 WHERE tenant_id = $1
				   AND id = $2
				RETURNING id, tenant_id, module_code, company_id, valid_from, valid_to, status, version, updated_at`,
				tenantID, existing.ID, from, to).Scan(
				&result.ID, &result.TenantID, &result.ModuleCode, &result.CompanyID,
				&result.ValidFrom, &result.ValidTo, &result.Status, &result.Version, &result.UpdatedAt)
		} else {
			err = tx.QueryRow(ctx, `
				INSERT INTO entitlement.license_assignment
				       (tenant_id, module_code, company_id, valid_from, valid_to, status)
				VALUES ($1, $2, $3, $4, $5, 'active')
				RETURNING id, tenant_id, module_code, company_id, valid_from, valid_to, status, version, updated_at`,
				tenantID, in.ModuleCode, in.CompanyID, from, to).Scan(
				&result.ID, &result.TenantID, &result.ModuleCode, &result.CompanyID,
				&result.ValidFrom, &result.ValidTo, &result.Status, &result.Version, &result.UpdatedAt)
		}
		if err != nil {
			return fmt.Errorf("%w: gravar licenca: %w", ErrEntitlementUnavailable, err)
		}
		if err := appendEvent(ctx, tx, tenantID, in.ModuleCode, in.CompanyID, actorID, ActionActivated, in.Origin); err != nil {
			return err
		}
		return finishMutation(ctx, tx, tenantID, key, "activate", actorID, in, result)
	})
	if err != nil {
		return Assignment{}, classifyServiceError(err)
	}
	return result, nil
}

// Deactivate desliga o uso novo de um modulo preservando a licenca, a vigencia
// e todos os dados de negocio. Reativar depois restaura o estado exatamente
// como estava.
func (s *Service) Deactivate(ctx context.Context, actorID uuid.UUID, in TransitionInput) (Assignment, error) {
	return s.transition(ctx, actorID, "", in, StatusInactive, ActionDeactivated)
}

func (s *Service) DeactivateIdempotent(ctx context.Context, actorID uuid.UUID, key string, in TransitionInput) (Assignment, error) {
	return s.transition(ctx, actorID, key, in, StatusInactive, ActionDeactivated)
}

// Revoke revoga o acesso ao modulo. A linha e a vigencia sao preservadas; o
// status revogado nao pode ser desfeito por Deactivate.
func (s *Service) Revoke(ctx context.Context, actorID uuid.UUID, in TransitionInput) (Assignment, error) {
	return s.transition(ctx, actorID, "", in, StatusRevoked, ActionRevoked)
}

func (s *Service) RevokeIdempotent(ctx context.Context, actorID uuid.UUID, key string, in TransitionInput) (Assignment, error) {
	return s.transition(ctx, actorID, key, in, StatusRevoked, ActionRevoked)
}

func (s *Service) transition(ctx context.Context, actorID uuid.UUID, key string, in TransitionInput, target, action string) (Assignment, error) {
	if err := validateActorOrigin(actorID, in.Origin); err != nil {
		return Assignment{}, err
	}
	if strings.TrimSpace(in.ModuleCode) == "" || in.CompanyID == uuid.Nil {
		return Assignment{}, fmt.Errorf("%w: modulo e empresa sao obrigatorios", ErrInvalidRequest)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return Assignment{}, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	if err := validateCompanyWith(ctx, s.companyResolver, tenantID, in.CompanyID); err != nil {
		return Assignment{}, err
	}

	var result Assignment
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		replayed, err := beginMutation(ctx, tx, tenantID, key, action, actorID, in)
		if err != nil {
			return err
		}
		if replayed != nil {
			result = *replayed
			return nil
		}
		kind, active, err := loadModule(ctx, tx, in.ModuleCode)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("%w: modulo %q inativo", ErrModuleDisabled, in.ModuleCode)
		}
		if kind != moduleKindOptional {
			return fmt.Errorf("%w: modulo %q e %s", ErrCoreModuleImmutable, in.ModuleCode, kind)
		}

		existing, found, err := loadAssignment(ctx, tx, tenantID, in.ModuleCode, in.CompanyID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: modulo %q empresa %s", ErrAssignmentNotFound, in.ModuleCode, in.CompanyID)
		}
		if in.ExpectedVersion > 0 && existing.Version != in.ExpectedVersion {
			return fmt.Errorf("%w: esperado %d, atual %d", ErrStaleVersion, in.ExpectedVersion, existing.Version)
		}
		if existing.Status == target {
			result = existing
			return finishMutation(ctx, tx, tenantID, key, action, actorID, in, result)
		}
		if target == StatusInactive && existing.Status == StatusRevoked {
			return fmt.Errorf("%w: licenca revogada nao pode ser desativada", ErrInvalidTransition)
		}

		err = tx.QueryRow(ctx, `
			UPDATE entitlement.license_assignment
			   SET status = $3,
			       version = version + 1,
			       updated_at = now()
			 WHERE tenant_id = $1
			   AND id = $2
			RETURNING id, tenant_id, module_code, company_id, valid_from, valid_to, status, version, updated_at`,
			tenantID, existing.ID, target).Scan(
			&result.ID, &result.TenantID, &result.ModuleCode, &result.CompanyID,
			&result.ValidFrom, &result.ValidTo, &result.Status, &result.Version, &result.UpdatedAt)
		if err != nil {
			return fmt.Errorf("%w: atualizar licenca: %w", ErrEntitlementUnavailable, err)
		}
		if err := appendEvent(ctx, tx, tenantID, in.ModuleCode, in.CompanyID, actorID, action, in.Origin); err != nil {
			return err
		}
		return finishMutation(ctx, tx, tenantID, key, action, actorID, in, result)
	})
	if err != nil {
		return Assignment{}, classifyServiceError(err)
	}
	return result, nil
}

// ModuleQuota devolve a cota provisionada e a ocupacao vigente na data at,
// contada a partir das licencas ativas. Sem cota provisionada, Configured e
// falso e a ativacao falha fechada.
func (s *Service) ModuleQuota(ctx context.Context, moduleCode string, at time.Time) (ModuleQuota, error) {
	if strings.TrimSpace(moduleCode) == "" || at.IsZero() {
		return ModuleQuota{}, fmt.Errorf("%w: modulo e data sao obrigatorios", ErrInvalidRequest)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return ModuleQuota{}, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	result := ModuleQuota{ModuleCode: moduleCode}
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT quota, version FROM entitlement.license_pool
			 WHERE tenant_id = $1 AND module_code = $2`, tenantID, moduleCode).Scan(&result.Quota, &result.Version)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			result.Configured = false
		case err != nil:
			return fmt.Errorf("%w: consultar cota: %w", ErrEntitlementUnavailable, err)
		default:
			result.Configured = true
		}
		date := dateOnly(at)
		return tx.QueryRow(ctx, `
			SELECT count(*)
			  FROM entitlement.license_assignment
			 WHERE tenant_id = $1
			   AND module_code = $2
			   AND status = 'active'
			   AND valid_from <= $3::date
			   AND (valid_to IS NULL OR valid_to >= $3::date)`,
			tenantID, moduleCode, date).Scan(&result.InUse)
	})
	if err != nil {
		return ModuleQuota{}, classifyServiceError(err)
	}
	return result, nil
}

// SetModuleQuota mantém a API interna usada pelo provisionamento inicial.
func (s *Service) SetModuleQuota(ctx context.Context, moduleCode string, quota int) error {
	_, err := s.setModuleQuota(ctx, uuid.Nil, "", moduleCode, quota, 0)
	return err
}

// SetModuleQuotaIdempotent ajusta a quota do módulo e audita o efeito.
func (s *Service) SetModuleQuotaIdempotent(ctx context.Context, actorID uuid.UUID, key, moduleCode string, quota int, expectedVersion int64) (ModuleQuota, error) {
	if actorID == uuid.Nil {
		return ModuleQuota{}, ErrActorRequired
	}
	return s.setModuleQuota(ctx, actorID, key, moduleCode, quota, expectedVersion)
}

func (s *Service) setModuleQuota(ctx context.Context, actorID uuid.UUID, key, moduleCode string, quota int, expectedVersion int64) (ModuleQuota, error) {
	if strings.TrimSpace(moduleCode) == "" || quota < 0 {
		return ModuleQuota{}, fmt.Errorf("%w: modulo obrigatorio e cota nao negativa", ErrInvalidRequest)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return ModuleQuota{}, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	var result ModuleQuota
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		replay, err := beginMutationRaw(ctx, tx, tenantID, key, "set_module_quota", actorID, struct {
			ModuleCode string `json:"moduleCode"`
			Quota      int    `json:"quota"`
			Version    int64  `json:"version"`
		}{moduleCode, quota, expectedVersion})
		if err != nil {
			return err
		}
		if replay != nil {
			return json.Unmarshal(replay, &result)
		}
		kind, active, err := loadModule(ctx, tx, moduleCode)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("%w: modulo %q inativo", ErrModuleDisabled, moduleCode)
		}
		if kind != moduleKindOptional {
			return fmt.Errorf("%w: modulo %q e %s", ErrCoreModuleImmutable, moduleCode, kind)
		}
		var previousQuota, previousVersion int64
		err = tx.QueryRow(ctx, `
			SELECT quota, version FROM entitlement.license_pool
			 WHERE tenant_id = $1 AND module_code = $2 FOR UPDATE`, tenantID, moduleCode).Scan(&previousQuota, &previousVersion)
		configured := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: consultar cota: %w", ErrEntitlementUnavailable, err)
		}
		if configured {
			if expectedVersion > 0 && previousVersion != expectedVersion {
				return fmt.Errorf("%w: esperado %d, atual %d", ErrStaleVersion, expectedVersion, previousVersion)
			}
		} else if expectedVersion > 0 {
			return fmt.Errorf("%w: quota ainda não configurada", ErrStaleVersion)
		}
		var activeCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM entitlement.license_assignment
			 WHERE tenant_id = $1 AND module_code = $2 AND status = 'active'`,
			tenantID, moduleCode).Scan(&activeCount); err != nil {
			return fmt.Errorf("%w: contar licencas ativas: %w", ErrEntitlementUnavailable, err)
		}
		if quota < activeCount {
			return fmt.Errorf("%w: %d licencas ativas com cota %d", ErrQuotaExceeded, activeCount, quota)
		}
		if configured && previousQuota == int64(quota) {
			result = ModuleQuota{ModuleCode: moduleCode, Quota: quota, InUse: activeCount, Configured: true, Version: previousVersion}
			return finishQuotaMutation(ctx, tx, tenantID, key, actorID, moduleCode, quota, result)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO entitlement.license_pool (tenant_id, module_code, quota)
			VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, module_code)
			DO UPDATE SET quota = EXCLUDED.quota, version = entitlement.license_pool.version + 1, updated_at = now()`, tenantID, moduleCode, quota)
		if err != nil {
			return fmt.Errorf("%w: gravar cota: %w", ErrEntitlementUnavailable, err)
		}
		if actorID != uuid.Nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO entitlement.quota_event (tenant_id, module_code, actor_id, previous_quota, new_quota)
				VALUES ($1, $2, $3, $4, $5)`, tenantID, moduleCode, actorID, previousQuota, quota); err != nil {
				return fmt.Errorf("%w: gravar auditoria de quota: %w", ErrEntitlementUnavailable, err)
			}
		}
		if !configured {
			previousVersion = 0
		}
		result = ModuleQuota{ModuleCode: moduleCode, Quota: quota, InUse: activeCount, Configured: true, Version: previousVersion + 1}
		return finishQuotaMutation(ctx, tx, tenantID, key, actorID, moduleCode, quota, result)
	})
	if err != nil {
		return ModuleQuota{}, classifyServiceError(err)
	}
	return result, nil
}

func finishQuotaMutation(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key string, actorID uuid.UUID, moduleCode string, quota int, result ModuleQuota) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("%w: serializar quota: %w", ErrEntitlementUnavailable, err)
	}
	return finishMutationRaw(ctx, tx, tenantID, key, "set_module_quota", actorID, struct {
		ModuleCode string `json:"moduleCode"`
		Quota      int    `json:"quota"`
		Version    int64  `json:"version"`
	}{moduleCode, quota, result.Version}, encoded)
}

// UserQuota devolve a cota de acessos do tenant. Configured e falso quando o
// admin ainda nao provisionou assentos.
func (s *Service) UserQuota(ctx context.Context) (UserQuota, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return UserQuota{}, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	var result UserQuota
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT seats, used FROM entitlement.user_quota WHERE tenant_id = $1`, tenantID).
			Scan(&result.Seats, &result.Used)
		if errors.Is(err, pgx.ErrNoRows) {
			result = UserQuota{}
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: consultar cota de usuarios: %w", ErrEntitlementUnavailable, err)
		}
		result.Configured = true
		return nil
	})
	if err != nil {
		return UserQuota{}, classifyServiceError(err)
	}
	return result, nil
}

// SetUserSeats provisiona a cota de acessos do tenant. Nao reduz abaixo do uso
// ja reportado: isso quebraria o invariante used <= seats.
func (s *Service) SetUserSeats(ctx context.Context, seats int) error {
	if seats < 0 {
		return fmt.Errorf("%w: assentos nao podem ser negativos", ErrInvalidRequest)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var used int
		err := tx.QueryRow(ctx, `
			SELECT used FROM entitlement.user_quota WHERE tenant_id = $1 FOR UPDATE`, tenantID).Scan(&used)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: consultar cota de usuarios: %w", ErrEntitlementUnavailable, err)
		}
		if err == nil && used > seats {
			return fmt.Errorf("%w: %d assentos em uso", ErrQuotaExceeded, used)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO entitlement.user_quota (tenant_id, seats, used)
			VALUES ($1, $2, 0)
			ON CONFLICT (tenant_id)
			DO UPDATE SET seats = EXCLUDED.seats, updated_at = now()`, tenantID, seats)
		if err != nil {
			return fmt.Errorf("%w: gravar cota de usuarios: %w", ErrEntitlementUnavailable, err)
		}
		return nil
	})
	return classifyServiceError(err)
}

// ReportSeatUsage recebe do reporte de ocupacao da celula quantos assentos
// estao em uso. Exige cota provisionada e recusa ultrapassar seats.
func (s *Service) ReportSeatUsage(ctx context.Context, used int) error {
	if used < 0 {
		return fmt.Errorf("%w: uso nao pode ser negativo", ErrInvalidRequest)
	}
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var seats int
		err := tx.QueryRow(ctx, `
			SELECT seats FROM entitlement.user_quota WHERE tenant_id = $1 FOR UPDATE`, tenantID).Scan(&seats)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: assentos do tenant", ErrQuotaNotConfigured)
		}
		if err != nil {
			return fmt.Errorf("%w: consultar cota de usuarios: %w", ErrEntitlementUnavailable, err)
		}
		if used > seats {
			return fmt.Errorf("%w: %d de %d assentos", ErrQuotaExceeded, used, seats)
		}
		_, err = tx.Exec(ctx, `
			UPDATE entitlement.user_quota
			   SET used = $2, updated_at = now()
			 WHERE tenant_id = $1`, tenantID, used)
		if err != nil {
			return fmt.Errorf("%w: gravar uso de assentos: %w", ErrEntitlementUnavailable, err)
		}
		return nil
	})
	return classifyServiceError(err)
}

// ListEvents le a auditoria append-only do tenant. Limit padrao 100, teto 1000.
func (s *Service) ListEvents(ctx context.Context, filter EventFilter) ([]Event, error) {
	tenantID, err := db.TenantFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var companyArg any
	if filter.CompanyID != nil {
		companyArg = *filter.CompanyID
	}
	var events []Event
	err = s.database.WithTenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, module_code, company_id, actor_id, action, origin, "at"
			  FROM entitlement.entitlement_event
			 WHERE tenant_id = $1
			   AND ($2 = '' OR module_code = $2)
			   AND ($3::uuid IS NULL OR company_id = $3)
			 ORDER BY "at" DESC, id DESC
			 LIMIT $4`, tenantID, filter.ModuleCode, companyArg, limit)
		if err != nil {
			return fmt.Errorf("%w: consultar auditoria: %w", ErrEntitlementUnavailable, err)
		}
		defer rows.Close()
		for rows.Next() {
			var e Event
			if err := rows.Scan(&e.ID, &e.TenantID, &e.ModuleCode, &e.CompanyID, &e.ActorID, &e.Action, &e.Origin, &e.At); err != nil {
				return fmt.Errorf("%w: ler auditoria: %w", ErrEntitlementUnavailable, err)
			}
			events = append(events, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, classifyServiceError(err)
	}
	return events, nil
}

func loadModule(ctx context.Context, tx pgx.Tx, moduleCode string) (string, bool, error) {
	var kind string
	var active bool
	err := tx.QueryRow(ctx, `
		SELECT kind, active FROM entitlement.module WHERE code = $1`, moduleCode).Scan(&kind, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("%w: modulo %q inexistente", ErrModuleDisabled, moduleCode)
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: consultar modulo: %w", ErrEntitlementUnavailable, err)
	}
	return kind, active, nil
}

func loadAssignment(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, moduleCode string, companyID uuid.UUID) (Assignment, bool, error) {
	var a Assignment
	err := tx.QueryRow(ctx, `
		SELECT id, tenant_id, module_code, company_id, valid_from, valid_to, status, version, updated_at
		  FROM entitlement.license_assignment
		 WHERE tenant_id = $1
		   AND module_code = $2
		   AND company_id = $3
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1
		 FOR UPDATE`, tenantID, moduleCode, companyID).Scan(
		&a.ID, &a.TenantID, &a.ModuleCode, &a.CompanyID, &a.ValidFrom, &a.ValidTo, &a.Status, &a.Version, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Assignment{}, false, nil
	}
	if err != nil {
		return Assignment{}, false, fmt.Errorf("%w: consultar licenca: %w", ErrEntitlementUnavailable, err)
	}
	return a, true, nil
}

func resolveDates(in ActivateInput, existing Assignment, found bool) (time.Time, *time.Time, error) {
	var from time.Time
	var to *time.Time
	if found {
		from, to = existing.ValidFrom, existing.ValidTo
	} else {
		from = todayUTC()
	}
	if in.ValidFrom != nil {
		from = dateOnly(*in.ValidFrom)
	} else if found {
		from = dateOnly(existing.ValidFrom)
	}
	switch {
	case in.ClearValidTo:
		to = nil
	case in.ValidTo != nil:
		value := dateOnly(*in.ValidTo)
		to = &value
	}
	if to != nil && to.Before(from) {
		return time.Time{}, nil, fmt.Errorf("%w: valid_to anterior a valid_from", ErrInvalidRequest)
	}
	return from, to, nil
}

func appendEvent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, moduleCode string, companyID, actorID uuid.UUID, action, origin string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO entitlement.entitlement_event
		       (tenant_id, module_code, company_id, actor_id, action, origin)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		tenantID, moduleCode, companyID, actorID, action, origin)
	if err != nil {
		return fmt.Errorf("%w: gravar auditoria: %w", ErrEntitlementUnavailable, err)
	}
	return nil
}

func beginMutation(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key, operation string, actorID uuid.UUID, payload any) (*Assignment, error) {
	result, err := beginMutationRaw(ctx, tx, tenantID, key, operation, actorID, payload)
	if err != nil || result == nil {
		return nil, err
	}
	var assignment Assignment
	if err := json.Unmarshal(result, &assignment); err != nil {
		return nil, fmt.Errorf("%w: replay invalido", ErrEntitlementUnavailable)
	}
	return &assignment, nil
}

func beginMutationRaw(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key, operation string, actorID uuid.UUID, payload any) ([]byte, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil
	}
	key = strings.TrimSpace(key)
	if len(key) > 200 {
		return nil, fmt.Errorf("%w: Idempotency-Key excede 200 caracteres", ErrInvalidRequest)
	}
	hash := requestHash(operation, actorID, payload)
	if _, err := tx.Exec(ctx, `
		INSERT INTO entitlement.mutation_idempotency (tenant_id, idempotency_key, operation, request_hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, idempotency_key) DO NOTHING`, tenantID, key, operation, hash); err != nil {
		return nil, fmt.Errorf("%w: reservar idempotencia: %w", ErrEntitlementUnavailable, err)
	}
	var storedOperation, storedHash string
	var result []byte
	err := tx.QueryRow(ctx, `
		SELECT operation, request_hash, result
		  FROM entitlement.mutation_idempotency
		 WHERE tenant_id = $1 AND idempotency_key = $2
		 FOR UPDATE`, tenantID, key).Scan(&storedOperation, &storedHash, &result)
	if err != nil {
		return nil, fmt.Errorf("%w: ler idempotencia: %w", ErrEntitlementUnavailable, err)
	}
	if storedOperation != operation || storedHash != hash {
		return nil, ErrIdempotencyConflict
	}
	return result, nil
}

func finishMutation(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key, operation string, actorID uuid.UUID, payload any, result Assignment) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("%w: serializar replay: %w", ErrEntitlementUnavailable, err)
	}
	return finishMutationRaw(ctx, tx, tenantID, key, operation, actorID, payload, encoded)
}

func finishMutationRaw(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key, operation string, actorID uuid.UUID, payload any, result []byte) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	command, err := tx.Exec(ctx, `
		UPDATE entitlement.mutation_idempotency
		   SET result = $3
		 WHERE tenant_id = $1 AND idempotency_key = $2`, tenantID, strings.TrimSpace(key), result)
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = errors.New("registro de idempotencia ausente")
		}
		return fmt.Errorf("%w: gravar replay: %w", ErrEntitlementUnavailable, err)
	}
	return nil
}

func requestHash(operation string, actorID uuid.UUID, payload any) string {
	encoded, _ := json.Marshal(struct {
		Operation string    `json:"operation"`
		ActorID   uuid.UUID `json:"actorId"`
		Payload   any       `json:"payload"`
	}{operation, actorID, payload})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validateActorOrigin(actorID uuid.UUID, origin string) error {
	if actorID == uuid.Nil {
		return ErrActorRequired
	}
	switch origin {
	case OriginSelfService, OriginAdmin:
		return nil
	default:
		return fmt.Errorf("%w: origem %q desconhecida", ErrInvalidRequest, origin)
	}
}

func dateOnly(t time.Time) time.Time {
	year, month, day := t.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func todayUTC() time.Time { return dateOnly(time.Now()) }

// classifyServiceError preserva os sentinels do contrato e trata qualquer falha
// de banco nao classificada como indisponibilidade (fail-closed).
func classifyServiceError(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{
		ErrModuleDisabled,
		ErrEntitlementUnavailable,
		ErrAssignmentNotFound,
		ErrQuotaExceeded,
		ErrQuotaNotConfigured,
		ErrCoreModuleImmutable,
		ErrInvalidTransition,
		ErrInvalidRequest,
		ErrActorRequired,
		ErrIdempotencyConflict,
		ErrStaleVersion,
	} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return fmt.Errorf("%w: %v", ErrEntitlementUnavailable, err)
}
