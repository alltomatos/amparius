package party

import (
	"errors"
	"regexp"
	"strings"
)

// ErrInvalidCNPJ é devolvido quando o CNPJ informado não tem o formato ou os
// dígitos verificadores válidos (numérico tradicional ou alfanumérico 2026).
var ErrInvalidCNPJ = errors.New("party: cnpj inválido")

const cnpjLength = 14

var cnpjAllowedPattern = regexp.MustCompile(`^[0-9A-Z]{12}[0-9]{2}$`)

var (
	cnpjCheckDigit1Weights = []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	cnpjCheckDigit2Weights = []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
)

// NormalizeCNPJ remove máscara, converte para maiúsculas e valida os dígitos
// verificadores. Aceita tanto o CNPJ numérico tradicional quanto o formato
// alfanumérico adotado em 2026 (dígitos verificadores continuam numéricos).
// Devolve o CNPJ normalizado em 14 posições, sem pontuação.
func NormalizeCNPJ(raw string) (string, error) {
	cleaned := canonicalCNPJText(raw)
	if !cnpjAllowedPattern.MatchString(cleaned) {
		return "", ErrInvalidCNPJ
	}
	if !validateCNPJCheckDigits(cleaned) {
		return "", ErrInvalidCNPJ
	}
	return cleaned, nil
}

func canonicalCNPJText(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch r {
		case '.', '-', '/', ' ':
			continue
		default:
			if r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validateCNPJCheckDigits(cnpj string) bool {
	if len(cnpj) != cnpjLength {
		return false
	}
	dv1 := cnpjCheckDigit(cnpj[:12], cnpjCheckDigit1Weights)
	dv2 := cnpjCheckDigit(cnpj[:13], cnpjCheckDigit2Weights)
	return cnpj[12] == dv1 && cnpj[13] == dv2
}

// cnpjCheckDigit segue o dígito verificador módulo 11 oficial. No CNPJ
// alfanumérico (2026) cada caractere entra na soma pelo valor ASCII menos 48
// (Nota Técnica do CNPJ alfanumérico): para '0'-'9' isso reproduz o valor
// numérico tradicional; para 'A'-'Z' resulta em 17-42.
func cnpjCheckDigit(base string, weights []int) byte {
	sum := 0
	for i := 0; i < len(base); i++ {
		sum += int(base[i]-48) * weights[i]
	}
	remainder := sum % 11
	if remainder < 2 {
		return '0'
	}
	return byte('0' + (11 - remainder))
}
