// Package log monta loggers estruturados (JSON) sobre log/slog.
//
// Nenhum logger global é criado: o logger é construído no bootstrap e
// injetado nas dependências.
package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// ParseLevel converte o nível textual em slog.Level.
func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("nível de log inválido: %q", value)
	}
}

// New cria um logger JSON no nível informado, escrevendo em w.
// O horário é sempre emitido em UTC (RFC3339Nano).
func New(w io.Writer, level slog.Level) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key != slog.TimeKey {
				return attr
			}
			if ts, ok := attr.Value.Any().(time.Time); ok {
				return slog.String(slog.TimeKey, ts.UTC().Format(time.RFC3339Nano))
			}
			return attr
		},
	})
	return slog.New(handler)
}

// NewFromEnv cria um logger JSON lendo LOG_LEVEL do ambiente.
func NewFromEnv(w io.Writer) (*slog.Logger, error) {
	level, err := ParseLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		return nil, err
	}
	return New(w, level), nil
}
