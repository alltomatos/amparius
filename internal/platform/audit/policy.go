package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alltomatos/amparius/internal/platform/db"
)

// ErrPolicyUnavailable sinaliza que não há política de privacidade aprovada
// pelo responsável de privacidade/DPO para a finalidade pedida. É sempre
// falha fechada: nenhum chamador deve inventar base legal, finalidade ou
// prazo de retenção na ausência de um provedor configurado (ADR 0030).
var ErrPolicyUnavailable = errors.New("política de privacidade do tenant indisponível")

// Policy é a finalidade, base legal e retenção aprovadas pelo DPO do tenant
// para uma chave de operação. Os três valores vêm sempre de fora da
// plataforma (ADR 0030 §5); nenhum deles tem default de produção.
type Policy struct {
	PurposeCode    string
	LegalBasisCode string
	RetentionUntil time.Time
}

// PolicyProvider resolve a política vigente de uma finalidade, no tenant do
// contexto. Não existe implementação de produção nesta base de código: criar
// uma exigiria administração do DPO (cadastro, aprovação e auditoria da
// própria política), fora do escopo de qualquer caso de uso que apenas
// consome a decisão já aprovada. Testes injetam implementações sintéticas,
// identificadas como tal, nunca como política real.
type PolicyProvider interface {
	Resolve(ctx context.Context, purposeKey string) (Policy, error)
}

// ResolvePolicy obtains and validates the approved policy for an operation.
func ResolvePolicy(ctx context.Context, provider PolicyProvider, purposeKey string) (Policy, error) {
	if _, err := db.TenantFromContext(ctx); err != nil {
		return Policy{}, err
	}
	if provider == nil {
		return Policy{}, ErrPolicyUnavailable
	}
	policy, err := provider.Resolve(ctx, purposeKey)
	if err != nil {
		return Policy{}, fmt.Errorf("%w: %w", ErrPolicyUnavailable, err)
	}
	if !codePattern.MatchString(policy.PurposeCode) || !codePattern.MatchString(policy.LegalBasisCode) ||
		!policy.RetentionUntil.After(time.Now()) {
		return Policy{}, ErrPolicyUnavailable
	}
	return policy, nil
}
