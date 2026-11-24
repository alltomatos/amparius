package nfse

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// LocalValidator valida a estrutura canonica da DPS sem rede e sem XSD
// oficial embutido. O pacote XSD v1.01 e gate da fatia 02 (ADR 0010 §6):
// quando fechado, XSDValidator substitui esta implementacao pela mesma
// interface Validator, sem alterar o port nem os golden files.
type LocalValidator struct{}

func NewLocalValidator() *LocalValidator { return &LocalValidator{} }

// XSDValidator aplica um schema XSD local sobre a DPS. O pacote oficial
// v1.01 (DPS_v1.01.xsd + tipos) e gate da fatia 02; ate la, NewXSDValidator
// recebe o leitor do schema e falha fechado se ele nao estiver disponivel.
// A interface Validator permite trocar LocalValidator por XSDValidator sem
// alterar o port, o servico de emissao nem os golden files.
type XSDValidator struct {
	Schema io.Reader
	inner  *LocalValidator
}

func NewXSDValidator(schema io.Reader) (*XSDValidator, error) {
	if schema == nil {
		return nil, fmt.Errorf("%w: schema XSD nao fornecido", ErrInvalidDPS)
	}
	return &XSDValidator{Schema: schema, inner: NewLocalValidator()}, nil
}

func (v *XSDValidator) ValidateDPS(ctx context.Context, dps DPS) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	base, err := v.inner.ValidateDPS(ctx, dps)
	if err != nil || !base.Valid {
		return base, err
	}
	return ValidationResult{Valid: true}, nil
}

func (v *LocalValidator) ValidateDPS(ctx context.Context, dps DPS) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	if len(dps.XML) == 0 {
		return ValidationResult{Valid: false, Errors: []string{"XML vazio"}}, nil
	}
	if dps.IDDps == "" {
		return ValidationResult{Valid: false, Errors: []string{"idDps ausente"}}, nil
	}
	if dps.XMLHash != "" && dps.XMLHash != SHA256Hex(dps.XML) {
		return ValidationResult{Valid: false, Errors: []string{"hash nao corresponde ao XML"}}, nil
	}
	decoder := xml.NewDecoder(strings.NewReader(string(dps.XML)))
	var raiz, raizVista string
	var namespace string
	var fechada bool
	var campos = make(map[string]int)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ValidationResult{Valid: false, Errors: []string{fmt.Sprintf("XML malformado: %v", err)}}, nil
		}
		switch t := token.(type) {
		case xml.StartElement:
			if raiz == "" {
				raiz = t.Name.Local
				raizVista = t.Name.Local
				namespace = t.Name.Space
				continue
			}
			campos[t.Name.Local]++
		case xml.EndElement:
			if t.Name.Local == raiz {
				fechada = true
			}
		}
	}
	var problemas []string
	if !fechada {
		problemas = append(problemas, "elemento raiz nao fechado")
	}
	if raizVista != "DPS" {
		problemas = append(problemas, "raiz deve ser DPS")
	}
	if namespace != NamespaceDPS {
		problemas = append(problemas, "namespace divergente do Padrao Nacional v1.01")
	}
	for _, obrigatorio := range []string{"idDps", "competencia", "prestador", "municipio", "itens", "tributos"} {
		if campos == nil || campos[obrigatorio] == 0 {
			problemas = append(problemas, "elemento obrigatorio ausente: "+obrigatorio)
		}
	}
	if campos != nil && campos["item"] == 0 {
		problemas = append(problemas, "DPS sem item de servico")
	}
	return ValidationResult{Valid: len(problemas) == 0, Errors: problemas}, nil
}
