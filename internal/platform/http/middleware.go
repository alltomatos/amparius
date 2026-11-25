package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alltomatos/amparius/internal/platform/observability"
)

// RequestIDFromContext devolve o request-id (correlation id) do contexto.
func RequestIDFromContext(ctx context.Context) string {
	return observability.CorrelationIDFromContext(ctx)
}

// middleware é o formato padrão de encadeamento.
type middleware func(http.Handler) http.Handler

// chain aplica os middlewares na ordem informada, do mais externo ao mais interno.
func chain(next http.Handler, middlewares ...middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		next = middlewares[i](next)
	}
	return next
}

// requestID propaga o X-Request-Id recebido (quando válido) ou gera um novo.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := observability.WithCorrelationID(r.Context(), id)
		traceID := traceIDFromHeader(r.Header.Get("Traceparent"))
		if traceID == "" {
			traceID = observability.NewTraceID()
		}
		w.Header().Set("X-Trace-Id", traceID)
		ctx = observability.WithTraceID(ctx, traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// accessLog registra uma linha estruturada por requisição.
// Não registra IP, query string ou caminho bruto para não vazar dado pessoal.
func accessLog(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(recorder, r)

			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http_request",
				slog.String("method", r.Method),
				slog.Int("status", status),
				slog.Int("bytes", recorder.bytes),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("trace_id", observability.TraceIDFromContext(r.Context())),
			)
		})
	}
}

func traceIDFromHeader(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || parts[3] == "00" {
		return ""
	}
	for _, part := range parts[1:3] {
		if !validHex(part) || strings.Trim(part, "0") == "" {
			return ""
		}
	}
	return strings.ToLower(parts[1])
}

func validHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// recoverer impede que um panic derrube o processo e devolve 500.
func recoverer(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				logger.LogAttrs(r.Context(), slog.LevelError, "http_panic",
					slog.String("panic_type", fmt.Sprintf("%T", recovered)),
					slog.String("request_id", RequestIDFromContext(r.Context())),
					slog.String("trace_id", observability.TraceIDFromContext(r.Context())),
				)
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"status": "error",
					"error":  "erro interno",
				})
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func validRequestID(id string) bool {
	if len(id) < 8 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		switch {
		case ch >= 'a' && ch <= 'z',
			ch >= 'A' && ch <= 'Z',
			ch >= '0' && ch <= '9',
			ch == '-', ch == '_', ch == '.':
		default:
			return false
		}
	}
	return true
}
