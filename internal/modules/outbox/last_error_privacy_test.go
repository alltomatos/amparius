package outbox_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestFailRedactsPIIFromLastError garante que uma mensagem de erro de handler
// nunca grave dado pessoal em last_error, mesmo que um handler futuro retorne
// erro com PII por descuido. A outbox é trilha de auditoria, não cadastro.
func TestFailRedactsPIIFromLastError(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	event := env.appendEvent(t, 0, "pii:last-error:"+time.Now().String(), 3)
	if _, err := env.repository.Claim(ctxA, 10, time.Minute); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("falha ao contatar maria@example.com, CPF 52998224725, token ABC123SEGREDO")
	failed, err := env.repository.Fail(ctxA, event.ID, cause, 0)
	if err != nil {
		t.Fatal(err)
	}
	if failed.LastError == nil {
		t.Fatal("last_error deveria estar preenchido")
	}
	text := *failed.LastError
	for _, forbidden := range []string{"maria@example.com", "52998224725", "ABC123SEGREDO"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("last_error vazou dado sensível %q: %q", forbidden, text)
		}
	}
	if !strings.Contains(text, "[redigido]") {
		t.Fatalf("last_error deveria marcar redação: %q", text)
	}
	_ = context.Background
}
