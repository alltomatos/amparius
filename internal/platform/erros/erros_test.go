package erros

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorsIsComparaClassificacao(t *testing.T) {
	err := NotFound("party.Buscar", "cliente 42")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("errors.Is(err, ErrNotFound) = false, quer true")
	}
	if errors.Is(err, ErrConflict) {
		t.Fatalf("errors.Is(err, ErrConflict) = true, quer false")
	}

	wrapped := fmt.Errorf("caso de uso: %w", err)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatalf("errors.Is(wrapped, ErrNotFound) = false, quer true")
	}
}

func TestErrorsAsRecuperaTipo(t *testing.T) {
	causa := errors.New("nome vazio")
	var err error = Wrap("catalog.CriarItem", ErrInvalid, causa)

	var domainErr *Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("errors.As não recuperou *Error")
	}
	if domainErr.Op != "catalog.CriarItem" {
		t.Fatalf("Op = %q, quer %q", domainErr.Op, "catalog.CriarItem")
	}
	if !errors.Is(errors.Unwrap(err), causa) {
		t.Fatal("Unwrap não devolveu a causa original")
	}
}

func TestErrorNaoPanicaComNulo(t *testing.T) {
	var err *Error
	if got := err.Error(); got != "<nil>" {
		t.Fatalf("Error() = %q, quer %q", got, "<nil>")
	}
	if err.Unwrap() != nil {
		t.Fatal("Unwrap() de erro nulo deve ser nil")
	}
}
