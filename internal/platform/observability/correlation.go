// Package observability concentra correlação e métricas sem dependência
// externa. O correlation id é propagado pelo contexto e nunca carrega dado
// pessoal: é um identificador opaco por requisição, ciclo de worker ou evento.
package observability

import (
	"context"

	"github.com/google/uuid"
)

type correlationContextKey struct{}

// WithCorrelationID devolve um contexto filho com o correlation id informado.
// Um id vazio mantém o contexto original.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationContextKey{}, id)
}

// CorrelationIDFromContext devolve o correlation id do contexto, ou "".
func CorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(correlationContextKey{}).(string)
	return id
}

// NewCorrelationID gera um id opaco (UUIDv7) para requisição, ciclo ou evento.
func NewCorrelationID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
