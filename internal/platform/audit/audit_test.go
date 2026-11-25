package audit

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidateDetailAcceptsOperationalReferences(t *testing.T) {
	detail := json.RawMessage(`{"resource_id":"018f2d8e-6f4b-7c05-8a50-2c5e11f8a1b0","status":"success","count":2}`)
	if err := ValidateDetail(detail); err != nil {
		t.Fatalf("detalhe operacional recusado: %v", err)
	}
}

func TestValidateDetailRejectsPIIAndFreeText(t *testing.T) {
	for _, detail := range []json.RawMessage{
		json.RawMessage(`{"email":"person@example.invalid"}`),
		json.RawMessage(`{"customer_name":"Pessoa"}`),
		json.RawMessage(`{"reason":"12345678901"}`),
		json.RawMessage(`{"message":"texto"}`),
	} {
		if !errors.Is(ValidateDetail(detail), ErrPIIInDetail) {
			t.Fatalf("detalhe sensível deveria falhar: %s", detail)
		}
	}
}
