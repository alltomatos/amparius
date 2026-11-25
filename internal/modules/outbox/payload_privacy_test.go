package outbox_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alltomatos/amparius/internal/modules/outbox"
)

func TestValidatePayloadPrivacyAcceptsDomainReferences(t *testing.T) {
	accepted := []string{
		`{"contract_id":"0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d","status":"draft","total_amount_cents":15000}`,
		`{"items":[{"catalog_item_id":"0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d","quantity":2}],"from_status":"draft","to_status":"active"}`,
		`{"document_id":"0190f3c8-6d3b-7c1a-9b8e-2f4d5a6b7c8d","access_key":"35261100000000000000000000000000000000000000"}`,
		`{"amount":1,"nested":{"due_date":"2026-11-25","flag":true,"optional":null}}`,
	}
	for _, payload := range accepted {
		if err := outbox.ValidatePayloadPrivacy(json.RawMessage(payload)); err != nil {
			t.Errorf("payload %s deveria ser aceito: %v", payload, err)
		}
	}
}

func TestValidatePayloadPrivacyRejectsPersonalData(t *testing.T) {
	rejected := []struct {
		name    string
		payload string
	}{
		{"nome civil", `{"name":"Maria da Silva"}`},
		{"nome composto", `{"customer_full_name":"Maria da Silva"}`},
		{"cpf", `{"cpf":"52998224725"}`},
		{"cnpj", `{"cnpj":"11.222.333/0001-81"}`},
		{"email", `{"contact":"maria@example.com"}`},
		{"telefone", `{"phone":"+55 85 99999-0000"}`},
		{"endereco", `{"address":"Rua das Flores, 100"}`},
		{"nascimento", `{"birth_date":"1970-01-01"}`},
		{"texto livre", `{"notes":"` + strings.Repeat("a", 600) + `"}`},
		{"pii aninhada", `{"payload":{"items":[{"deceased_name":"Jose"}]}}`},
		{"valor cpf sem chave", `{"reference":"52998224725"}`},
	}
	for _, tc := range rejected {
		err := outbox.ValidatePayloadPrivacy(json.RawMessage(tc.payload))
		if !errors.Is(err, outbox.ErrPayloadContainsPII) {
			t.Errorf("%s: esperado ErrPayloadContainsPII, obtido %v", tc.name, err)
		}
	}
}

func TestValidatePayloadPrivacyRejectsOversizedPayload(t *testing.T) {
	payload := `{"blob":"` + strings.Repeat("a", outbox.MaxPayloadBytes) + `"}`
	if err := outbox.ValidatePayloadPrivacy(json.RawMessage(payload)); !errors.Is(err, outbox.ErrPayloadContainsPII) {
		t.Fatalf("payload acima do teto deveria falhar: %v", err)
	}
	if err := outbox.ValidatePayloadPrivacy(json.RawMessage(`[1,2,3]`)); !errors.Is(err, outbox.ErrInvalidEvent) {
		t.Fatalf("payload que não é objeto deveria falhar: %v", err)
	}
	if err := outbox.ValidatePayloadPrivacy(json.RawMessage(`{"a":}`)); !errors.Is(err, outbox.ErrInvalidEvent) {
		t.Fatalf("payload malformado deveria falhar: %v", err)
	}
}
