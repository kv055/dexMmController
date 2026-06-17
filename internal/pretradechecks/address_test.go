package pretradechecks

import "testing"

func TestMatchWhitelistedLNDAddress(t *testing.T) {
	tests := []struct {
		name   string
		wallet []string
		cex    []string
		want   string
	}{
		{
			name:   "first wallet match wins",
			wallet: []string{"bc1qaaa", "bc1qbbb"},
			cex:    []string{"bc1qbbb", "bc1qaaa"},
			want:   "bc1qaaa",
		},
		{
			name:   "case insensitive",
			wallet: []string{"BC1QWALLET"},
			cex:    []string{"bc1qwallet"},
			want:   "BC1QWALLET",
		},
		{
			name:   "no match",
			wallet: []string{"bc1qwallet"},
			cex:    []string{"bc1qother"},
			want:   "",
		},
		{
			name:   "skips empty entries",
			wallet: []string{"", "bc1qwallet"},
			cex:    []string{"bc1qwallet"},
			want:   "bc1qwallet",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchWhitelistedLNDAddress(tc.wallet, tc.cex)
			if got != tc.want {
				t.Errorf("MatchWhitelistedLNDAddress() = %q, want %q", got, tc.want)
			}
		})
	}
}
