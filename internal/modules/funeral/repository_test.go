package funeral

import (
	"testing"
	"time"
)

func TestBuildInstallmentsPreservesTotalAndRemainder(t *testing.T) {
	plan := &ContractPaymentPlan{
		InstallmentCount: 3,
		FirstDueDate:     time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
		IntervalMonths:   1,
	}
	installments, err := buildInstallments(10000, plan, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(installments) != 3 || installments[0].AmountCents != 3334 || installments[1].AmountCents != 3333 || installments[2].AmountCents != 3333 {
		t.Fatalf("parcelas = %#v", installments)
	}
	var total int64
	for _, installment := range installments {
		total += installment.AmountCents
	}
	if total != 10000 {
		t.Fatalf("soma = %d, esperado 10000", total)
	}
	if got := installments[1].DueDate.Format("2006-01-02"); got != "2026-03-03" {
		t.Fatalf("AddDate mensal = %s, esperado 2026-03-03", got)
	}
}

func TestBuildInstallmentsDefaultsToOneToday(t *testing.T) {
	now := time.Date(2026, 11, 25, 23, 59, 0, 0, time.UTC)
	installments, err := buildInstallments(15000, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(installments) != 1 || installments[0].AmountCents != 15000 || installments[0].DueDate.Format("2006-01-02") != "2026-11-25" {
		t.Fatalf("padrão = %#v", installments)
	}
}

func TestValidatePaymentPlanRejectsIncompleteOrExcessivePlan(t *testing.T) {
	cases := []*ContractPaymentPlan{
		{InstallmentCount: 0, FirstDueDate: time.Now().UTC(), IntervalMonths: 1},
		{InstallmentCount: 121, FirstDueDate: time.Now().UTC(), IntervalMonths: 1},
		{InstallmentCount: 2, IntervalMonths: 1},
		{InstallmentCount: 2, FirstDueDate: time.Now().UTC(), IntervalMonths: 0},
	}
	for _, plan := range cases {
		if err := validatePaymentPlan(plan); err != ErrInvalidContract {
			t.Fatalf("plano %#v: erro = %v", plan, err)
		}
	}
}
