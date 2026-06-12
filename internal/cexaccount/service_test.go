package cexaccount

import (
	"encoding/json"
	"testing"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/cex/coinbase"
)

func TestHedgePositionFromTransactionActiveVsPast(t *testing.T) {
	openDetails, _ := json.Marshal(map[string]string{
		"venue":      "kraken_futures",
		"instrument": "PI_XBTUSD",
	})
	closeDetails, _ := json.Marshal(map[string]string{"status": "closed"})

	active := hedgePositionFromTransaction(db.Transaction{
		ID:                            1,
		Direction:                     "BUY",
		Status:                        db.StatusCexTradeSuccessful,
		CexBitcoinAmount:              0.01,
		CexCounterPositionOpenDetails: openDetails,
		CexCounterPositionFee:         0.0001,
		CreatedAt:                     time.Now().UTC(),
	})
	if active.Venue != "" && active.Instrument != "PI_XBTUSD" {
		t.Fatalf("instrument = %q, want PI_XBTUSD", active.Instrument)
	}

	past := db.Transaction{
		ID:                             2,
		Direction:                      "SELL",
		Status:                         db.StatusDexPositionReleased,
		CexCounterPositionOpenDetails:  openDetails,
		CexCounterPositionCloseDetails: closeDetails,
		TransactionComplete:            true,
		UpdatedAt:                      time.Now().UTC(),
	}
	if !hasJSONData(past.CexCounterPositionCloseDetails) {
		t.Fatal("expected close details to be present")
	}
}

func TestBalancesFromCoinbase(t *testing.T) {
	resp := balancesFromCoinbase([]coinbase.Account{
		{
			Currency: "BTC",
			Active:   true,
			AvailableBalance: coinbase.AccountAmount{Value: "0.25", Currency: "BTC"},
		},
		{
			Currency: "EUR",
			Active:   true,
			AvailableBalance: coinbase.AccountAmount{Value: "42.10", Currency: "EUR"},
		},
	})
	if resp.Bitcoin == nil || resp.Bitcoin.Total != 0.25 {
		t.Fatalf("bitcoin balance = %#v, want 0.25 BTC", resp.Bitcoin)
	}
	if len(resp.Fiat) != 1 || resp.Fiat[0].Asset != "EUR" {
		t.Fatalf("fiat balances = %#v, want EUR only", resp.Fiat)
	}
}

func TestBalancesFromKraken(t *testing.T) {
	resp := balancesFromKraken(map[string]string{
		"XXBT": "0.25",
		"ZEUR": "1500.50",
		"ZUSD": "0",
	})
	if resp.Bitcoin == nil || resp.Bitcoin.Total != 0.25 {
		t.Fatalf("bitcoin balance = %#v, want 0.25 BTC", resp.Bitcoin)
	}
	if len(resp.Fiat) != 1 || resp.Fiat[0].Asset != "EUR" {
		t.Fatalf("fiat balances = %#v, want EUR only", resp.Fiat)
	}
}
