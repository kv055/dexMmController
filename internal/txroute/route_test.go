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

func TestMethodForAsk(t *testing.T) {
	cases := []struct {
		dex  string
		want Method
	}{
		{"bisq", Direct},
		{"bisq2", Direct},
		{"hodlhodl", Direct},
		{"peachbitcoin", Direct},
		{"robosats", Swap},
		{"unknown", Direct},
	}
	for _, c := range cases {
		if got := MethodFor(c.dex, SideAsk); got != c.want {
			t.Errorf("MethodFor(%q, ask) = %q, want %q", c.dex, got, c.want)
		}
	}
}

func TestHasLightningLeg(t *testing.T) {
	if Direct.HasLightningLeg() || Hub.HasLightningLeg() {
		t.Fatal("Direct/Hub must not report a Lightning leg")
	}
	if !Swap.HasLightningLeg() {
		t.Fatal("Swap must report a Lightning leg")
	}
}

func TestNormalizeSide(t *testing.T) {
	if NormalizeSide("ask") != SideAsk || NormalizeSide("ASK") != SideAsk {
		t.Fatal("expected ask")
	}
	if NormalizeSide("bid") != SideBid || NormalizeSide("") != SideBid {
		t.Fatal("expected bid default")
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
