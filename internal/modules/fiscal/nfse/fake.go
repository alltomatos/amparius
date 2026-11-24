package nfse

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// FakeConfig parametriza o adapter fake deterministico. RejectOn forca 400,
// TimeoutOn simula resposta perdida apos a nota ter sido gerada (cenario de
// reconciliacao do ADR 0010). Nenhuma lista e obrigatoria.
type FakeConfig struct {
	RejectOn  []string
	TimeoutOn []string
	// Now e injetada para manter timestamps determinísticos nos testes.
	Now func() time.Time
}

// FakeAdapter e um adapter em memoria, sem rede, sem certificado e sem
// segredo. Implementa integralmente o port para a fatia 01.
type FakeAdapter struct {
	mu        sync.Mutex
	config    FakeConfig
	porDPS    map[string]EmitResult
	porChave  map[string]EmitResult
	porKey    map[string]string
	emissoes  map[string]int
	validator Validator
}

func NewFakeAdapter(config FakeConfig) *FakeAdapter {
	if config.Now == nil {
		config.Now = func() time.Time { return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC) }
	}
	sort.Strings(config.RejectOn)
	sort.Strings(config.TimeoutOn)
	return &FakeAdapter{
		config: config, porDPS: make(map[string]EmitResult), porChave: make(map[string]EmitResult),
		porKey: make(map[string]string), emissoes: make(map[string]int), validator: NewLocalValidator(),
	}
}

func (f *FakeAdapter) EmitDPS(ctx context.Context, request EmitRequest) (EmitResult, error) {
	if err := ctx.Err(); err != nil {
		return EmitResult{}, err
	}
	if err := validateEmitRequest(request); err != nil {
		return EmitResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if existente, ok := f.porDPS[request.IDDps]; ok {
		return existente, nil
	}
	if chave, ok := f.porKey[request.IdempotencyKey]; ok && chave != request.IDDps {
		return EmitResult{}, fmt.Errorf("%w: chave reutilizada por idDps diverso", ErrIdempotencyConflict)
	}
	if contains(f.config.RejectOn, request.IDDps) {
		return EmitResult{}, &ErrDocumentRejected{Code: "E001", Description: "DPS rejeitada na validacao oficial"}
	}
	resultado := f.buildResult(request)
	if contains(f.config.TimeoutOn, request.IDDps) {
		f.registrar(request, resultado)
		f.emissoes[request.IDDps] = 1
		return EmitResult{}, fmt.Errorf("%w: timeout antes da resposta 201", ErrAdapterUnavailable)
	}
	f.registrar(request, resultado)
	f.emissoes[request.IDDps]++
	return resultado, nil
}

func (f *FakeAdapter) GetDPSStatus(ctx context.Context, idDps string) (DPSStatus, error) {
	if err := ctx.Err(); err != nil {
		return DPSStatus{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	resultado, ok := f.porDPS[idDps]
	if !ok {
		return DPSStatus{Exists: false}, nil
	}
	return DPSStatus{Exists: true, AccessKey: resultado.AccessKey}, nil
}

func (f *FakeAdapter) GetNFSByKey(ctx context.Context, accessKey string) (EmitResult, error) {
	if err := ctx.Err(); err != nil {
		return EmitResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	resultado, ok := f.porChave[accessKey]
	if !ok {
		return EmitResult{}, fmt.Errorf("%w: chave de acesso nao encontrada", ErrAdapterUnavailable)
	}
	return resultado, nil
}

func (f *FakeAdapter) ValidateDPS(ctx context.Context, dps DPS) (ValidationResult, error) {
	return f.validator.ValidateDPS(ctx, dps)
}

func (f *FakeAdapter) MunicipalParameters(ctx context.Context, municipalityCode, serviceCode string, competence time.Time) (MunicipalParameter, error) {
	if err := ctx.Err(); err != nil {
		return MunicipalParameter{}, err
	}
	if strings.TrimSpace(municipalityCode) == "" || strings.TrimSpace(serviceCode) == "" || competence.IsZero() {
		return MunicipalParameter{}, fmt.Errorf("%w: parametros municipais invalidos", ErrInvalidDPS)
	}
	raw := []byte(fmt.Sprintf("{\"municipalityCode\":%q,\"serviceCode\":%q,\"competence\":%q,\"source\":\"conferencia-local\"}",
		municipalityCode, serviceCode, competence.UTC().Format("2006-01-02")))
	return MunicipalParameter{MunicipalityCode: municipalityCode, ServiceCode: serviceCode, Competence: competence.UTC(), Raw: raw}, nil
}

// EmitCount expoe quantas emissoes efetivas ocorreram para um idDps. Usado
// apenas por testes: prova que replay nao duplicou documento.
func (f *FakeAdapter) EmitCount(idDps string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.emissoes[idDps]
}

func (f *FakeAdapter) buildResult(request EmitRequest) EmitResult {
	accessKey := "NFS-" + SHA256Hex([]byte(request.IDDps + request.IdempotencyKey))[:32]
	corpo := []byte(fmt.Sprintf("{\"chaveAcesso\":%q,\"idDps\":%q,\"ambiente\":%q,\"status\":201,\"timestamp\":%q}",
		accessKey, request.IDDps, request.Environment, f.config.Now().UTC().Format(time.RFC3339Nano)))
	return EmitResult{
		HTTPStatus: 201, AccessKey: accessKey,
		NFSXML:       []byte(fmt.Sprintf(`<NFS-e xmlns=%q><chaveAcesso>%s</chaveAcesso></NFS-e>`, NamespaceDPS, accessKey)),
		ResponseBody: corpo, ResponseHash: SHA256Hex(corpo), ResponseContentType: "application/json",
		IDDps: request.IDDps, Environment: request.Environment,
	}
}

func (f *FakeAdapter) registrar(request EmitRequest, resultado EmitResult) {
	f.porDPS[request.IDDps] = resultado
	f.porChave[resultado.AccessKey] = resultado
	f.porKey[request.IdempotencyKey] = request.IDDps
}

func validateEmitRequest(request EmitRequest) error {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.CompanyID) == "" ||
		strings.TrimSpace(request.IDDps) == "" || strings.TrimSpace(request.IdempotencyKey) == "" ||
		request.Competence.IsZero() || len(request.SignedXML) == 0 {
		return fmt.Errorf("%w: requisicao de emissao incompleta", ErrInvalidDPS)
	}
	if request.Environment != EnvironmentProducaoRestrita && request.Environment != EnvironmentProducao {
		return fmt.Errorf("%w: ambiente desconhecido", ErrInvalidDPS)
	}
	return nil
}

func contains(list []string, value string) bool {
	i := sort.SearchStrings(list, value)
	return i < len(list) && list[i] == value
}
