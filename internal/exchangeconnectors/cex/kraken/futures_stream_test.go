package kraken

import "testing"

func TestPositionUpdateFromKrakenFrameLong(t *testing.T) {
	frame := map[string]any{
		"feed": "open_positions",
		"positions": []any{
			map[string]any{
				"instrument":            "PI_XBTUSD",
				"balance":               0.5,
				"entry_price":           50000.0,
				"mark_price":            51000.0,
				"liquidation_threshold": 40000.0,
				"pnl":                   0.01,
				"initial_margin":        0.1,
				"pnl_currency":          "BTC",
			},
		},
	}

	update, ok := positionUpdateFromKrakenFrame(frame, 0.0001)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if update.Venue != "kraken_futures" || update.Instrument != "PI_XBTUSD" {
		t.Errorf("venue/instrument: got %s/%s", update.Venue, update.Instrument)
	}
	if update.Side != "long" {
		t.Errorf("side: got %q want long", update.Side)
	}
	if update.SizeBTC != 0.5 || update.EntryPrice != 50000 || update.MarkPrice != 51000 {
		t.Errorf("size/entry/mark: got %v/%v/%v", update.SizeBTC, update.EntryPrice, update.MarkPrice)
	}
	if update.LiquidationPrice != 40000 {
		t.Errorf("liquidation: got %v", update.LiquidationPrice)
	}
	if update.UnrealizedPnL != 0.01 || update.Margin != 0.1 {
		t.Errorf("pnl/margin: got %v/%v", update.UnrealizedPnL, update.Margin)
	}
	if update.FundingRate != 0.0001 {
		t.Errorf("funding: got %v", update.FundingRate)
	}
	if update.Currency != "BTC" {
		t.Errorf("currency: got %q", update.Currency)
	}
	if !update.Connected {
		t.Error("expected connected=true")
	}
}

func TestPositionUpdateFromKrakenFrameShort(t *testing.T) {
	frame := map[string]any{
		"positions": []any{
			map[string]any{"instrument": "PI_XBTUSD", "balance": -1.25, "mark_price": 60000.0},
		},
	}
	update, ok := positionUpdateFromKrakenFrame(frame, 0)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if update.Side != "short" {
		t.Errorf("side: got %q want short", update.Side)
	}
	if update.SizeBTC != 1.25 {
		t.Errorf("size (abs): got %v want 1.25", update.SizeBTC)
	}
}

func TestPositionUpdateFromKrakenFrameFlat(t *testing.T) {
	// No PI_XBTUSD position in the frame → flat.
	frame := map[string]any{
		"positions": []any{
			map[string]any{"instrument": "PI_ETHUSD", "balance": 3.0},
		},
	}
	update, ok := positionUpdateFromKrakenFrame(frame, 0)
	if !ok {
		t.Fatal("expected ok=true (flat is a valid update)")
	}
	if update.Side != "flat" || update.SizeBTC != 0 {
		t.Errorf("expected flat/0, got %q/%v", update.Side, update.SizeBTC)
	}
}

func TestPositionUpdateFromKrakenFrameNoPositionsKey(t *testing.T) {
	if _, ok := positionUpdateFromKrakenFrame(map[string]any{"feed": "ticker"}, 0); ok {
		t.Error("expected ok=false when positions key is absent")
	}
}
