package log

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"INFO":    slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	}
	for input, want := range cases {
		got, err := ParseLevel(input)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v, quer %v", input, got, want)
		}
	}
}

func TestParseLevelInvalido(t *testing.T) {
	if _, err := ParseLevel("trace"); err == nil {
		t.Fatal("ParseLevel(\"trace\") deveria falhar")
	}
}

func TestNewEmiteJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)
	logger.Info("teste", "chave", "valor")

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("saída não é JSON válido: %v (%s)", err, buf.String())
	}
	if payload["msg"] != "teste" || payload["chave"] != "valor" {
		t.Fatalf("payload inesperado: %v", payload)
	}
}
