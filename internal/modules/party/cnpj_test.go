package party

import "testing"

// Todos os CNPJs abaixo são sintéticos (sequências de teste), nunca
// documentos reais, conforme AGENTS.md #5.
func TestNormalizeCNPJValidNumeric(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"sem máscara", "12345678000195"},
		{"com máscara padrão", "11.222.333/0001-81"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := NormalizeCNPJ(tc.raw)
			if err != nil {
				t.Fatalf("esperava sucesso, obteve erro: %v", err)
			}
			if len(normalized) != 14 {
				t.Fatalf("esperava 14 posições, obteve %q", normalized)
			}
		})
	}
}

func TestNormalizeCNPJValidAlphanumeric(t *testing.T) {
	cases := []string{"1234ABCD001198", "AB123456780107", "A2CD3456000185"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			normalized, err := NormalizeCNPJ(raw)
			if err != nil {
				t.Fatalf("esperava sucesso, obteve erro: %v", err)
			}
			if normalized != raw {
				t.Fatalf("esperava %q normalizado, obteve %q", raw, normalized)
			}
		})
	}
}

func TestNormalizeCNPJOfficialCheckDigitExample(t *testing.T) {
	// Exemplo publicado no Manual de Cálculo do DV vinculado ao ADR 0044.
	got, err := NormalizeCNPJ("12.ABC.345/01DE-35")
	if err != nil || got != "12ABC34501DE35" {
		t.Fatalf("exemplo do manual: obtido %q, erro %v", got, err)
	}
}

func TestNormalizeCNPJAlphanumericWithMask(t *testing.T) {
	normalized, err := NormalizeCNPJ("1234.ABCD/0011-98")
	if err != nil {
		t.Fatalf("esperava sucesso, obteve erro: %v", err)
	}
	if normalized != "1234ABCD001198" {
		t.Fatalf("esperava normalização sem máscara, obteve %q", normalized)
	}
}

func TestNormalizeCNPJAlphanumericLowercase(t *testing.T) {
	normalized, err := NormalizeCNPJ("ab123456780107")
	if err != nil {
		t.Fatalf("esperava sucesso, obteve erro: %v", err)
	}
	if normalized != "AB123456780107" {
		t.Fatalf("esperava maiúsculas, obteve %q", normalized)
	}
}

func TestNormalizeCNPJInvalidCheckDigit(t *testing.T) {
	cases := []string{
		"12345678000196", // DV2 trocado (válido terminaria em 95)
		"1234ABCD001199", // DV2 trocado (válido terminaria em 98)
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := NormalizeCNPJ(raw); err != ErrInvalidCNPJ {
				t.Fatalf("esperava ErrInvalidCNPJ, obteve %v", err)
			}
		})
	}
}

func TestNormalizeCNPJInvalidLength(t *testing.T) {
	cases := []string{"", "123", "1234567800019", "123456780001955"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := NormalizeCNPJ(raw); err != ErrInvalidCNPJ {
				t.Fatalf("esperava ErrInvalidCNPJ, obteve %v", err)
			}
		})
	}
}

func TestNormalizeCNPJInvalidCharacters(t *testing.T) {
	cases := []string{"1234567800019$", "abcdefghijkl@#"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := NormalizeCNPJ(raw); err != ErrInvalidCNPJ {
				t.Fatalf("esperava ErrInvalidCNPJ, obteve %v", err)
			}
		})
	}
}

func TestNormalizeCNPJCheckDigitsMustBeNumeric(t *testing.T) {
	// Os dois últimos dígitos verificadores continuam numéricos mesmo no
	// formato alfanumérico de 2026; letras nessas posições são inválidas.
	if _, err := NormalizeCNPJ("1234ABCD0011AB"); err != ErrInvalidCNPJ {
		t.Fatalf("esperava ErrInvalidCNPJ para DV não numérico")
	}
}
