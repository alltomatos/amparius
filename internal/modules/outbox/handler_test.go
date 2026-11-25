package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/outbox"
	"github.com/google/uuid"
)

// TestDeliveryHandlerValidatesEnvelope prova que o handler real de entrega
// aceita apenas eventos com envelope v1 íntegro e não toca em payload/PII.
func TestDeliveryHandlerValidatesEnvelope(t *testing.T) {
	handler := outbox.NewDeliveryHandler()
	valid := outbox.Event{
		ID: uuid.New(), TenantID: uuid.New(), AggregateType: "funeral.contract",
		AggregateID: uuid.New(), EventType: "funeral.contract.created", Version: 1,
		Payload:        json.RawMessage(`{"contract_id":"` + uuid.NewString() + `"}`),
		IdempotencyKey: "funeral.contract.create:k1", Status: "processing", Attempts: 1,
	}
	if err := handler.Handle(context.Background(), valid); err != nil {
		t.Fatalf("envelope válido rejeitado: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*outbox.Event)
	}{
		{"sem id", func(e *outbox.Event) { e.ID = uuid.Nil }},
		{"sem tenant", func(e *outbox.Event) { e.TenantID = uuid.Nil }},
		{"sem agregado", func(e *outbox.Event) { e.AggregateID = uuid.Nil }},
		{"tipo de evento inválido", func(e *outbox.Event) { e.EventType = "Criado" }},
		{"versão zero", func(e *outbox.Event) { e.Version = 0 }},
		{"payload com PII", func(e *outbox.Event) { e.Payload = json.RawMessage(`{"name":"Maria"}`) }},
		{"payload inválido", func(e *outbox.Event) { e.Payload = json.RawMessage(`[]`) }},
		{"status inesperado", func(e *outbox.Event) { e.Status = "pending" }},
		{"attempt zero", func(e *outbox.Event) { e.Attempts = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := valid
			tc.mutate(&event)
			err := handler.Handle(context.Background(), event)
			if err == nil {
				t.Fatalf("envelope inválido aceito")
			}
			if strings.Contains(err.Error(), "Maria") || strings.Contains(err.Error(), "maria") {
				t.Fatalf("erro do handler vazou PII: %v", err)
			}
			if !errors.Is(err, outbox.ErrInvalidEvent) && !errors.Is(err, outbox.ErrPayloadContainsPII) {
				t.Fatalf("erro deveria ser tipado, obtido %v", err)
			}
		})
	}
}
