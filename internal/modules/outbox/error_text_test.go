package outbox

import (
	"strings"
	"testing"
)

func TestErrorTextRedaction(t *testing.T) {
	cases := []struct {
		name       string
		cause      error
		forbidden  []string
		wantMarker bool
	}{
		{"mensagem normal", errString("falha transitoria sintetica"), nil, false},
		{"email", errString("contato maria@example.com falhou"), []string{"maria@example.com"}, true},
		{"cpf", errString("cliente 52998224725 nao encontrado"), []string{"52998224725"}, true},
		{"cnpj", errString("empresa 11.222.333/0001-81 invalida"), []string{"11.222.333/0001-81"}, true},
		{"telefone", errString("ligar +55 85 99999-0000"), []string{"99999-0000"}, true},
		{"token", errString("token=ABC123SEGREDO expirou"), []string{"ABC123SEGREDO"}, true},
		{"senha", errString("senha: segredo123 rejeitada"), []string{"segredo123"}, true},
		{"erro nulo", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := errorText(tc.cause)
			if got == "" {
				t.Fatal("mensagem vazia")
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(got, forbidden) {
					t.Fatalf("vazou %q: %q", forbidden, got)
				}
			}
			if tc.wantMarker && !strings.Contains(got, "[redigido]") {
				t.Fatalf("deveria marcar redação: %q", got)
			}
			if !tc.wantMarker && strings.Contains(got, "[redigido]") {
				t.Fatalf("redação indevida em mensagem operacional: %q", got)
			}
			if tc.name == "mensagem normal" && !strings.Contains(got, "falha transitoria") {
				t.Fatalf("mensagem operacional perdida: %q", got)
			}
		})
	}
}

func TestErrorTextTruncates(t *testing.T) {
	got := errorText(errString(strings.Repeat("a", maxErrorRunes+500)))
	if len([]rune(got)) != maxErrorRunes {
		t.Fatalf("last_error deveria truncar em %d runes, obtido %d", maxErrorRunes, len([]rune(got)))
	}
}

type errString string

func (e errString) Error() string { return string(e) }
