package binance

import "testing"

func TestApplyAccountUpdateLong(t *testing.T) {
	data := map[string]any{
		"e": "ACCOUNT_UPDATE",
		"a": map[string]any{
			"P": []any{
				map[string]any{"s": "BTCUSDT", "pa": "0.500", "ep": "50000.0", "up": "12.5", "iw": "100.0"},
				map[string]any{"s": "ETHUSDT", "pa": "2.0"},
			},
		},
	}
	var snap snapshot
	if !applyAccountUpdate(data, &snap) {
		t.Fatal("expected BTCUSDT position to be applied")
	}
	if snap.side != "long" || snap.sizeBTC != 0.5 {
		t.Errorf("side/size: got %q/%v", snap.side, snap.sizeBTC)
	}
	if snap.entryPrice != 50000 || snap.unrealized != 12.5 || snap.margin != 100 {
		t.Errorf("entry/pnl/margin: got %v/%v/%v", snap.entryPrice, snap.unrealized, snap.margin)
	}
}

func TestApplyAccountUpdateShort(t *testing.T) {
	data := map[string]any{
		"a": map[string]any{
			"P": []any{map[string]any{"s": "BTCUSDT", "pa": "-0.25"}},
		},
	}
	var snap snapshot
	applyAccountUpdate(data, &snap)
	if snap.side != "short" || snap.sizeBTC != 0.25 {
		t.Errorf("expected short/0.25, got %q/%v", snap.side, snap.sizeBTC)
	}
}

func TestApplyAccountUpdateNoMatch(t *testing.T) {
	data := map[string]any{"a": map[string]any{"P": []any{map[string]any{"s": "ETHUSDT", "pa": "1.0"}}}}
	var snap snapshot
	if applyAccountUpdate(data, &snap) {
		t.Error("expected no BTCUSDT position")
	}
}

func TestApplyMarkPriceAndSnapshot(t *testing.T) {
	var snap snapshot
	applyAccountUpdate(map[string]any{"a": map[string]any{"P": []any{
		map[string]any{"s": "BTCUSDT", "pa": "0.5", "ep": "50000", "up": "5", "iw": "80"},
	}}}, &snap)
	applyMarkPrice(map[string]any{"s": "BTCUSDT", "p": "51000.0", "r": "0.0001"}, &snap)

	update := updateFromSnapshot(snap)
	if update.Venue != "binance_futures" || update.Instrument != "BTCUSDT" {
		t.Errorf("venue/instrument: got %s/%s", update.Venue, update.Instrument)
	}
	if update.MarkPrice != 51000 || update.FundingRate != 0.0001 {
		t.Errorf("mark/funding: got %v/%v", update.MarkPrice, update.FundingRate)
	}
	if update.Side != "long" || update.SizeBTC != 0.5 || update.EntryPrice != 50000 {
		t.Errorf("side/size/entry: got %q/%v/%v", update.Side, update.SizeBTC, update.EntryPrice)
	}
	if update.Currency != "USDT" || !update.Connected {
		t.Errorf("currency/connected: got %q/%v", update.Currency, update.Connected)
	}
}

func TestUpdateFromSnapshotDefaultsFlat(t *testing.T) {
	update := updateFromSnapshot(snapshot{})
	if update.Side != "flat" {
		t.Errorf("expected flat, got %q", update.Side)
	}
}
