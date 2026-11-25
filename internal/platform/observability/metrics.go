package observability

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Labels identificam uma série temporal. São sempre valores de baixa
// cardinalidade: tenant_id é opaco e não contém PII.
type Labels map[string]string

// Sample é uma amostra de gauge usada para substituir a família inteira.
type Sample struct {
	Labels Labels
	Value  float64
}

type metricKind uint8

const (
	kindCounter metricKind = iota + 1
	kindGauge
	kindHistogram
)

var defaultHistogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

var (
	metricNamePattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNamePattern  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

type series struct {
	name         string
	kind         metricKind
	labels       string
	value        float64
	bucketCounts []uint64
	sum          float64
	count        uint64
}

// Metrics é um registry concorrente com exposição no formato Prometheus.
// O zero value não é utilizável: use NewMetrics.
type Metrics struct {
	mu      sync.Mutex
	series  map[string]*series
	buckets []float64
}

// NewMetrics cria um registry vazio.
func NewMetrics() *Metrics {
	return &Metrics{
		series:  make(map[string]*series),
		buckets: append([]float64(nil), defaultHistogramBuckets...),
	}
}

// AddCounter soma delta ao contador. Nome inválido é ignorado.
func (m *Metrics) AddCounter(name string, labels Labels, delta float64) {
	m.record(name, labels, kindCounter, delta)
}

// SetGauge define o valor atual do gauge. Nome inválido é ignorado.
func (m *Metrics) SetGauge(name string, labels Labels, value float64) {
	m.record(name, labels, kindGauge, value)
}

// Observe registra uma amostra de histograma. Nome inválido é ignorado.
func (m *Metrics) Observe(name string, labels Labels, value float64) {
	m.record(name, labels, kindHistogram, value)
}

// ReplaceGauges troca todas as séries de gauge do nome informado. É usado no
// backlog por tenant: cada ciclo publica a fotografia atual e remove tenants
// que deixaram de ser visitados, evitando série obsoleta.
func (m *Metrics) ReplaceGauges(name string, samples []Sample) {
	if m == nil || !metricNamePattern.MatchString(name) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := fmt.Sprintf("%d\x00%s\x00", kindGauge, name)
	for key := range m.series {
		if strings.HasPrefix(key, prefix) {
			delete(m.series, key)
		}
	}
	for _, sample := range samples {
		m.recordLocked(name, sample.Labels, kindGauge, sample.Value)
	}
}

// Reset descarta todas as séries.
func (m *Metrics) Reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.series = make(map[string]*series)
}

// Handler devolve o endpoint de scrape Prometheus.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = m.WritePrometheus(w)
	})
}

// WritePrometheus escreve a exposição texto Prometheus.
func (m *Metrics) WritePrometheus(w io.Writer) error {
	if m == nil || w == nil {
		return errors.New("registry de métricas ou destino nulo")
	}
	m.mu.Lock()
	items := make([]*series, 0, len(m.series))
	for _, item := range m.series {
		copied := *item
		copied.bucketCounts = append([]uint64(nil), item.bucketCounts...)
		items = append(items, &copied)
	}
	buckets := append([]float64(nil), m.buckets...)
	m.mu.Unlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].name != items[j].name {
			return items[i].name < items[j].name
		}
		return items[i].labels < items[j].labels
	})

	var builder strings.Builder
	var currentTypeName string
	for _, item := range items {
		if item.name != currentTypeName {
			currentTypeName = item.name
			builder.WriteString("# TYPE ")
			builder.WriteString(item.name)
			builder.WriteString(" ")
			builder.WriteString(kindName(item.kind))
			builder.WriteString("\n")
		}
		switch item.kind {
		case kindHistogram:
			writeHistogram(&builder, item, buckets)
		default:
			builder.WriteString(item.name)
			builder.WriteString(item.labels)
			builder.WriteString(" ")
			builder.WriteString(formatFloat(item.value))
			builder.WriteString("\n")
		}
	}
	_, err := io.WriteString(w, builder.String())
	return err
}

func (m *Metrics) record(name string, labels Labels, kind metricKind, value float64) {
	if m == nil || !metricNamePattern.MatchString(name) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordLocked(name, labels, kind, value)
}

func (m *Metrics) recordLocked(name string, labels Labels, kind metricKind, value float64) {
	key := fmt.Sprintf("%d\x00%s\x00%s", kind, name, renderLabels(labels))
	item := m.series[key]
	if item == nil {
		item = &series{name: name, kind: kind, labels: renderLabels(labels)}
		if kind == kindHistogram {
			item.bucketCounts = make([]uint64, len(m.buckets))
		}
		m.series[key] = item
	}
	if item.kind != kind {
		return
	}
	switch kind {
	case kindCounter:
		item.value += value
	case kindGauge:
		item.value = value
	case kindHistogram:
		item.count++
		item.sum += value
		for i, bucket := range m.buckets {
			if value <= bucket {
				item.bucketCounts[i]++
			}
		}
	}
}

func writeHistogram(builder *strings.Builder, item *series, buckets []float64) {
	for i, bucket := range buckets {
		builder.WriteString(item.name)
		builder.WriteString("_bucket")
		builder.WriteString(appendLabel(item.labels, "le", formatFloat(bucket)))
		builder.WriteString(" ")
		builder.WriteString(strconv.FormatUint(item.bucketCounts[i], 10))
		builder.WriteString("\n")
	}
	builder.WriteString(item.name)
	builder.WriteString("_bucket")
	builder.WriteString(appendLabel(item.labels, "le", "+Inf"))
	builder.WriteString(" ")
	builder.WriteString(strconv.FormatUint(item.count, 10))
	builder.WriteString("\n")
	builder.WriteString(item.name)
	builder.WriteString("_sum")
	builder.WriteString(item.labels)
	builder.WriteString(" ")
	builder.WriteString(formatFloat(item.sum))
	builder.WriteString("\n")
	builder.WriteString(item.name)
	builder.WriteString("_count")
	builder.WriteString(item.labels)
	builder.WriteString(" ")
	builder.WriteString(strconv.FormatUint(item.count, 10))
	builder.WriteString("\n")
}

func renderLabels(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if labelNamePattern.MatchString(key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"=\""+escapeLabelValue(labels[key])+"\"")
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func appendLabel(rendered, key, value string) string {
	pair := key + "=\"" + escapeLabelValue(value) + "\""
	if rendered == "" {
		return "{" + pair + "}"
	}
	return rendered[:len(rendered)-1] + "," + pair + "}"
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func kindName(kind metricKind) string {
	switch kind {
	case kindCounter:
		return "counter"
	case kindGauge:
		return "gauge"
	case kindHistogram:
		return "histogram"
	default:
		return "untyped"
	}
}
