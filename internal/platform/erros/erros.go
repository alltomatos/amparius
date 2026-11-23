// Package erros concentra os erros de domínio da plataforma.
//
// Todo erro carrega uma causa sentinela (Kind) para que chamadores possam
// classificá-lo com errors.Is sem depender do texto da mensagem. O tipo Error
// também é acessível por errors.As.
package erros

import (
	"errors"
	"fmt"
)

// Sentinelas de classificação. Use-as como Kind em New, Wrap e nos atalhos.
var (
	ErrNotFound     = errors.New("recurso não encontrado")
	ErrConflict     = errors.New("conflito de estado")
	ErrInvalid      = errors.New("argumento inválido")
	ErrUnauthorized = errors.New("não autorizado")
	ErrForbidden    = errors.New("acesso negado")
	ErrUnavailable  = errors.New("serviço indisponível")
	ErrInternal     = errors.New("erro interno")
)

// Error é o erro de domínio. Op identifica a operação ("finance.BaixarTitulo"),
// Kind classifica o erro, Msg é a mensagem legível e Err é a causa original.
type Error struct {
	Op   string
	Kind error
	Msg  string
	Err  error
}

// Error implementa a interface error.
func (e *Error) Error() string {
	switch {
	case e == nil:
		return "<nil>"
	case e.Op == "" && e.Msg == "" && e.Err == nil:
		return e.Kind.Error()
	case e.Op == "" && e.Msg == "":
		return fmt.Sprintf("%s: %v", e.Kind, e.Err)
	case e.Op == "" && e.Err == nil:
		return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
	case e.Op == "":
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Msg, e.Err)
	case e.Msg == "" && e.Err == nil:
		return fmt.Sprintf("%s: %s", e.Op, e.Kind)
	case e.Msg == "":
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Kind, e.Err)
	case e.Err == nil:
		return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Msg)
	default:
		return fmt.Sprintf("%s: %s: %s: %v", e.Op, e.Kind, e.Msg, e.Err)
	}
}

// Unwrap expõe a causa original para errors.Is/As.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is permite que errors.Is(e, ErrNotFound) compare a classificação (Kind),
// mesmo quando a causa original é outra.
func (e *Error) Is(target error) bool {
	if e == nil || e.Kind == nil {
		return false
	}
	return errors.Is(e.Kind, target)
}

// New cria um erro classificado sem causa subjacente.
func New(op string, kind error, msg string) *Error {
	return &Error{Op: op, Kind: kind, Msg: msg}
}

// Wrap envolve um erro existente com operação e classificação.
func Wrap(op string, kind error, err error) *Error {
	return &Error{Op: op, Kind: kind, Err: err}
}

// NotFound cria um erro de recurso ausente.
func NotFound(op, msg string) *Error {
	return New(op, ErrNotFound, msg)
}

// Invalid cria um erro de argumento inválido.
func Invalid(op, msg string) *Error {
	return New(op, ErrInvalid, msg)
}

// Conflict cria um erro de conflito de estado.
func Conflict(op, msg string) *Error {
	return New(op, ErrConflict, msg)
}

// Unavailable cria um erro de dependência indisponível.
func Unavailable(op, msg string) *Error {
	return New(op, ErrUnavailable, msg)
}
