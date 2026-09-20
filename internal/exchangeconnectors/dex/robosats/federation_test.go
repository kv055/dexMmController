package robosats

import "testing"

func TestTradeRefRoundTrip(t *testing.T) {
	if got := TradeRef("expats", "42"); got != "expats:42" {
		t.Fatalf("TradeRef = %q", got)
	}
	coord, order := SplitTradeRef("expats:42")
	if coord != "expats" || order != "42" {
		t.Fatalf("SplitTradeRef = %q %q", coord, order)
	}
	if coord, order = SplitTradeRef("42"); coord != "" || order != "42" {
		t.Fatalf("legacy SplitTradeRef = %q %q", coord, order)
	}
}
