package observability

import (
	"context"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

type traceContextKey struct{}

// WithTraceID associa ao contexto um trace-id opaco de 32 caracteres hexadecimais.
func WithTraceID(ctx context.Context, id string) context.Context {
	if ctx == nil || !validTraceID(id) {
		return ctx
	}
	return context.WithValue(ctx, traceContextKey{}, strings.ToLower(id))
}

// TraceIDFromContext devolve o trace-id do contexto, ou "".
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(traceContextKey{}).(string)
	if !validTraceID(id) {
		return ""
	}
	return id
}

// NewTraceID gera um trace-id sem carregar tenant, usuário ou dado de negócio.
func NewTraceID() string {
	id := uuid.New()
	return hex.EncodeToString(id[:])
}

func validTraceID(id string) bool {
	if len(id) != 32 || id == strings.Repeat("0", 32) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
