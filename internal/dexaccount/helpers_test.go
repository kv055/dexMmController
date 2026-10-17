package dexaccount

import (
	"math"
	"testing"
)

func TestBTCToSatsRejectsNonFiniteAndOverflow(t *testing.T) {
	const maxSats = int64(21_000_000) * 100_000_000
	cases := []struct {
		name string
		in   float64
		want int64
	}{
		{"one btc", 1.0, 100_000_000},
		{"dust", 0.00000001, 1},
		{"zero", 0, 0},
		{"negative", -2.5, 0},
		{"nan", math.NaN(), 0},
		{"plus inf", math.Inf(1), maxSats},
		{"huge finite", 1e20, maxSats},
		{"exact supply", 21_000_000, maxSats},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BTCToSats(tc.in); got != tc.want {
				t.Fatalf("BTCToSats(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
