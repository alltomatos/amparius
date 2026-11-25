package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestAppendTxRollsBackWithAggregate prova a invariante central do OUT-01A: o
// evento e o dado de negócio vivem ou morrem juntos. Um erro depois do append
// desfaz o evento; um replay dentro da mesma transação não duplica a linha.
func TestAppendTxRollsBackWithAggregate(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)
	sentinel := errors.New("falha sintetica depois do append")

	event := outbox.Event{
		AggregateType:  "finance.title",
		AggregateID:    uuid.New(),
		EventType:      "finance.title.created",
		Version:        1,
		Payload:        json.RawMessage(`{"amount_cents":100}`),
		IdempotencyKey: "atomic:rollback",
	}
	err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := env.repository.AppendTx(ctx, tx, event); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("erro esperado da transacao, obtido %v", err)
	}
	if count := env.countEvents(t, 0); count != 0 {
		t.Fatalf("rollback deixou %d evento(s)", count)
	}

	// O mesmo fluxo commitado grava uma única linha, mesmo com dois appends da
	// mesma chave na mesma transação.
	var first, second uuid.UUID
	if err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		a, err := env.repository.AppendTx(ctx, tx, event)
		if err != nil {
			return err
		}
		b, err := env.repository.AppendTx(ctx, tx, event)
		if err != nil {
			return err
		}
		first, second = a.ID, b.ID
		return nil
	}); err != nil {
		t.Fatalf("append transacional: %v", err)
	}
	if first != second {
		t.Fatalf("replay deveria devolver o mesmo evento: %s != %s", first, second)
	}
	if count := env.countEvents(t, 0); count != 1 {
		t.Fatalf("append duplicou evento: %d", count)
	}
}

// TestDatabaseRejectsPersonalDataInPayload prova que a guarda de PII vale no
// banco, mesmo quando a escrita não passa pelo repositório Go.
func TestDatabaseRejectsPersonalDataInPayload(t *testing.T) {
	env := newEnvironment(t, 1)
	ctxA := env.tenantContext(t, 0)

	cases := []struct {
		name    string
		payload string
	}{
		{"nome civil", `{"name":"Maria da Silva"}`},
		{"cpf", `{"cpf":"52998224725"}`},
		{"cnpj formatado", `{"cnpj":"11.222.333/0001-81"}`},
		{"email", `{"contact":"maria@example.com"}`},
		{"telefone", `{"phone":"+55 85 99999-0000"}`},
		{"pii aninhada", `{"payload":{"items":[{"deceased_name":"Jose"}]}}`},
		{"texto livre", `{"notes":"` + strings.Repeat("a", 600) + `"}`},
	}
	for _, tc := range cases {
		err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
INSERT INTO outbox.event (tenant_id,aggregate_type,aggregate_id,event_type,event_version,payload,idempotency_key)
VALUES ($1,'finance.title',$2,'finance.title.created',1,$3::jsonb,$4)`,
				env.tenants[0], uuid.New(), tc.payload, "pii:"+uuid.NewString())
			return err
		})
		if err == nil {
			t.Fatalf("%s deveria ser bloqueado pelo banco", tc.name)
		}
		if !strings.Contains(err.Error(), "dado pessoal") {
			t.Fatalf("%s: erro inesperado do banco: %v", tc.name, err)
		}
	}

	oversized := `{"blob":"` + strings.Repeat("a", outbox.MaxPayloadBytes) + `"}`
	err := env.database.WithTenantTx(ctxA, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
INSERT INTO outbox.event (tenant_id,aggregate_type,aggregate_id,event_type,event_version,payload,idempotency_key)
VALUES ($1,'finance.title',$2,'finance.title.created',1,$3::jsonb,$4)`,
			env.tenants[0], uuid.New(), oversized, "pii:oversized")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "excede") {
		t.Fatalf("payload acima do teto deveria ser bloqueado pelo banco: %v", err)
	}

	if count := env.countEvents(t, 0); count != 0 {
		t.Fatalf("tentativas rejeitadas gravaram %d evento(s)", count)
	}
}

func (e *environment) countEvents(t *testing.T, tenantIndex int) int {
	t.Helper()
	var count int
	if err := e.database.WithTenantTx(e.tenantContext(t, tenantIndex), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM outbox.event WHERE tenant_id=$1`, e.tenants[tenantIndex]).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
