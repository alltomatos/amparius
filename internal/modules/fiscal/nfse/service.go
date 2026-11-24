package nfse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

// AttemptStore persiste a trilha crua da tentativa. A implementacao sobre o
// repositorio fiscal usa db.WithTenantTx internamente (RegisterEmissionAttempt).
type AttemptStore interface {
	Register(ctx context.Context, attempt fiscal.EmissionAttempt) (fiscal.EmissionAttempt, error)
	GetByKey(ctx context.Context, idempotencyKey string) (fiscal.EmissionAttempt, error)
}

type repositoryAttemptStore struct{ repository *fiscal.Repository }

func NewRepositoryAttemptStore(repository *fiscal.Repository) AttemptStore {
	return &repositoryAttemptStore{repository: repository}
}

func (s *repositoryAttemptStore) Register(ctx context.Context, attempt fiscal.EmissionAttempt) (fiscal.EmissionAttempt, error) {
	return s.repository.RegisterEmissionAttempt(ctx, attempt)
}

func (s *repositoryAttemptStore) GetByKey(ctx context.Context, idempotencyKey string) (fiscal.EmissionAttempt, error) {
	return s.repository.GetEmissionAttempt(ctx, idempotencyKey)
}

// ServiceInput e montado pelo caso de uso autenticado. Nenhum campo e confiado
// diretamente a um corpo HTTP sem resolucao de sessao/tenant.
type ServiceInput struct {
	DocumentID                           uuid.UUID
	Competence                           time.Time
	TenantID, CompanyID, EstablishmentID uuid.UUID
	IdempotencyKey                       string
	DPS                                  DPSInput
}

// ServiceResult resume o desfecho sem expor corpo cru nem XML.
type ServiceResult struct {
	Status      string
	AccessKey   string
	IDDps       string
	RequestHash string
}

// ServiceConfig injeta todas as dependencias; Logger nunca recebe XML/PII.
type ServiceConfig struct {
	Repository   *fiscal.Repository
	Adapter      Adapter
	Validator    Validator
	AttemptStore AttemptStore
	Logger       *slog.Logger
	Now          func() time.Time
}

// Service orquestra montagem, validacao, transporte e persistencia da fatia 01.
type Service struct {
	config ServiceConfig
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Repository == nil {
		return nil, errors.New("repositorio fiscal nulo")
	}
	if config.Adapter == nil {
		return nil, errors.New("adapter NFS-e nulo")
	}
	if config.Validator == nil {
		config.Validator = NewLocalValidator()
	}
	if config.AttemptStore == nil {
		config.AttemptStore = NewRepositoryAttemptStore(config.Repository)
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{config: config}, nil
}

// Emit e idempotente por (tenant, idempotency_key) e por idDps. Antes de
// qualquer reenvio consulta GetDPSStatus; timeout deixa o documento pendente
// (contingencia offline = fila, nunca autorizacao, ADR 0010 decisao 5).
func (s *Service) Emit(ctx context.Context, input ServiceInput) (ServiceResult, error) {
	var result ServiceResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, err := db.TenantFromContext(ctx); err != nil {
		return result, fmt.Errorf("emissao NFS-e exige tenant no contexto: %w", err)
	}
	if err := validateServiceInput(input); err != nil {
		return result, err
	}
	input.DPS.IDDps = strings.TrimSpace(input.DPS.IDDps)
	dps, err := BuildDPS(ctx, input.DPS)
	if err != nil {
		return result, err
	}
	validacao, err := s.config.Validator.ValidateDPS(ctx, dps)
	if err != nil {
		return result, err
	}
	if !validacao.Valid {
		return result, fmt.Errorf("%w: %s", ErrInvalidDPS, strings.Join(validacao.Errors, "; "))
	}
	result.IDDps = dps.IDDps
	result.RequestHash = dps.XMLHash

	if existente, err := s.config.AttemptStore.GetByKey(ctx, input.IdempotencyKey); err == nil && existente.Result == "authorized" {
		return s.resultFromAttempt(existente, dps), nil
	} else if err != nil && !errors.Is(err, fiscal.ErrAttemptNotFound) {
		return result, fmt.Errorf("consultar tentativa anterior: %w", err)
	}

	status, err := s.config.Adapter.GetDPSStatus(ctx, dps.IDDps)
	if err != nil {
		return s.pending(ctx, input, dps, result), err
	}
	if status.Exists {
		return s.reconcile(ctx, input, dps, result, status.AccessKey)
	}

	request := EmitRequest{
		TenantID: input.TenantID.String(), CompanyID: input.CompanyID.String(),
		EstablishmentID: input.EstablishmentID.String(), Competence: input.Competence.UTC(),
		IDDps: dps.IDDps, IdempotencyKey: input.IdempotencyKey,
		Environment: input.DPS.Environment, SignedXML: dps.XML,
	}
	resposta, err := s.config.Adapter.EmitDPS(ctx, request)
	if err != nil {
		return s.pending(ctx, input, dps, result), err
	}
	if err := s.persistAuthorization(ctx, input, dps, resposta); err != nil {
		return result, err
	}
	s.config.Logger.Info("nfse emissao autorizada", "document_id", input.DocumentID.String(), "id_dps", dps.IDDps, "http_status", resposta.HTTPStatus)
	return ServiceResult{Status: "authorized", AccessKey: resposta.AccessKey, IDDps: dps.IDDps, RequestHash: dps.XMLHash}, nil
}

func (s *Service) reconcile(ctx context.Context, input ServiceInput, dps DPS, result ServiceResult, accessKey string) (ServiceResult, error) {
	resposta := EmitResult{HTTPStatus: 201, AccessKey: accessKey, IDDps: dps.IDDps, Environment: input.DPS.Environment,
		ResponseBody:        []byte(fmt.Sprintf("{\"reconciliado\":true,\"chaveAcesso\":%q}", accessKey)),
		ResponseContentType: "application/json"}
	resposta.ResponseHash = SHA256Hex(resposta.ResponseBody)
	if err := s.persistAuthorization(ctx, input, dps, resposta); err != nil {
		return result, err
	}
	return ServiceResult{Status: "authorized", AccessKey: accessKey, IDDps: dps.IDDps, RequestHash: dps.XMLHash}, nil
}

func (s *Service) pending(ctx context.Context, input ServiceInput, dps DPS, result ServiceResult) ServiceResult {
	attempt := fiscal.EmissionAttempt{
		DocumentID: input.DocumentID, Competence: input.Competence.UTC(), Attempt: 1,
		IdempotencyKey: input.IdempotencyKey, Mode: "contingency", Result: "pending",
		DPSID: ptrString(dps.IDDps), RequestHash: ptrString(dps.XMLHash),
	}
	if _, err := s.config.AttemptStore.Register(ctx, attempt); err != nil {
		s.config.Logger.Error("nfse falha ao registrar pendencia", "document_id", input.DocumentID.String(), "erro", err.Error())
	}
	s.config.Logger.Warn("nfse emissao pendente sem autorizacao", "document_id", input.DocumentID.String(), "id_dps", dps.IDDps)
	return ServiceResult{Status: "pending", IDDps: dps.IDDps, RequestHash: dps.XMLHash}
}

func (s *Service) persistAuthorization(ctx context.Context, input ServiceInput, dps DPS, resposta EmitResult) error {
	status := resposta.HTTPStatus
	ambiente := resposta.Environment
	chave := resposta.AccessKey
	dpsID := dps.IDDps
	requestHash := dps.XMLHash
	corpo := string(resposta.ResponseBody)
	responseHash := resposta.ResponseHash
	if responseHash == "" {
		responseHash = SHA256Hex(resposta.ResponseBody)
	}
	contentType := resposta.ResponseContentType
	protocolo := dps.IDDps
	_, err := s.config.AttemptStore.Register(ctx, fiscal.EmissionAttempt{
		DocumentID: input.DocumentID, Competence: input.Competence.UTC(), Attempt: 1,
		IdempotencyKey: input.IdempotencyKey, Mode: "normal", Result: "authorized",
		Protocol: &protocolo, ResponseBody: &corpo, ResponseHash: &responseHash,
		ResponseContentType: &contentType, HTTPStatus: &status, Environment: &ambiente,
		AccessKey: &chave, DPSID: &dpsID, RequestHash: &requestHash,
		AttemptedAt: s.config.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("persistir tentativa autorizada: %w", err)
	}
	return nil
}

func (s *Service) resultFromAttempt(attempt fiscal.EmissionAttempt, dps DPS) ServiceResult {
	accessKey := ""
	if attempt.AccessKey != nil {
		accessKey = *attempt.AccessKey
	}
	return ServiceResult{Status: "authorized", AccessKey: accessKey, IDDps: dps.IDDps, RequestHash: dps.XMLHash}
}

func validateServiceInput(input ServiceInput) error {
	if input.DocumentID == uuid.Nil || input.Competence.IsZero() || input.TenantID == uuid.Nil ||
		input.CompanyID == uuid.Nil || input.EstablishmentID == uuid.Nil ||
		strings.TrimSpace(input.IdempotencyKey) == "" {
		return fmt.Errorf("%w: identificadores da emissao ausentes", ErrInvalidDPS)
	}
	if input.DPS.IDDps == "" || input.DPS.Competence.IsZero() || len(input.DPS.Items) == 0 {
		return fmt.Errorf("%w: DPS incompleta", ErrInvalidDPS)
	}
	return nil
}

func ptrString(value string) *string { return &value }
