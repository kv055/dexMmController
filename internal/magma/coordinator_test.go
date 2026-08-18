package magma

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"dexMmController/internal/lsp"
	lnd "dexMmController/internal/wallethub"
)

type fakeStore struct {
	rows map[string]OrderRecord
}

func (s *fakeStore) Create(_ context.Context, row OrderRecord) error {
	if s.rows == nil {
		s.rows = map[string]OrderRecord{}
	}
	s.rows[row.ID] = row
	return nil
}
func (s *fakeStore) GetByID(_ context.Context, id string) (OrderRecord, error) {
	row, ok := s.rows[id]
	if !ok {
		return OrderRecord{}, errors.New("missing")
	}
	return row, nil
}
func (s *fakeStore) List(_ context.Context, _ int) ([]OrderRecord, error) {
	out := make([]OrderRecord, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, row)
	}
	return out, nil
}
func (s *fakeStore) Incomplete(_ context.Context) ([]OrderRecord, error) {
	var out []OrderRecord
	for _, row := range s.rows {
		if !TerminalOrderState(row.OrderState) {
			out = append(out, row)
		}
	}
	return out, nil
}
func (s *fakeStore) Update(_ context.Context, row OrderRecord) error {
	s.rows[row.ID] = row
	return nil
}
func (s *fakeStore) MarkPayment(_ context.Context, id, paymentHash string) error {
	row := s.rows[id]
	row.PaymentHash = paymentHash
	s.rows[id] = row
	return nil
}

type fakeWallet struct {
	pubkey          string
	syncedToChain   bool
	blockHeight     uint32
	confirmedSat    int64
	localBalanceSat int64
	invoiceSat      int64
}

func (f fakeWallet) NodeInfo(context.Context) (*lnd.NodeInfoResponse, error) {
	return &lnd.NodeInfoResponse{
		IdentityPubkey: f.pubkey,
		SyncedToChain:  f.syncedToChain,
		SyncedToGraph:  false,
		BlockHeight:    f.blockHeight,
	}, nil
}
func (f fakeWallet) OnChainBalance(context.Context) (*lnd.OnChainBalanceResponse, error) {
	value := strconv.FormatInt(f.confirmedSat, 10)
	return &lnd.OnChainBalanceResponse{ConfirmedBalance: value, TotalBalance: value}, nil
}
func (f fakeWallet) ListChannels(context.Context) ([]lnd.Channel, error) {
	return []lnd.Channel{{
		ChannelPoint: "txid:0",
		RemotePubkey: "03remote",
		Active:       true,
		LocalBalance: strconv.FormatInt(f.localBalanceSat, 10),
	}}, nil
}
func (fakeWallet) ListPeers(context.Context) ([]lnd.Peer, error)     { return nil, nil }
func (fakeWallet) ConnectPeer(context.Context, string, string) error { return nil }
func (fakeWallet) PayInvoice(context.Context, string, int64) (*lnd.PayInvoiceResponse, error) {
	return &lnd.PayInvoiceResponse{}, nil
}
func (f fakeWallet) DecodeInvoice(context.Context, string) (*lnd.DecodedInvoice, error) {
	return &lnd.DecodedInvoice{PaymentHash: "hash", NumSatoshis: strconv.FormatInt(f.invoiceSat, 10)}, nil
}

type fakeRegistrar struct {
	updated lsp.Config
}

func (f *fakeRegistrar) Get(context.Context) (lsp.Config, error) {
	return lsp.Config{}, ErrLSPConfigNotFound
}
func (f *fakeRegistrar) Update(_ context.Context, cfg lsp.Config) (lsp.Config, error) {
	f.updated = cfg
	return cfg, nil
}

func TestCoordinatorSyncAutoRegistersCompletedOrder(t *testing.T) {
	store := &fakeStore{rows: map[string]OrderRecord{
		"id-1": {ID: "id-1", Pool: "recommended", OrderID: "order-1", OrderState: "CREATED"},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Order{
			OrderID:    "order-1",
			OrderState: "COMPLETED",
			Payment:    Payment{Bolt11: PaymentBolt11{State: "PAID"}},
			Channel:    &Channel{FundingOutpoint: "txid:0"},
		})
	}))
	defer server.Close()

	registrar := &fakeRegistrar{}
	coordinator := &Coordinator{
		Client:       NewClient(server.URL + "/api/lsp/v1"),
		Store:        store,
		LSPRegistrar: registrar,
		Wallet:       func(context.Context) LNDWallet { return fakeWallet{pubkey: "03local", syncedToChain: true} },
	}

	updated, err := coordinator.syncOrder(context.Background(), store.rows["id-1"])
	if err != nil {
		t.Fatalf("syncOrder: %v", err)
	}
	if updated.OrderState != "COMPLETED" || !updated.AutoRegistered {
		t.Fatalf("updated = %+v", updated)
	}
	if registrar.updated.ChannelPoint != "txid:0" || registrar.updated.ProviderLabel != providerLabel {
		t.Fatalf("registrar = %+v", registrar.updated)
	}
}

func TestCoordinatorPayOrderIsIdempotentWhenPaid(t *testing.T) {
	store := &fakeStore{rows: map[string]OrderRecord{
		"id-1": {ID: "id-1", OrderState: "CREATED", PaymentState: "PAID", Bolt11Invoice: "lnbc..."},
	}}
	coordinator := &Coordinator{
		Store:  store,
		Wallet: func(context.Context) LNDWallet { return fakeWallet{pubkey: "03local", syncedToChain: true} },
	}
	record, err := coordinator.PayOrder(context.Background(), "id-1", 1000)
	if err != nil {
		t.Fatalf("PayOrder: %v", err)
	}
	if record.PaymentState != "PAID" {
		t.Fatalf("record = %+v", record)
	}
}

func TestGetInfoReportsWalletNotReadyWhenBalanceZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Info{MinInitialLSPBalanceSat: "500000", MaxInitialLSPBalanceSat: "100000000"})
	}))
	defer server.Close()

	coordinator := &Coordinator{
		Client: NewClient(server.URL + "/api/lsp/v1"),
		Wallet: func(context.Context) LNDWallet {
			return fakeWallet{pubkey: "03local", syncedToChain: true, confirmedSat: 0, localBalanceSat: 0}
		},
	}
	info, err := coordinator.GetInfo(context.Background(), PoolRecommended)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if info.WalletReady || info.OutboundSat != 0 || info.ConfirmedBalanceSat != 0 {
		t.Fatalf("expected empty wallet, got %+v", info)
	}
}

func TestCreateOrderRejectsWhenLightningOutboundMissing(t *testing.T) {
	coordinator := &Coordinator{
		Client: NewClient("http://magma.test"),
		Store:  &fakeStore{},
		Wallet: func(context.Context) LNDWallet {
			return fakeWallet{pubkey: "03local", syncedToChain: true, confirmedSat: 0, localBalanceSat: 0}
		},
	}
	_, err := coordinator.CreateOrder(context.Background(), CreateParams{
		Pool: PoolRecommended, LSPBalanceSat: 500000, ChannelExpiryBlocks: 144,
	})
	if err == nil || !strings.Contains(err.Error(), "Lightning outbound is too low") {
		t.Fatalf("CreateOrder error = %v", err)
	}
}

func TestPayOrderRejectsWhenOutboundBelowInvoice(t *testing.T) {
	store := &fakeStore{rows: map[string]OrderRecord{
		"id-1": {ID: "id-1", OrderState: "CREATED", PaymentState: "EXPECT_PAYMENT", Bolt11Invoice: "lnbc...", OrderTotalSat: 12000},
	}}
	coordinator := &Coordinator{
		Store: store,
		Wallet: func(context.Context) LNDWallet {
			return fakeWallet{pubkey: "03local", syncedToChain: true, localBalanceSat: 500, invoiceSat: 12000}
		},
	}
	_, err := coordinator.PayOrder(context.Background(), "id-1", 1000)
	if err == nil || !strings.Contains(err.Error(), "Lightning outbound is too low") {
		t.Fatalf("PayOrder error = %v", err)
	}
}
