package nfse_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/modules/fiscal"
	"github.com/alltomatos/amparius/internal/modules/fiscal/nfse"
	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
)

// memoryAttemptStore e um AttemptStore em memoria, sem Postgres, usado para
// isolar a regra de negocio de idempotencia no nivel do Service (AGENTS.md
// #3) sem depender de testcontainers.
type memoryAttemptStore struct {
	mu       sync.Mutex
	attempts []fiscal.EmissionAttempt
	document fiscal.Document
}

func newMemoryAttemptStore(documentID uuid.UUID, competence time.Time) *memoryAttemptStore {
	return &memoryAttemptStore{document: fiscal.Document{ID: documentID, Competence: competence, Status: "pending"}}
}

func (m *memoryAttemptStore) Register(_ context.Context, attempt fiscal.EmissionAttempt) (fiscal.EmissionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts = append(m.attempts, attempt)
	switch attempt.Result {
	case "authorized":
		m.document.Status = "authorized"
	case "rejected":
		m.document.Status = "rejected"
	}
	return attempt, nil
}

func (m *memoryAttemptStore) GetByKey(_ context.Context, idempotencyKey string) (fiscal.EmissionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.attempts {
		if a.IdempotencyKey == idempotencyKey {
			return a, nil
		}
	}
	return fiscal.EmissionAttempt{}, errors.New("tentativa nao encontrada")
}

func (m *memoryAttemptStore) List(_ context.Context, documentID uuid.UUID, competence time.Time) ([]fiscal.EmissionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []fiscal.EmissionAttempt
	for _, a := range m.attempts {
		if a.DocumentID == documentID && a.Competence.Equal(competence) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *memoryAttemptStore) GetDocument(_ context.Context, _ uuid.UUID, _ time.Time) (fiscal.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.document, nil
}

func entradaIdempotencia(documentID, tenant, company, establishment uuid.UUID, competence time.Time, idDps, key, descricao string) nfse.ServiceInput {
	return nfse.ServiceInput{
		DocumentID: documentID, Competence: competence,
		TenantID: tenant, CompanyID: company, EstablishmentID: establishment,
		IdempotencyKey: key,
		DPS: nfse.DPSInput{
			IDDps: idDps, Competence: competence,
			EmitterDocument: "00000000000191", MunicipalityCode: "2304400",
			Series: "1", Number: 1, Environment: nfse.EnvironmentProducaoRestrita,
			Items: []nfse.DPSItem{{ServiceCode: "25.01", Description: descricao, Quantity: "1", UnitPriceCents: 15000, TotalCents: 15000}},
		},
	}
}

// TestEmitReusoChaveComPayloadDiferenteEhConflito reproduz o cenario do QA de
// Contrato na issue #27: reemitir o mesmo DocumentID/Competence com a mesma
// chave de idempotencia, mas descricao (e portanto XML/hash) diferente do
// autorizado, deve falhar com ErrIdempotencyConflict — nunca devolver
// "authorized" com um RequestHash novo e divergente do persistido.
func TestEmitReusoChaveComPayloadDiferenteEhConflito(t *testing.T) {
	ctx := db.WithTenant(context.Background(), uuid.New())
	tenant, _ := db.TenantFromContext(ctx)
	documentID, company, establishment := uuid.New(), uuid.New(), uuid.New()
	competence := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	store := newMemoryAttemptStore(documentID, competence)
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository:   &fiscal.Repository{},
		Adapter:      nfse.NewFakeAdapter(nfse.FakeConfig{}),
		Validator:    nfse.NewLocalValidator(),
		AttemptStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}

	original := entradaIdempotencia(documentID, tenant, company, establishment, competence, "DPS-IDEMP-001", "idemp-key-conflito", "Servico original")
	primeiro, err := servico.Emit(ctx, original)
	if err != nil || primeiro.Status != "authorized" {
		t.Fatalf("primeira emissao deveria autorizar: %+v err=%v", primeiro, err)
	}

	divergente := entradaIdempotencia(documentID, tenant, company, establishment, competence, "DPS-IDEMP-001", "idemp-key-conflito", "Servico DIFERENTE do original")
	segundo, err := servico.Emit(ctx, divergente)
	if !errors.Is(err, nfse.ErrIdempotencyConflict) {
		t.Fatalf("reuso de chave com payload diferente deveria ser ErrIdempotencyConflict, veio: resultado=%+v err=%v", segundo, err)
	}
	if segundo.Status == "authorized" {
		t.Fatalf("reuso de chave com payload diferente nunca pode reautorizar silenciosamente: %+v", segundo)
	}

	// Replay identico (mesmo hash) continua idempotente: devolve o resultado
	// persistido, sem duplicar e sem exigir novo transporte.
	replay, err := servico.Emit(ctx, original)
	if err != nil || replay.Status != "authorized" || replay.AccessKey != primeiro.AccessKey {
		t.Fatalf("replay identico deveria repetir a autorizacao original: %+v err=%v", replay, err)
	}

	historico, err := store.List(ctx, documentID, competence)
	if err != nil || len(historico) != 1 {
		t.Fatalf("nem o conflito nem o replay deveriam persistir nova tentativa: %d err=%v", len(historico), err)
	}
}

// TestEmitReusoChaveComPayloadDiferenteAposRejeicaoEhConflito cobre o mesmo
// caminho para uma tentativa anterior rejeitada: reuso de chave com payload
// diferente tambem e conflito, nao a rejeicao persistida repetida.
func TestEmitReusoChaveComPayloadDiferenteAposRejeicaoEhConflito(t *testing.T) {
	ctx := db.WithTenant(context.Background(), uuid.New())
	tenant, _ := db.TenantFromContext(ctx)
	documentID, company, establishment := uuid.New(), uuid.New(), uuid.New()
	competence := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	store := newMemoryAttemptStore(documentID, competence)
	servico, err := nfse.NewService(nfse.ServiceConfig{
		Repository:   &fiscal.Repository{},
		Adapter:      nfse.NewFakeAdapter(nfse.FakeConfig{RejectOn: []string{"DPS-IDEMP-002"}}),
		Validator:    nfse.NewLocalValidator(),
		AttemptStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}

	original := entradaIdempotencia(documentID, tenant, company, establishment, competence, "DPS-IDEMP-002", "idemp-key-rejeitado", "Servico original")
	var rejeicao *nfse.ErrDocumentRejected
	primeiro, err := servico.Emit(ctx, original)
	if !errors.As(err, &rejeicao) || primeiro.Status != "rejected" {
		t.Fatalf("primeira emissao deveria rejeitar: %+v err=%v", primeiro, err)
	}

	divergente := entradaIdempotencia(documentID, tenant, company, establishment, competence, "DPS-IDEMP-002", "idemp-key-rejeitado", "Servico DIFERENTE do original")
	segundo, err := servico.Emit(ctx, divergente)
	if !errors.Is(err, nfse.ErrIdempotencyConflict) {
		t.Fatalf("reuso de chave com payload diferente apos rejeicao deveria ser ErrIdempotencyConflict: resultado=%+v err=%v", segundo, err)
	}
}
