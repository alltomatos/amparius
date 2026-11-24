// Package nfse implementa a fatia 01 da NFS-e nacional: contrato do adapter,
// montagem canonica da DPS e validacao local, sem transmissao real.
package nfse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// EnvironmentProducaoRestrita e o primeiro ambiente de rede previsto no
	// ADR 0010; nenhuma transmissao ocorre nesta fatia.
	EnvironmentProducaoRestrita = "producao_restrita"
	EnvironmentProducao         = "producao"
	// NamespaceDPS e o namespace oficial do Padrão Nacional v1.01.
	NamespaceDPS = "http://www.sped.fazenda.gov.br/nfse"
)

var (
	ErrAdapterUnavailable   = errors.New("adaptador NFS-e indisponivel")
	ErrIdempotencyConflict  = errors.New("conflito de idempotencia")
	ErrDocumentRejectedType = errors.New("documento rejeitado pelo fisco")
	ErrInvalidDPS           = errors.New("DPS invalida")
	ErrTaxRuleNotFound      = errors.New("regra tributaria nao encontrada para a competencia")
)

// ErrDocumentRejected carrega codigo e descricao oficiais da rejeicao.
type ErrDocumentRejected struct {
	Code        string
	Description string
}

func (e *ErrDocumentRejected) Error() string {
	return ErrDocumentRejectedType.Error() + ": " + e.Code + " " + e.Description
}

func (e *ErrDocumentRejected) Unwrap() error { return ErrDocumentRejectedType }

// DPSInput e montada pelo caso de uso a partir de documento/itens ja
// modelados. Valores de tributo vem de tax_calc, nunca de constante aqui.
type DPSInput struct {
	IDDps                             string
	Competence                        time.Time
	EmitterDocument, MunicipalityCode string
	Series                            string
	Number                            int64
	IdempotencyKey                    string
	Environment                       string
	TaxCalculations                   []TaxCalculationInput
	Items                             []DPSItem
}

// TaxCalculationInput e o snapshot ja calculado e persistido em tax_calc.
type TaxCalculationInput struct {
	TaxType, Rate  string
	BaseCents      int64
	AmountCents    int64
	RuleSourceHash string
}

// DPSItem descreve um servico; valores monetarios em centavos.
type DPSItem struct {
	ServiceCode, Description string
	Quantity                 string
	UnitPriceCents           int64
	TotalCents               int64
}

// DPS e o resultado canonico: XML determinisco + hash SHA-256 + idDps.
type DPS struct {
	XML     []byte
	XMLHash string
	IDDps   string
}

// EmitRequest e a entrada do transporte; o XML ja vem montado e validado.
type EmitRequest struct {
	TenantID, CompanyID, EstablishmentID string
	Competence                           time.Time
	IDDps, IdempotencyKey, Environment   string
	SignedXML                            []byte
}

// EmitResult e a resposta crua do transporte, sem interpretacao fiscal.
type EmitResult struct {
	HTTPStatus          int
	AccessKey           string
	NFSXML              []byte
	ResponseBody        []byte
	ResponseHash        string
	ResponseContentType string
	IDDps               string
	Environment         string
}

// DPSStatus resume a reconciliacao pelo idDps.
type DPSStatus struct {
	Exists    bool
	AccessKey string
}

// MunicipalParameter e usado apenas para conferencia; nao substitui tax_rule.
type MunicipalParameter struct {
	MunicipalityCode, ServiceCode string
	Competence                    time.Time
	Raw                           []byte
}

// validationResult e o retorno da validacao XSD local.
type ValidationResult struct {
	Valid  bool
	Errors []string
}

// Validator valida a DPS localmente, sem rede.
type Validator interface {
	ValidateDPS(ctx context.Context, dps DPS) (ValidationResult, error)
}

// Adapter e o port do Padrão Nacional. Nao calcula tributo, nao transmite
// nesta fatia e todas as operacoes recebem context.Context primeiro.
type Adapter interface {
	EmitDPS(ctx context.Context, request EmitRequest) (EmitResult, error)
	GetNFSByKey(ctx context.Context, accessKey string) (EmitResult, error)
	GetDPSStatus(ctx context.Context, idDps string) (DPSStatus, error)
	ValidateDPS(ctx context.Context, dps DPS) (ValidationResult, error)
	MunicipalParameters(ctx context.Context, municipalityCode, serviceCode string, competence time.Time) (MunicipalParameter, error)
}

// BuildDPS monta a DPS canonica. Itens e calculos sao ordenados para que a
// mesma entrada produza sempre os mesmos bytes e o mesmo hash.
func BuildDPS(ctx context.Context, input DPSInput) (DPS, error) {
	if err := ctx.Err(); err != nil {
		return DPS{}, err
	}
	if err := validateInput(input); err != nil {
		return DPS{}, err
	}
	items := append([]DPSItem(nil), input.Items...)
	sort.Slice(items, func(i, j int) bool { return lessItem(items[i], items[j]) })
	calcs := append([]TaxCalculationInput(nil), input.TaxCalculations...)
	sort.Slice(calcs, func(i, j int) bool { return lessCalc(calcs[i], calcs[j]) })

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<DPS xmlns="` + NamespaceDPS + `" versao="1.01">`)
	b.WriteString(`<idDps>` + escape(input.IDDps) + `</idDps>`)
	b.WriteString(`<competencia>` + input.Competence.UTC().Format("2006-01-02") + `</competencia>`)
	b.WriteString(`<prestador><documento>` + escape(input.EmitterDocument) + `</documento></prestador>`)
	b.WriteString(`<municipio>` + escape(input.MunicipalityCode) + `</municipio>`)
	b.WriteString(`<serie>` + escape(input.Series) + `</serie>`)
	b.WriteString(`<numero>` + fmt.Sprintf("%d", input.Number) + `</numero>`)
	b.WriteString(`<itens>`)
	for _, item := range items {
		b.WriteString(`<item>`)
		b.WriteString(`<codigoServico>` + escape(item.ServiceCode) + `</codigoServico>`)
		b.WriteString(`<descricao>` + escape(item.Description) + `</descricao>`)
		b.WriteString(`<quantidade>` + escape(item.Quantity) + `</quantidade>`)
		b.WriteString(`<valorUnitarioCentavos>` + fmt.Sprintf("%d", item.UnitPriceCents) + `</valorUnitarioCentavos>`)
		b.WriteString(`<valorTotalCentavos>` + fmt.Sprintf("%d", item.TotalCents) + `</valorTotalCentavos>`)
		b.WriteString(`</item>`)
	}
	b.WriteString(`</itens>`)
	b.WriteString(`<tributos>`)
	for _, calc := range calcs {
		b.WriteString(`<calculo>`)
		b.WriteString(`<tipo>` + escape(calc.TaxType) + `</tipo>`)
		if calc.Rate != "" {
			b.WriteString(`<aliquota>` + escape(calc.Rate) + `</aliquota>`)
		}
		b.WriteString(`<baseCentavos>` + fmt.Sprintf("%d", calc.BaseCents) + `</baseCentavos>`)
		b.WriteString(`<valorCentavos>` + fmt.Sprintf("%d", calc.AmountCents) + `</valorCentavos>`)
		if calc.RuleSourceHash != "" {
			b.WriteString(`<ruleSourceHash>` + escape(calc.RuleSourceHash) + `</ruleSourceHash>`)
		}
		b.WriteString(`</calculo>`)
	}
	b.WriteString(`</tributos>`)
	b.WriteString(`</DPS>`)
	raw := []byte(b.String())
	return DPS{XML: raw, XMLHash: SHA256Hex(raw), IDDps: input.IDDps}, nil
}

func validateInput(input DPSInput) error {
	if strings.TrimSpace(input.IDDps) == "" || input.Competence.IsZero() ||
		strings.TrimSpace(input.EmitterDocument) == "" || strings.TrimSpace(input.MunicipalityCode) == "" ||
		input.Number <= 0 || len(input.Items) == 0 {
		return fmt.Errorf("%w: campos obrigatorios ausentes", ErrInvalidDPS)
	}
	if input.Environment != "" && input.Environment != EnvironmentProducaoRestrita && input.Environment != EnvironmentProducao {
		return fmt.Errorf("%w: ambiente desconhecido", ErrInvalidDPS)
	}
	for _, item := range input.Items {
		if strings.TrimSpace(item.ServiceCode) == "" || strings.TrimSpace(item.Description) == "" ||
			strings.TrimSpace(item.Quantity) == "" || item.UnitPriceCents < 0 || item.TotalCents < 0 {
			return fmt.Errorf("%w: item invalido", ErrInvalidDPS)
		}
	}
	return nil
}

func lessItem(a, b DPSItem) bool {
	if a.ServiceCode != b.ServiceCode {
		return a.ServiceCode < b.ServiceCode
	}
	if a.Description != b.Description {
		return a.Description < b.Description
	}
	if a.Quantity != b.Quantity {
		return a.Quantity < b.Quantity
	}
	if a.UnitPriceCents != b.UnitPriceCents {
		return a.UnitPriceCents < b.UnitPriceCents
	}
	return a.TotalCents < b.TotalCents
}

func lessCalc(a, b TaxCalculationInput) bool {
	if a.TaxType != b.TaxType {
		return a.TaxType < b.TaxType
	}
	if a.Rate != b.Rate {
		return a.Rate < b.Rate
	}
	if a.BaseCents != b.BaseCents {
		return a.BaseCents < b.BaseCents
	}
	if a.AmountCents != b.AmountCents {
		return a.AmountCents < b.AmountCents
	}
	return a.RuleSourceHash < b.RuleSourceHash
}

func escape(value string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

// SHA256Hex devolve o hash hexadecimal minusculo usado em request_hash,
// response_hash e xml_hash.
func SHA256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
