package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/platform/readiness"
)

func TestReadyzP95(t *testing.T) {
	iterations := benchmarkIterations(t)
	if iterations == 0 {
		t.Skip("defina READINESS_P95_ITERATIONS para executar a medição operacional")
	}

	server := httptest.NewServer(New(slog.New(slog.NewJSONHandler(io.Discard, nil)), Options{
		Readiness: readiness.New([]readiness.Dependency{
			benchmarkDependency{name: "synthetic-dependency"},
		}, time.Second),
	}).Handler())
	defer server.Close()

	client := server.Client()
	latencies := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		started := time.Now()
		response, err := client.Get(server.URL + "/readyz")
		if err != nil {
			t.Fatalf("requisição %d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("requisição %d: status = %d, esperado 200", i, response.StatusCode)
		}
		latencies = append(latencies, time.Since(started))
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	index := (len(latencies)*95+99)/100 - 1
	if index < 0 {
		index = 0
	}
	p95 := latencies[index]
	t.Logf("readiness_p95_us=%d readiness_p95_ms=%.3f samples=%d", p95.Microseconds(), float64(p95.Microseconds())/1000, len(latencies))
}

type benchmarkDependency struct{ name string }

func (d benchmarkDependency) Name() string                { return d.name }
func (d benchmarkDependency) Check(context.Context) error { return nil }

func benchmarkIterations(t *testing.T) int {
	t.Helper()
	value := os.Getenv("READINESS_P95_ITERATIONS")
	if value == "" {
		return 0
	}
	iterations, err := strconv.Atoi(value)
	if err != nil || iterations < 100 {
		t.Fatalf("READINESS_P95_ITERATIONS deve ser inteiro >= 100: %q", value)
	}
	return iterations
}
