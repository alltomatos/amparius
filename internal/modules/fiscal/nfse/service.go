package nfse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

// AttemptStore persiste a trilha crua da tentativa e le o historico
// append-only. A implementacao sobre o repositorio fiscal usa
// db.WithTenantTx internamente, com RLS por tenant.
type AttemptStore interface {
	Register(ctx context.Context, attempt fiscal.EmissionAttempt) (fiscal.EmissionAttempt, error)
	GetByKey(ctx context.Context, idempotencyKey string) (fiscal.EmissionAttempt, error)
	List(ctx context.Context, documentID uuid.UUID, competence time.Time) ([]fiscal.EmissionAttempt, error)
	GetDocument(ctx context.Context, id uuid.UUID, competence time.Time) (fiscal.Document, error)
}

type repositoryAttemptStore struct{ repository *fiscal.Repository }

func NewRepositoryAttemptStore(repository *fiscal.Repository) AttemptStore {
	return &repositoryAttemptStore{repository: repository}
}

func (s *repositoryAttemptStore) Register(ctx context.Context, attempt fiscal.EmissionAttempt) (fiscal.EmissionAttempt, error) {
	return s.repository.RegisterEmissionAttempt(ctx, attempt)
}

func (s *repositoryAttemptStore) RegisterWithDocument(ctx context.Context, attempt fiscal.EmissionAttempt, xmlRaw []byte) (fiscal.EmissionAttempt, error) {
	return s.repository.RegisterEmissionAttemptWithDocument(ctx, attempt, xmlRaw)
}

func (s *repositoryAttemptStore) GetByKey(ctx context.Context, idempotencyKey string) (fiscal.EmissionAttempt, error) {
	return s.repository.GetEmissionAttempt(ctx, idempotencyKey)
}

func (s *repositoryAttemptStore) List(ctx context.Context, documentID uuid.UUID, competence time.Time) ([]fiscal.EmissionAttempt, error) {
	return s.repository.ListEmissionAttempts(ctx, documentID, competence)
}

func (s *repositoryAttemptStore) GetDocument(ctx context.Context, id uuid.UUID, competence time.Time) (fiscal.Document, error) {
	return s.repository.GetDocument(ctx, id, competence)
}

// ServiceInput e montado pelo caso de uso autenticado. Nenhum campo e confiado
// diretamente a um corpo HTTP sem resolucao de sessao/tenant.
type ServiceInput struct {
	DocumentID                           uuid.UUID
	Competence                           time.Time
	TenantID, CompanyID, EstablishmentID uuid.UUID
	IdempotencyKey                       string
	DPS                                  DPSInput
	// Official, quando presente, seleciona o builder no leiaute oficial
	// v1.01 (BuildDPSOficial) em vez do builder estrutural. O Validator
	// injetado deve ser o XSDValidator para esse caminho.
	Official *OfficialDPSInput
}

// ServiceResult resume o desfecho sem expor corpo cru nem XML. Em rejeicao o
// erro tipado acompanha o resultado; em autorizacao o status nunca e offline.
type ServiceResult struct {
	Status               string
	AccessKey            string
	IDDps                string
	RequestHash          string
	RejectionCode        string
	RejectionDescription string
}

// StatusInput e a chave de leitura do estado persistido de uma emissao.
type StatusInput struct {
	DocumentID uuid.UUID
	Competence time.Time
}

// RejectionInfo carrega o codigo e a descricao oficiais ja persistidos.
type RejectionInfo struct {
	Code        string
	Description string
}

// StatusResult e a leitura consultavel do ciclo de emissao. Ele nao transmite,
// nao reenvia e nao inventa estado: vem do documento e das tentativas
// append-only.
type StatusResult struct {
	DocumentStatus string
	AttemptResult  string
	Attempts       int
	AccessKey      string
	IDDps          string
	HTTPStatus     *int
	Environment    string
	Rejection      *RejectionInfo
	LastAttemptAt  *time.Time
}

var (
	ErrStatusUnavailable = errors.New("status de emissao indisponivel")
)

// ServiceConfig injeta todas as dependencias; Logger nunca recebe XML/PII.
type ServiceConfig struct {
	Repository   *fiscal.Repository
	Adapter      Adapter
	Validator    Validator
	AttemptStore AttemptStore
	DANFSe       DANFSeRenderer
	Logger       *slog.Logger
	Now          func() time.Time
}

// Service orquestra montagem, validacao, transporte, fechamento e leitura de
// status da NFS-e. Nenhum caminho marca documento como autorizado sem uma
// resposta de autorizacao do adapter (ADR 0010, decisao 5).
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
	if config.DANFSe == nil {
		config.DANFSe = NewFakeDANFSeRenderer()
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{config: config}, nil
}

const (
	attemptKeySuffix   = ":attempt-"
	reconcileKeySuffix = ":reconcile-"
)

// Emit e idempotente pelo fechamento persistido: se ja existe autorizacao ou
// rejeicao no historico do documento, o pedido termina sem novo transporte.
// Antes de reenviar consulta GetDPSStatus; timeout deixa o documento pendente
// (contingencia offline = fila, nunca autorizacao, ADR 0010 decisao 5) e a
// reconciliacao grava uma tentativa propria, preservando a anterior.
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
	var dps DPS
	var err error
	if input.Official != nil {
		if input.DPS.Environment == "" {
			input.DPS.Environment = EnvironmentProducaoRestrita
		}
		dps, err = BuildDPSOficial(ctx, *input.Official)
	} else {
		dps, err = BuildDPS(ctx, input.DPS)
	}
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

	attempts, err := s.config.AttemptStore.List(ctx, input.DocumentID, input.Competence.UTC())
	if err != nil {
		return result, fmt.Errorf("consultar historico de emissao: %w", err)
	}
	if autorizado := latestAttempt(attempts, "authorized"); autorizado != nil {
		return s.resultFromAttempt(*autorizado, dps)
	}
	if rejeitado := latestAttempt(attempts, "rejected"); rejeitado != nil {
		return s.resultFromRejected(*rejeitado, dps)
	}

	next := nextAttemptNumber(attempts)

	status, err := s.config.Adapter.GetDPSStatus(ctx, dps.IDDps)
	if err != nil {
		return s.pending(ctx, input, dps, result, next, attemptKey(input.IdempotencyKey, next)), err
	}
	if status.Exists {
		return s.reconcile(ctx, input, dps, result, status.AccessKey, next)
	}

	request := EmitRequest{
		TenantID: input.TenantID.String(), CompanyID: input.CompanyID.String(),
		EstablishmentID: input.EstablishmentID.String(), Competence: input.Competence.UTC(),
		IDDps: dps.IDDps, IdempotencyKey: input.IdempotencyKey,
		Environment: input.DPS.Environment, SignedXML: dps.XML,
	}
	resposta, err := s.config.Adapter.EmitDPS(ctx, request)
	if err != nil {
		var rejeicao *ErrDocumentRejected
		if errors.As(err, &rejeicao) {
			return s.persistRejection(ctx, input, dps, next, attemptKey(input.IdempotencyKey, next), rejeicao)
		}
		return s.pending(ctx, input, dps, result, next, attemptKey(input.IdempotencyKey, next)), err
	}
	authorizedKey := attemptKey(input.IdempotencyKey, next)
	if err := s.persistAuthorization(ctx, input, dps, resposta, next, authorizedKey); err != nil {
		return result, err
	}
	s.config.Logger.Info("nfse emissao autorizada", "document_id", input.DocumentID.String(), "id_dps", dps.IDDps, "http_status", resposta.HTTPStatus)
	return ServiceResult{Status: "authorized", AccessKey: resposta.AccessKey, IDDps: dps.IDDps, RequestHash: dps.XMLHash}, nil
}

// Status devolve o estado persistido do documento e a ultima tentativa. Falha
// fechado quando o documento nao existe ou nao pertence ao tenant do contexto.
func (s *Service) Status(ctx context.Context, input StatusInput) (StatusResult, error) {
	var result StatusResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, err := db.TenantFromContext(ctx); err != nil {
		return result, fmt.Errorf("consulta de status exige tenant no contexto: %w", err)
	}
	if input.DocumentID == uuid.Nil || input.Competence.IsZero() {
		return result, fmt.Errorf("%w: documento/competencia ausentes", ErrStatusUnavailable)
	}
	document, err := s.config.AttemptStore.GetDocument(ctx, input.DocumentID, input.Competence.UTC())
	if err != nil {
		return result, err
	}
	attempts, err := s.config.AttemptStore.List(ctx, input.DocumentID, input.Competence.UTC())
	if err != nil {
		return result, fmt.Errorf("consultar historico de emissao: %w", err)
	}
	result.DocumentStatus = document.Status
	result.Attempts = len(attempts)
	if latest := latestAttemptByNumber(attempts); latest != nil {
		result.AttemptResult = latest.Result
		result.HTTPStatus = latest.HTTPStatus
		result.AccessKey = derefString(latest.AccessKey)
		result.IDDps = derefString(latest.DPSID)
		result.Environment = derefString(latest.Environment)
		at := latest.AttemptedAt
		result.LastAttemptAt = &at
		if latest.Result == "rejected" {
			if code, description, ok := parseRejection(*latest); ok {
				result.Rejection = &RejectionInfo{Code: code, Description: description}
			}
		}
	}
	if autorizado := latestAttempt(attempts, "authorized"); autorizado != nil {
		if key := derefString(autorizado.AccessKey); key != "" {
			result.AccessKey = key
		}
	}
	return result, nil
}

// RenderDANFSe monta o contrato de impressao a partir de uma NFS-e ja
// autorizada e persistida. Nao transmite, nao assina e nao cria documento
// fiscal: apenas entrega bytes deterministicos de um renderer injetado.
func (s *Service) RenderDANFSe(ctx context.Context, input DANFSeInput) (DANFSeDocument, error) {
	if err := ctx.Err(); err != nil {
		return DANFSeDocument{}, err
	}
	tenant, err := db.TenantFromContext(ctx)
	if err != nil {
		return DANFSeDocument{}, fmt.Errorf("DANFSe exige tenant no contexto: %w", err)
	}
	if input.DocumentID == uuid.Nil || input.Competence.IsZero() {
		return DANFSeDocument{}, fmt.Errorf("%w: documento/competencia ausentes", ErrInvalidDANFSeRequest)
	}
	document, err := s.config.AttemptStore.GetDocument(ctx, input.DocumentID, input.Competence.UTC())
	if err != nil {
		return DANFSeDocument{}, err
	}
	if document.Status != "authorized" {
		return DANFSeDocument{}, ErrDANFSeNotAuthorized
	}
	attempts, err := s.config.AttemptStore.List(ctx, input.DocumentID, input.Competence.UTC())
	if err != nil {
		return DANFSeDocument{}, fmt.Errorf("consultar historico de emissao: %w", err)
	}
	autorizado := latestAttempt(attempts, "authorized")
	if autorizado == nil {
		return DANFSeDocument{}, ErrDANFSeNotAuthorized
	}
	issuedAt := time.Time{}
	if document.IssuedAt != nil {
		issuedAt = *document.IssuedAt
	}
	if issuedAt.IsZero() {
		issuedAt = autorizado.AttemptedAt
	}
	return s.config.DANFSe.Render(ctx, DANFSeRequest{
		TenantID: tenant, CompanyID: document.CompanyID, EstablishmentID: document.EstablishmentID,
		AccessKey: derefString(autorizado.AccessKey), IDDps: derefString(autorizado.DPSID),
		Environment: derefString(autorizado.Environment), Competence: document.Competence.UTC(),
		IssuedAt:         issuedAt.UTC(),
		DPSXML:           append([]byte(nil), autorizado.RequestXML...),
		AuthorizationRaw: append([]byte(nil), autorizado.ResponseRaw...),
	})
}

func (s *Service) reconcile(ctx context.Context, input ServiceInput, dps DPS, result ServiceResult, accessKey string, next int) (ServiceResult, error) {
	resposta := EmitResult{HTTPStatus: 201, AccessKey: accessKey, IDDps: dps.IDDps, Environment: input.DPS.Environment,
		ResponseBody:        []byte(fmt.Sprintf("{\"reconciliado\":true,\"chaveAcesso\":%q}", accessKey)),
		ResponseContentType: "application/json"}
	resposta.ResponseHash = SHA256Hex(resposta.ResponseBody)
	if err := s.persistAuthorization(ctx, input, dps, resposta, next, reconcileKey(input.IdempotencyKey, next)); err != nil {
		return result, err
	}
	return ServiceResult{Status: "authorized", AccessKey: accessKey, IDDps: dps.IDDps, RequestHash: dps.XMLHash}, nil
}

func (s *Service) pending(ctx context.Context, input ServiceInput, dps DPS, result ServiceResult, number int, key string) ServiceResult {
	attempt := fiscal.EmissionAttempt{
		DocumentID: input.DocumentID, Competence: input.Competence.UTC(), Attempt: number,
		IdempotencyKey: key, Mode: "contingency", Result: "pending",
		DPSID: ptrString(dps.IDDps), RequestHash: ptrString(dps.XMLHash),
	}
	if _, err := registerAttemptWithDocument(s.config.AttemptStore, ctx, attempt, dps.XML); err != nil {
		s.config.Logger.Error("nfse falha ao registrar pendencia", "document_id", input.DocumentID.String(), "erro", err.Error())
	}
	s.config.Logger.Warn("nfse emissao pendente sem autorizacao", "document_id", input.DocumentID.String(), "id_dps", dps.IDDps)
	return ServiceResult{Status: "pending", IDDps: dps.IDDps, RequestHash: dps.XMLHash}
}

func (s *Service) persistAuthorization(ctx context.Context, input ServiceInput, dps DPS, resposta EmitResult, number int, key string) error {
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
	attempt := fiscal.EmissionAttempt{
		DocumentID: input.DocumentID, Competence: input.Competence.UTC(), Attempt: number,
		IdempotencyKey: key, Mode: "normal", Result: "authorized",
		Protocol: &protocolo, ResponseBody: &corpo, ResponseHash: &responseHash,
		ResponseContentType: &contentType, HTTPStatus: &status, Environment: &ambiente,
		AccessKey: &chave, DPSID: &dpsID, RequestHash: &requestHash,
		RequestXML: append([]byte(nil), dps.XML...), ResponseRaw: append([]byte(nil), resposta.ResponseBody...),
		AttemptedAt: s.config.Now().UTC(),
	}
	_, err := registerAttemptWithDocument(s.config.AttemptStore, ctx, attempt, dps.XML)
	if err != nil {
		return fmt.Errorf("persistir tentativa autorizada: %w", err)
	}
	return nil
}

// persistRejection grava a rejeicao oficial como tentativa append-only e
// fecha o documento como rejected. A resposta crua e o codigo/descricao ficam
// consultaveis; replay nao reenvia.
func (s *Service) persistRejection(ctx context.Context, input ServiceInput, dps DPS, number int, key string, rejeicao *ErrDocumentRejected) (ServiceResult, error) {
	corpo := rejectionBody(rejeicao)
	hash := SHA256Hex(corpo)
	statusHTTP := 400
	ambiente := input.DPS.Environment
	dpsID := dps.IDDps
	requestHash := dps.XMLHash
	contentType := "application/json"
	protocolo := dps.IDDps
	attempt := fiscal.EmissionAttempt{
		DocumentID: input.DocumentID, Competence: input.Competence.UTC(), Attempt: number,
		IdempotencyKey: key, Mode: "normal", Result: "rejected",
		Protocol: &protocolo, ResponseBody: ptrString(string(corpo)), ResponseHash: &hash,
		ResponseContentType: &contentType, HTTPStatus: &statusHTTP, Environment: &ambiente,
		DPSID: &dpsID, RequestHash: &requestHash,
		RequestXML: append([]byte(nil), dps.XML...), ResponseRaw: append([]byte(nil), corpo...),
		AttemptedAt: s.config.Now().UTC(),
	}
	if _, err := registerAttemptWithDocument(s.config.AttemptStore, ctx, attempt, dps.XML); err != nil {
		return ServiceResult{}, fmt.Errorf("persistir rejeicao NFS-e: %w", err)
	}
	s.config.Logger.Warn("nfse emissao rejeitada", "document_id", input.DocumentID.String(), "id_dps", dps.IDDps, "codigo", rejeicao.Code)
	return ServiceResult{
		Status: "rejected", IDDps: dps.IDDps, RequestHash: dps.XMLHash,
		RejectionCode: rejeicao.Code, RejectionDescription: rejeicao.Description,
	}, &ErrDocumentRejected{Code: rejeicao.Code, Description: rejeicao.Description}
}

type documentAttemptStore interface {
	RegisterWithDocument(context.Context, fiscal.EmissionAttempt, []byte) (fiscal.EmissionAttempt, error)
}

func registerAttemptWithDocument(store AttemptStore, ctx context.Context, attempt fiscal.EmissionAttempt, xmlRaw []byte) (fiscal.EmissionAttempt, error) {
	if transactional, ok := store.(documentAttemptStore); ok {
		return transactional.RegisterWithDocument(ctx, attempt, xmlRaw)
	}
	return store.Register(ctx, attempt)
}

// resultFromAttempt devolve o fechamento persistido para a mesma chave de
// idempotencia/documento. Reuso da chave com payload identico (mesmo hash) e
// replay legitimo; payload diferente e conflito, nunca reautorizacao
// silenciosa (AGENTS.md #3).
func (s *Service) resultFromAttempt(attempt fiscal.EmissionAttempt, dps DPS) (ServiceResult, error) {
	if derefString(attempt.RequestHash) != dps.XMLHash {
		return ServiceResult{}, fmt.Errorf("%w: chave de idempotencia reutilizada com payload diferente do autorizado", ErrIdempotencyConflict)
	}
	return ServiceResult{
		Status: "authorized", AccessKey: derefString(attempt.AccessKey),
		IDDps: dps.IDDps, RequestHash: dps.XMLHash,
	}, nil
}

// resultFromRejected aplica a mesma regra de idempotencia do caminho
// autorizado: so repete a rejeicao persistida quando o hash do novo request
// coincide com o hash original.
func (s *Service) resultFromRejected(attempt fiscal.EmissionAttempt, dps DPS) (ServiceResult, error) {
	if derefString(attempt.RequestHash) != dps.XMLHash {
		return ServiceResult{}, fmt.Errorf("%w: chave de idempotencia reutilizada com payload diferente do rejeitado", ErrIdempotencyConflict)
	}
	code, description, _ := parseRejection(attempt)
	result := ServiceResult{
		Status: "rejected", IDDps: dps.IDDps, RequestHash: dps.XMLHash,
		RejectionCode: code, RejectionDescription: description,
	}
	return result, &ErrDocumentRejected{Code: code, Description: description}
}

type rejectionPayload struct {
	Codigo    string `json:"codigo"`
	Descricao string `json:"descricao"`
}

func rejectionBody(rejeicao *ErrDocumentRejected) []byte {
	raw, _ := json.Marshal(rejectionPayload{Codigo: rejeicao.Code, Descricao: rejeicao.Description})
	return raw
}

func parseRejection(attempt fiscal.EmissionAttempt) (string, string, bool) {
	if attempt.ResponseBody == nil || strings.TrimSpace(*attempt.ResponseBody) == "" {
		return "", "", false
	}
	var payload rejectionPayload
	if err := json.Unmarshal([]byte(*attempt.ResponseBody), &payload); err != nil {
		return "", "", false
	}
	if strings.TrimSpace(payload.Codigo) == "" && strings.TrimSpace(payload.Descricao) == "" {
		return "", "", false
	}
	return payload.Codigo, payload.Descricao, true
}

func latestAttempt(attempts []fiscal.EmissionAttempt, result string) *fiscal.EmissionAttempt {
	var best *fiscal.EmissionAttempt
	for i := range attempts {
		if attempts[i].Result != result {
			continue
		}
		if best == nil || attempts[i].Attempt > best.Attempt {
			best = &attempts[i]
		}
	}
	return best
}

func latestAttemptByNumber(attempts []fiscal.EmissionAttempt) *fiscal.EmissionAttempt {
	var best *fiscal.EmissionAttempt
	for i := range attempts {
		if best == nil || attempts[i].Attempt > best.Attempt {
			best = &attempts[i]
		}
	}
	return best
}

func nextAttemptNumber(attempts []fiscal.EmissionAttempt) int {
	next := 1
	for i := range attempts {
		if attempts[i].Attempt >= next {
			next = attempts[i].Attempt + 1
		}
	}
	return next
}

func attemptKey(base string, number int) string {
	if number <= 1 {
		return base
	}
	return fmt.Sprintf("%s%s%d", base, attemptKeySuffix, number)
}

func reconcileKey(base string, number int) string {
	return fmt.Sprintf("%s%s%d", base, reconcileKeySuffix, number)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateServiceInput(input ServiceInput) error {
	if input.DocumentID == uuid.Nil || input.Competence.IsZero() || input.TenantID == uuid.Nil ||
		input.CompanyID == uuid.Nil || input.EstablishmentID == uuid.Nil ||
		strings.TrimSpace(input.IdempotencyKey) == "" {
		return fmt.Errorf("%w: identificadores da emissao ausentes", ErrInvalidDPS)
	}
	if input.Official == nil {
		if input.DPS.IDDps == "" || input.DPS.Competence.IsZero() || len(input.DPS.Items) == 0 {
			return fmt.Errorf("%w: DPS incompleta", ErrInvalidDPS)
		}
	}
	return nil
}

func ptrString(value string) *string { return &value }
