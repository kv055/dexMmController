package dexrelay

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
	lnd "dexMmController/internal/wallethub"
)

// fakeStore is a no-op txStore for the relay routing tests.
type fakeStore struct{}

func (fakeStore) GetTransactionsByStatuses([]db.TransactionStatus, string) ([]db.Transaction, error) {
	return nil, nil
}
func (fakeStore) UpdateTransaction(*db.Transaction) error { return nil }

type fakeLiquidity struct{ events *[]string }

func (f fakeLiquidity) PrepareOutbound(context.Context, string, uint, int64, int64) error {
	if f.events != nil {
		*f.events = append(*f.events, "prepare")
	}
	return nil
}
func (f fakeLiquidity) Release(context.Context, string) error {
	if f.events != nil {
		*f.events = append(*f.events, "release")
	}
	return nil
}

// fakeTaker returns a fixed deposit/invoice from Take and ResolveDeposit.
type fakeTaker struct {
	deposit string
	invoice string
	events  *[]string
}

func (f fakeTaker) Take(_ context.Context, in dexaccount.OfferInput) (*dexaccount.TakeResult, error) {
	if f.events != nil {
		*f.events = append(*f.events, "take")
	}
	return &dexaccount.TakeResult{
		Execution:      map[string]any{"offer_id": in.OfferID, "exchange": in.Exchange, "trade_id": "t1"},
		DepositAddress: f.deposit,
		LNInvoice:      f.invoice,
	}, nil
}

func (f fakeTaker) ResolveDeposit(context.Context, string, map[string]any) (string, string, error) {
	return f.deposit, f.invoice, nil
}

// fakeLND records which relay primitive the worker invoked.
type fakeLND struct {
	sendCalls    int
	payCalls     int
	lastSendAddr string
	events       *[]string
	decodedSat   string
}

func (f *fakeLND) SendOnChain(_ context.Context, addr string, _, _ int64) (string, error) {
	f.sendCalls++
	f.lastSendAddr = addr
	return "txid-deadbeef", nil
}
func (f *fakeLND) PayInvoice(context.Context, string, int64) (string, error) {
	f.payCalls++
	if f.events != nil {
		*f.events = append(*f.events, "pay")
	}
	return "payment-hash", nil
}
func (f *fakeLND) DecodeInvoice(context.Context, string) (*lnd.DecodedInvoice, error) {
	amount := f.decodedSat
	if amount == "" {
		amount = "1000000"
	}
	return &lnd.DecodedInvoice{Destination: "03dest", PaymentHash: "hash", NumSatoshis: amount, Timestamp: strconv.FormatInt(time.Now().Unix(), 10), Expiry: "3600"}, nil
}
func (f *fakeLND) ConfirmedBalanceForAddress(context.Context, string) (int64, error) { return 0, nil }

func metaFor(exchange string) json.RawMessage {
	sel, _ := json.Marshal(map[string]any{"offer_id": "offer-1", "exchange": exchange})
	settlement := "onchain_hub"
	if exchange == "bisq" {
		settlement = "onchain_direct"
	} else if exchange == "robosats" {
		settlement = "onchain_swap"
	}
	raw, _ := db.MarshalDexOfferCreation(db.DexOfferMeta{SelectedOffer: sel, ResolvedRoute: &db.ResolvedRoute{ConfigVersion: 1, Provider: "binance", BTCAmount: 0.01, RequiredBTCLiquidity: 0.01, RequiredCapital: 1, Settlement: settlement}})
	return raw
}

// TestRelayToDEX_RoutesByMethod asserts the worker forwards funds per route: direct
// (bisq v1) sends nothing, hub forwards on-chain to the resolved deposit address, and
// swap (robosats) pays the hold invoice over Lightning.
func TestRelayToDEX_RoutesByMethod(t *testing.T) {
	cases := []struct {
		name           string
		exchange       string
		taker          fakeTaker
		preDestination string // destination_address already on the row (direct route)
		wantSend       int
		wantPay        int
		wantSendAddr   string
		wantMethod     string
		wantDestAddr   string
	}{
		{
			name:           "direct bisq v1 sends nothing",
			exchange:       "bisq",
			taker:          fakeTaker{},
			preDestination: "bc1qbisqwallet",
			wantSend:       0,
			wantPay:        0,
			wantMethod:     "direct",
			wantDestAddr:   "bc1qbisqwallet",
		},
		{
			name:         "hub forwards on-chain to deposit",
			exchange:     "hodlhodl",
			taker:        fakeTaker{deposit: "bc1qdeposit"},
			wantSend:     1,
			wantPay:      0,
			wantSendAddr: "bc1qdeposit",
			wantMethod:   "onchain",
			wantDestAddr: "bc1qdeposit",
		},
		{
			name:       "swap pays the hold invoice",
			exchange:   "robosats",
			taker:      fakeTaker{invoice: "lnbc1test"},
			wantSend:   0,
			wantPay:    1,
			wantMethod: "lightning",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lnd := &fakeLND{}
			w := &Worker{
				repo:      fakeStore{},
				taker:     c.taker,
				lnd:       lnd,
				satPerVb:  2,
				liquidity: fakeLiquidity{},
			}
			tx := &db.Transaction{
				ID:                 1,
				Status:             db.StatusCexCryptoWithdrawalArrived,
				Dex:                db.DexBisq,
				CexBitcoinAmount:   0.01,
				DestinationAddress: c.preDestination,
				DexOfferCreation:   metaFor(c.exchange),
			}

			if err := w.relayToDEX(context.Background(), tx); err != nil {
				t.Fatalf("relayToDEX: %v", err)
			}

			if lnd.sendCalls != c.wantSend {
				t.Errorf("SendOnChain calls = %d, want %d", lnd.sendCalls, c.wantSend)
			}
			if lnd.payCalls != c.wantPay {
				t.Errorf("PayInvoice calls = %d, want %d", lnd.payCalls, c.wantPay)
			}
			if c.wantSendAddr != "" && lnd.lastSendAddr != c.wantSendAddr {
				t.Errorf("SendOnChain addr = %q, want %q", lnd.lastSendAddr, c.wantSendAddr)
			}
			if c.wantDestAddr != "" && tx.DestinationAddress != c.wantDestAddr {
				t.Errorf("DestinationAddress = %q, want %q", tx.DestinationAddress, c.wantDestAddr)
			}
			if tx.Status != db.StatusDexPositionOpen {
				t.Errorf("status = %s, want %s", tx.Status, db.StatusDexPositionOpen)
			}

			var relay db.CexToDexRelayDetails
			if err := json.Unmarshal(tx.CexToDexWithdrawalTxDetails, &relay); err != nil {
				t.Fatalf("unmarshal relay details: %v", err)
			}
			if relay.PaymentMethod != c.wantMethod {
				t.Errorf("relay payment_method = %q, want %q", relay.PaymentMethod, c.wantMethod)
			}
		})
	}
}

func TestRelayToDEXPreparesBeforeTakeAndBlocksOversizedInvoice(t *testing.T) {
	events := []string{}
	lndClient := &fakeLND{events: &events, decodedSat: "1000001"}
	w := &Worker{
		repo:      fakeStore{},
		taker:     fakeTaker{invoice: "lnbc1test", events: &events},
		lnd:       lndClient,
		liquidity: fakeLiquidity{events: &events},
		satPerVb:  2,
	}
	tx := &db.Transaction{
		ID:               9,
		Status:           db.StatusCexCryptoWithdrawalArrived,
		Dex:              db.DexRoboSats,
		CexBitcoinAmount: 0.0101,
		DexOfferCreation: metaFor("robosats"),
	}
	if err := w.relayToDEX(context.Background(), tx); err == nil {
		t.Fatal("expected oversized invoice to require review")
	}
	if tx.Status != db.StatusDexTakenFundingNeedsReview || lndClient.payCalls != 0 {
		t.Fatalf("status=%s payCalls=%d", tx.Status, lndClient.payCalls)
	}
	want := []string{"prepare", "take", "release"}
	if len(events) != len(want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events=%v want=%v", events, want)
		}
	}
}
