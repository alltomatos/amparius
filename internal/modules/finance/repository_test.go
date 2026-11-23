package finance

import (
	"errors"
	"testing"
)

func TestMoneyConversion(t *testing.T) {
	tests := map[string]struct {
		cents     int64
		canonical string
	}{"0": {0, "0.00"}, "1": {100, "1.00"}, "12.3": {1230, "12.30"}, "12.34": {1234, "12.34"}, "-1.05": {-105, "-1.05"}}
	for input, test := range tests {
		got, err := numericToCents(input)
		if err != nil || got != test.cents {
			t.Errorf("numericToCents(%q) = %d, %v; want %d", input, got, err, test.cents)
		}
		if output, err := centsToNumeric(test.cents); err != nil || output != test.canonical {
			t.Errorf("centsToNumeric(%d) = %q, %v; want %q", test.cents, output, err, test.canonical)
		}
	}
	for _, input := range []string{"", ".10", "1.001", "abc", "--1", "92233720368547758.08"} {
		if _, err := numericToCents(input); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("numericToCents(%q) error = %v", input, err)
		}
	}
	if _, err := centsToNumeric(-1); err != nil {
		t.Fatalf("negative monetary value should be representable: %v", err)
	}
	if err := validateAmount(0, true); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("zero positive amount error = %v", err)
	}
	if err := validateAmount(-1, false); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("negative amount error = %v", err)
	}
}
