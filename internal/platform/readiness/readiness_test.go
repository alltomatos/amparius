package readiness

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeDependency struct {
	name string
	err  error
}

func (d fakeDependency) Name() string                { return d.name }
func (d fakeDependency) Check(context.Context) error { return d.err }

func TestCheckerReportsReadyOnlyWhenAllDependenciesPass(t *testing.T) {
	report := New([]Dependency{
		fakeDependency{name: "postgres"},
		fakeDependency{name: "redis", err: errors.New("não expor")},
	}, time.Second).Check(context.Background())

	if report.Ready || report.Status != "not_ready" {
		t.Fatalf("report = %+v, esperado not_ready", report)
	}
	if report.Checks["postgres"].Status != "ok" {
		t.Fatalf("postgres = %+v", report.Checks["postgres"])
	}
	if report.Checks["redis"].Status != "failed" {
		t.Fatalf("redis = %+v", report.Checks["redis"])
	}
}

func TestCheckerUsesTimeout(t *testing.T) {
	dependency := fakeBlockingDependency{name: "postgres"}
	report := New([]Dependency{dependency}, 10*time.Millisecond).Check(context.Background())
	if report.Ready {
		t.Fatal("dependência bloqueada não deveria ficar ready")
	}
}

type fakeBlockingDependency struct{ name string }

func (d fakeBlockingDependency) Name() string { return d.name }
func (d fakeBlockingDependency) Check(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestS3ChecksHeadBucketAgainstGenericEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/amparius-test" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("HeadBucket sem assinatura AWS SigV4")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	probe, err := NewS3(context.Background(), S3Config{
		Endpoint:     server.URL,
		Region:       "auto",
		Bucket:       "amparius-test",
		AccessKey:    "access",
		SecretKey:    "secret",
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	if err := probe.Check(context.Background()); err != nil {
		t.Fatalf("S3.Check: %v", err)
	}
}

func TestS3UnconfiguredFailsClosed(t *testing.T) {
	probe, err := NewS3(context.Background(), S3Config{})
	if err != nil {
		t.Fatalf("NewS3 sem configuração: %v", err)
	}
	if err := probe.Check(context.Background()); err == nil {
		t.Fatal("object storage sem configuração deveria falhar")
	}
}

func TestRedisSendsPingAndRequiresPong(t *testing.T) {
	address := serveProbe(t, func(connection net.Conn) {
		request := make([]byte, 14)
		if _, err := io.ReadFull(connection, request); err != nil {
			t.Errorf("ler PING: %v", err)
			return
		}
		if string(request) != "*1\r\n$4\r\nPING\r\n" {
			t.Errorf("request Redis = %q", request)
			return
		}
		_, _ = connection.Write([]byte("+PONG\r\n"))
	})
	if err := NewRedis(address).Check(context.Background()); err != nil {
		t.Fatalf("Redis.Check: %v", err)
	}
}

func TestRabbitMQCompletesAMQPHandshake(t *testing.T) {
	address := serveProbe(t, func(connection net.Conn) {
		header := make([]byte, 8)
		if _, err := io.ReadFull(connection, header); err != nil {
			t.Errorf("ler cabeçalho AMQP: %v", err)
			return
		}
		if string(header) != "AMQP\x00\x00\x09\x01" {
			t.Errorf("header AMQP = %v", header)
			return
		}
		_, _ = connection.Write([]byte{1, 0, 0, 0, 0, 0, 1})
	})
	if err := NewRabbitMQ(address).Check(context.Background()); err != nil {
		t.Fatalf("RabbitMQ.Check: %v", err)
	}
}

func TestNetworkAddressDefaultsPort(t *testing.T) {
	if got := networkAddress("localhost", "6379"); got != "localhost:6379" {
		t.Fatalf("address = %q", got)
	}
	if got := networkAddress("localhost:6380", "6379"); got != "localhost:6380" {
		t.Fatalf("address com porta = %q", got)
	}
}

func serveProbe(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		handler(connection)
	}()
	return listener.Addr().String()
}
