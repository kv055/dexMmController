package coinbase

import "testing"

func TestPositionUpdateFromCoinbaseArrayLong(t *testing.T) {
	raw := []byte(`[
		{"symbol":"ETH-PERP","net_size":"3.0"},
		{"symbol":"BTC-PERP","net_size":"0.5","entry_vwap":"50000","mark_price":"51000","unrealized_pnl":"500","liquidation_price":"40000","im_contribution":"1000"}
	]`)
	update, err := positionUpdateFromCoinbase(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if update.Venue != "coinbase_futures" || update.Instrument != "BTC-PERP" {
		t.Errorf("venue/instrument: got %s/%s", update.Venue, update.Instrument)
	}
	if update.Side != "long" || update.SizeBTC != 0.5 {
		t.Errorf("side/size: got %q/%v", update.Side, update.SizeBTC)
	}
	if update.EntryPrice != 50000 || update.MarkPrice != 51000 || update.UnrealizedPnL != 500 {
		t.Errorf("entry/mark/pnl: got %v/%v/%v", update.EntryPrice, update.MarkPrice, update.UnrealizedPnL)
	}
	if update.LiquidationPrice != 40000 || update.Margin != 1000 {
		t.Errorf("liq/margin: got %v/%v", update.LiquidationPrice, update.Margin)
	}
	if update.Currency != "USD" || !update.Connected {
		t.Errorf("currency/connected: got %q/%v", update.Currency, update.Connected)
	}
}

func TestPositionUpdateFromCoinbaseWrappedShort(t *testing.T) {
	raw := []byte(`{"positions":[{"symbol":"BTC-PERP","net_size":"-1.0","mark_price":"60000"}]}`)
	update, err := positionUpdateFromCoinbase(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if update.Side != "short" || update.SizeBTC != 1.0 {
		t.Errorf("expected short/1.0, got %q/%v", update.Side, update.SizeBTC)
	}
}

func TestPositionUpdateFromCoinbaseFlat(t *testing.T) {
	raw := []byte(`[{"symbol":"ETH-PERP","net_size":"2.0"}]`)
	update, err := positionUpdateFromCoinbase(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if update.Side != "flat" || update.SizeBTC != 0 {
		t.Errorf("expected flat/0, got %q/%v", update.Side, update.SizeBTC)
	}
}

func TestPositionUpdateFromCoinbaseBadJSON(t *testing.T) {
	if _, err := positionUpdateFromCoinbase([]byte(`not json`)); err == nil {
		t.Error("expected error for malformed JSON")
	}
}
