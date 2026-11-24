package catalog

import "testing"

func TestClampListLimit(t *testing.T) {
	cases := map[int]int{
		-1:               DefaultListLimit,
		0:                DefaultListLimit,
		1:                1,
		MaxListLimit:     MaxListLimit,
		MaxListLimit + 1: MaxListLimit,
		10_000:           MaxListLimit,
	}
	for input, want := range cases {
		if got := clampListLimit(input); got != want {
			t.Fatalf("clampListLimit(%d) = %d, quer %d", input, got, want)
		}
	}
}

func TestMoneyConversion(t *testing.T) {
	cases := []struct {
		cents   int64
		decimal string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{150000, "1500.00"},
		{-2599, "-25.99"},
		{9223372036854775807, "92233720368547758.07"},
	}
	for _, tc := range cases {
		decimal := centsToDecimal(tc.cents)
		if decimal != tc.decimal {
			t.Fatalf("centsToDecimal(%d) = %q, quer %q", tc.cents, decimal, tc.decimal)
		}
		back, err := decimalToCents(decimal)
		if err != nil {
			t.Fatalf("decimalToCents(%q): %v", decimal, err)
		}
		if back != tc.cents {
			t.Fatalf("roundtrip de %d = %d", tc.cents, back)
		}
	}
	if _, err := decimalToCents("1.234"); err == nil {
		t.Fatal("decimalToCents aceitou três casas")
	}
}

func TestNewRepositoryRejectsNilDatabase(t *testing.T) {
	if _, err := NewRepository(nil); err == nil {
		t.Fatal("NewRepository(nil) deveria falhar")
	}
}
