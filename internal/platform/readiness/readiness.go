// Package readiness verifica as dependências obrigatórias para receber tráfego.
//
// Os adaptadores deste pacote fazem somente probes de infraestrutura. Eles não
// acessam tabelas de módulos nem expõem detalhes do erro na resposta HTTP.
package readiness

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultTimeout = 2 * time.Second

// Dependency é uma verificação nomeada de infraestrutura.
type Dependency interface {
	Name() string
	Check(context.Context) error
}

// Result é o resultado público de uma verificação, sem erro bruto ou segredo.
type Result struct {
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
}

// Report agrega o resultado da readiness.
type Report struct {
	Ready  bool              `json:"-"`
	Status string            `json:"status"`
	Checks map[string]Result `json:"checks"`
}

// Checker executa os probes com um limite total por requisição.
type Checker struct {
	dependencies []Dependency
	timeout      time.Duration
}

// New cria um verificador. Um timeout inválido usa dois segundos.
func New(dependencies []Dependency, timeout time.Duration) *Checker {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Checker{
		dependencies: append([]Dependency(nil), dependencies...),
		timeout:      timeout,
	}
}

// Check executa os probes em ordem determinística, sem retornar detalhes
// potencialmente sensíveis dos erros.
func (c *Checker) Check(parent context.Context) Report {
	if c == nil || len(c.dependencies) == 0 {
		return Report{Status: "not_ready", Checks: map[string]Result{
			"readiness": {Status: "not_configured"},
		}}
	}

	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()

	report := Report{Ready: true, Status: "ready", Checks: make(map[string]Result, len(c.dependencies))}
	for index, dependency := range c.dependencies {
		if dependency == nil || dependency.Name() == "" {
			report.Ready = false
			report.Checks[fmt.Sprintf("dependency_%d", index)] = Result{Status: "failed"}
			continue
		}
		started := time.Now()
		err := dependency.Check(ctx)
		result := Result{Status: "ok", LatencyMS: time.Since(started).Milliseconds()}
		if err != nil {
			result.Status = "failed"
			report.Ready = false
		}
		report.Checks[dependency.Name()] = result
	}
	if !report.Ready {
		report.Status = "not_ready"
	}
	return report
}

// Postgres verifica o banco com um ping real via protocolo PostgreSQL.
type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("DSN PostgreSQL vazio")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("configurar PostgreSQL: %w", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("criar pool PostgreSQL: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (*Postgres) Name() string { return "postgres" }

func (p *Postgres) Check(ctx context.Context) error {
	if p == nil || p.pool == nil {
		return errors.New("pool PostgreSQL indisponível")
	}
	return p.pool.Ping(ctx)
}

func (p *Postgres) Close() {
	if p != nil && p.pool != nil {
		p.pool.Close()
	}
}

// Redis executa PING e exige PONG, em vez de apenas abrir uma porta TCP.
type Redis struct{ address string }

func NewRedis(address string) *Redis { return &Redis{address: networkAddress(address, "6379")} }

func (*Redis) Name() string { return "redis" }

func (r *Redis) Check(ctx context.Context) error {
	if r == nil || r.address == "" {
		return errors.New("endereço Redis vazio")
	}
	conn, err := dial(ctx, r.address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := setDeadline(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		return err
	}
	response := make([]byte, 7)
	if _, err := readFull(conn, response); err != nil {
		return err
	}
	if string(response) != "+PONG\r\n" {
		return errors.New("resposta Redis inesperada")
	}
	return nil
}

// RabbitMQ inicia o handshake AMQP para comprovar que o endpoint é um broker.
type RabbitMQ struct{ address string }

func NewRabbitMQ(rawURL string) *RabbitMQ {
	return &RabbitMQ{address: rabbitAddress(rawURL)}
}

func (*RabbitMQ) Name() string { return "rabbitmq" }

func (r *RabbitMQ) Check(ctx context.Context) error {
	if r == nil || r.address == "" {
		return errors.New("URL RabbitMQ vazia")
	}
	conn, err := dial(ctx, r.address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := setDeadline(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.Write([]byte{'A', 'M', 'Q', 'P', 0, 0, 9, 1}); err != nil {
		return err
	}
	frameHeader := make([]byte, 7)
	if _, err := readFull(conn, frameHeader); err != nil {
		return err
	}
	if frameHeader[0] != 1 || frameHeader[1] != 0 || frameHeader[2] != 0 {
		return errors.New("resposta AMQP inesperada")
	}
	return nil
}

// MinIO verifica o endpoint de saúde HTTP. A rota não inclui credenciais nem
// dados de bucket e é adequada para readiness da infraestrutura.
type MinIO struct {
	endpoint string
	client   *http.Client
}

func NewMinIO(endpoint string, useSSL bool) *MinIO {
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if !strings.Contains(endpoint, "://") {
		endpoint = scheme + "://" + endpoint
	}
	return &MinIO{endpoint: endpoint, client: &http.Client{}}
}

func (*MinIO) Name() string { return "minio" }

func (m *MinIO) Check(ctx context.Context) error {
	if m == nil || m.endpoint == "" {
		return errors.New("endpoint MinIO vazio")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint+"/minio/health/live", nil)
	if err != nil {
		return err
	}
	client := m.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("MinIO respondeu HTTP %d", response.StatusCode)
	}
	return nil
}

func networkAddress(raw, defaultPort string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		if parsed, err := url.Parse(raw); err == nil {
			raw = parsed.Host
		}
	}
	if _, _, err := net.SplitHostPort(raw); err == nil {
		return raw
	}
	if strings.Contains(raw, ":") && strings.Count(raw, ":") > 1 {
		return net.JoinHostPort(raw, defaultPort)
	}
	return net.JoinHostPort(raw, defaultPort)
}

func rabbitAddress(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
		if parsed.Port() == "" {
			return net.JoinHostPort(parsed.Hostname(), "5672")
		}
		return parsed.Host
	}
	return networkAddress(raw, "5672")
}

func dial(ctx context.Context, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", address)
}

func setDeadline(ctx context.Context, conn net.Conn) error {
	if deadline, ok := ctx.Deadline(); ok {
		return conn.SetDeadline(deadline)
	}
	return nil
}

func readFull(conn net.Conn, buffer []byte) (int, error) {
	read := 0
	for read < len(buffer) {
		n, err := conn.Read(buffer[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}
