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
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

// S3 verifica a existência do bucket por uma operação real do protocolo S3.
// S3Config descreve um endpoint S3 ou S3-compatible, como Cloudflare R2.
// Credenciais incompletas deixam a dependência não configurada e fazem o
// readiness falhar fechado, sem tentar a cadeia automática da AWS.
type S3Config struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

// S3 executa HeadBucket assinado contra object storage S3-compatible.
type S3 struct {
	client             *s3.Client
	bucket             string
	configurationError error
}

func NewS3(ctx context.Context, configuration S3Config) (*S3, error) {
	storage := &S3{bucket: strings.TrimSpace(configuration.Bucket)}
	missing := make([]string, 0, 5)
	if strings.TrimSpace(configuration.Endpoint) == "" {
		missing = append(missing, "endpoint")
	}
	if strings.TrimSpace(configuration.Region) == "" {
		missing = append(missing, "region")
	}
	if strings.TrimSpace(configuration.Bucket) == "" {
		missing = append(missing, "bucket")
	}
	if strings.TrimSpace(configuration.AccessKey) == "" {
		missing = append(missing, "access_key")
	}
	if strings.TrimSpace(configuration.SecretKey) == "" {
		missing = append(missing, "secret_key")
	}
	if len(missing) > 0 {
		storage.configurationError = fmt.Errorf("object storage não configurado: campos ausentes (%s)", strings.Join(missing, ","))
		return storage, nil
	}
	parsedEndpoint, err := url.Parse(strings.TrimRight(configuration.Endpoint, "/"))
	if err != nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return nil, errors.New("endpoint de object storage inválido")
	}
	awsConfiguration, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(configuration.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(configuration.AccessKey, configuration.SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("configurar cliente S3: %w", err)
	}
	storage.client = s3.NewFromConfig(awsConfiguration, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(strings.TrimRight(configuration.Endpoint, "/"))
		options.UsePathStyle = configuration.UsePathStyle
	})
	return storage, nil
}

func (*S3) Name() string { return "object_storage" }

func (s *S3) Check(ctx context.Context) error {
	if s == nil || s.client == nil {
		if s == nil || s.configurationError == nil {
			return errors.New("object storage não configurado")
		}
		return s.configurationError
	}
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	return err
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
