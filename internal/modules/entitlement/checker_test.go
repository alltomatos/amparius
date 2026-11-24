package entitlement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
	"github.com/google/uuid"
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

func TestCheckSemEmpresaNoContextoBloqueia(t *testing.T) {
	checker := &Checker{}
	ctx := db.WithTenant(context.Background(), uuid.New())
	err := checker.Check(ctx, ModuleFuneral, time.Now().UTC())
	if !errors.Is(err, ErrEntitlementUnavailable) {
		t.Fatalf("empresa ausente deveria bloquear como indisponível, recebido %v", err)
	}
}

func TestCheckCompanyRejeitaParametrosInvalidos(t *testing.T) {
	checker := &Checker{}
	ctx := db.WithTenant(context.Background(), uuid.New())
	if err := checker.CheckCompany(ctx, ModuleFuneral, uuid.Nil, time.Now().UTC()); !errors.Is(err, ErrModuleDisabled) {
		t.Fatalf("company nula deveria bloquear, recebido %v", err)
	}
	if err := checker.CheckCompany(ctx, "", uuid.New(), time.Now().UTC()); !errors.Is(err, ErrModuleDisabled) {
		t.Fatalf("módulo vazio deveria bloquear, recebido %v", err)
	}
	if err := checker.CheckCompany(ctx, ModuleFuneral, uuid.New(), time.Time{}); !errors.Is(err, ErrModuleDisabled) {
		t.Fatalf("instante zero deveria bloquear, recebido %v", err)
	}
}

type staticCompanyResolver struct {
	ok  bool
	err error
}

func (r staticCompanyResolver) CompanyBelongsToTenant(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return r.ok, r.err
}

func TestValidateCompanyDefesaEmProfundidade(t *testing.T) {
	tenantID, companyID := uuid.New(), uuid.New()
	tests := []struct {
		name    string
		resolve staticCompanyResolver
		want    error
	}{
		{name: "empresa do tenant", resolve: staticCompanyResolver{ok: true}},
		{name: "empresa de outro tenant", resolve: staticCompanyResolver{ok: false}, want: ErrModuleDisabled},
		{name: "falha de leitura", resolve: staticCompanyResolver{err: errors.New("timeout")}, want: ErrEntitlementUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &Checker{companyResolver: tt.resolve}
			err := checker.validateCompany(context.Background(), tenantID, companyID)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("esperado permitir, recebido %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("esperado %v, recebido %v", tt.want, err)
			}
		})
	}
}
