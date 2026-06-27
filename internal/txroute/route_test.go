package txroute

import "testing"

func TestBidMethod(t *testing.T) {
	cases := []struct {
		dex  string
		want Method
	}{
		{"bisq", Direct},
		{"BISQ", Direct},
		{" bisq ", Direct},
		{"bisq2", Hub},
		{"hodlhodl", Hub},
		{"peachbitcoin", Hub},
		{"robosats", Swap},
		{"unknown", Hub}, // default
		{"", Hub},
	}
	for _, c := range cases {
		if got := BidMethod(c.dex); got != c.want {
			t.Errorf("BidMethod(%q) = %q, want %q", c.dex, got, c.want)
		}
	}
}

func TestNeedsHub(t *testing.T) {
	if Direct.NeedsHub() {
		t.Error("Direct should not need the hub")
	}
	if !Hub.NeedsHub() {
		t.Error("Hub should need the hub")
	}
	if !Swap.NeedsHub() {
		t.Error("Swap should need the hub")
	}
}
