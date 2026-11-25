package main

import (
	"testing"

	"github.com/alltomatos/amparius/internal/modules/outbox"
)

func TestSelectHandlerUsesRealDeliveryByDefault(t *testing.T) {
	if _, ok := selectHandler("delivery").(outbox.DeliveryHandler); !ok {
		t.Fatal("handler padrão deveria ser DeliveryHandler")
	}
	if _, ok := selectHandler("refuse").(outbox.RefusingHandler); !ok {
		t.Fatal("handler refuse deveria ser RefusingHandler")
	}
	// Um nome desconhecido nunca cai em noop silencioso: a config já barra,
	// e o fallback conservador é a recusa explícita.
	if _, ok := selectHandler("inesperado").(outbox.DeliveryHandler); ok {
		t.Fatal("nome desconhecido não deveria entregar")
	}
}
