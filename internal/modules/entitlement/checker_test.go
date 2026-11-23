package entitlement

import (
	"errors"
	"testing"
)

func TestDecideCoreAtivoPermite(t *testing.T) {
	if err := decide(moduleKindCore, true, false); err != nil {
		t.Fatalf("core ativo deveria permitir: %v", err)
	}
}

func TestDecideOptionalComAssignmentVigentePermite(t *testing.T) {
	if err := decide(moduleKindOptional, true, true); err != nil {
		t.Fatalf("optional vigente deveria permitir: %v", err)
	}
}

func TestDecideBloqueiaCasosNegativos(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		active   bool
		assigned bool
	}{
		{name: "sem assignment", kind: moduleKindOptional, active: true},
		{name: "assignment vencido", kind: moduleKindOptional, active: true},
		{name: "assignment revoked", kind: moduleKindOptional, active: true},
		{name: "modulo inexistente", kind: "missing", active: false},
		{name: "modulo inativo", kind: moduleKindOptional, active: false, assigned: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(decide(tt.kind, tt.active, tt.assigned), ErrModuleDisabled) {
				t.Fatal("caso negativo deveria retornar ErrModuleDisabled")
			}
		})
	}
}

func TestErrosDeContratoSaoDistinguiveis(t *testing.T) {
	if errors.Is(ErrModuleDisabled, ErrEntitlementUnavailable) || errors.Is(ErrEntitlementUnavailable, ErrModuleDisabled) {
		t.Fatal("erros estáveis devem ser distinguíveis")
	}
}
