package fiscal

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEffectiveAtUsaIntervaloHalfOpen(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if !effectiveAt(from, &to, from) {
		t.Fatal("inicio deve ser incluido")
	}
	if effectiveAt(from, &to, to) {
		t.Fatal("fim deve ser excluido")
	}
	if !effectiveAt(from, nil, to) {
		t.Fatal("vigencia sem fim deve continuar aberta")
	}
}

func TestChooseRuleFailClosed(t *testing.T) {
	tenantID := uuid.New()
	if _, err := chooseRule(nil, tenantID); !errors.Is(err, ErrTaxRuleNotFound) {
		t.Fatalf("ausencia: %v", err)
	}
	if _, err := chooseRule([]TaxRule{{ID: uuid.New()}, {ID: uuid.New()}}, tenantID); !errors.Is(err, ErrTaxRuleAmbiguous) {
		t.Fatalf("ambiguidade: %v", err)
	}
}

func TestChooseRuleMantemOverrideDoTenantSelecionado(t *testing.T) {
	id := uuid.New()
	globalID := uuid.New()
	ruleTenant := id
	rule, err := chooseRule([]TaxRule{{ID: globalID}, {ID: id, TenantID: &ruleTenant}}, ruleTenant)
	if err != nil || rule.ID != id {
		t.Fatalf("regra selecionada: %v, %v", rule.ID, err)
	}
}
