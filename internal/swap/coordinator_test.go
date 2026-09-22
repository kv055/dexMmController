package swap

import (
	"context"
	"testing"
	"time"
)

type fakeProvider struct {
	statuses         []RemoteStatus
	creates, refunds int
}

func (f *fakeProvider) Quote(context.Context, int64) (Quote, error) {
	return Quote{LightningOutputSat: 1000, LockupAmountSat: 1030, ServiceFeeSat: 20, MinerFeeSat: 10, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (f *fakeProvider) Create(context.Context, Quote) (Prepared, error) {
	f.creates++
	return Prepared{ID: "swap-1", LockupAddress: "bc1qlock", InvoiceHash: "hash", RescueMaterial: "secret"}, nil
}
func (f *fakeProvider) Status(context.Context, string) (RemoteStatus, error) {
	if len(f.statuses) == 0 {
		return RemoteStatus{Settled: true}, nil
	}
	s := f.statuses[0]
	f.statuses = f.statuses[1:]
	return s, nil
}
func (f *fakeProvider) Refund(context.Context, string) error { f.refunds++; return nil }

type fakeWallet struct {
	calls int
}

func (w *fakeWallet) SendOnChain(context.Context, string, int64, int64) (string, error) {
	w.calls++
	return "tx-1", nil
}

type fakeSealer struct{}

func (fakeSealer) Seal(v string) ([]byte, error) { return []byte("sealed:" + v), nil }

type fakeStore struct {
	rows       map[string]Record
	incomplete []Record
	funded     int
	reserved   bool
}

func (s *fakeStore) Reserve(context.Context, string, uint, int64, int64, int64, int64, int64, int64, time.Time) error {
	s.reserved = true
	return nil
}
func (s *fakeStore) ReleaseReservation(context.Context, string) error             { s.reserved = false; return nil }
func (s *fakeStore) FinalizeReservation(context.Context, string, time.Time) error { return nil }
func (s *fakeStore) Create(_ context.Context, r Record, _ []byte) error {
	if s.rows == nil {
		s.rows = map[string]Record{}
	}
	s.rows[r.ID] = r
	return nil
}
func (s *fakeStore) MarkFundingPending(context.Context, string) error { return nil }
func (s *fakeStore) MarkFunded(_ context.Context, id, tx string) error {
	s.funded++
	r := s.rows[id]
	r.LockupTxID = tx
	s.rows[id] = r
	return nil
}
func (s *fakeStore) SetState(_ context.Context, id, state string) error {
	r := s.rows[id]
	r.State = state
	s.rows[id] = r
	return nil
}
func (s *fakeStore) Incomplete(context.Context) ([]Record, error) { return s.incomplete, nil }

func TestCoordinatorFundsExactlyOnceAndWaitsForSettlement(t *testing.T) {
	p := &fakeProvider{statuses: []RemoteStatus{{State: "pending"}, {Settled: true}}}
	w := &fakeWallet{}
	s := &fakeStore{}
	c := Coordinator{Provider: p, Wallet: w, Store: s, Sealer: fakeSealer{}, MaxFeeSat: 100, MaxFeePPM: 50000, PollInterval: time.Millisecond, timeOut: time.Second}
	r, err := c.Execute(context.Background(), "r1", 2, 1000, 2000, 1000, 2000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateSettled || w.calls != 1 || s.funded != 1 || !s.reserved {
		t.Fatalf("record=%+v calls=%d funded=%d reserved=%v", r, w.calls, s.funded, s.reserved)
	}
}
func TestCoordinatorCooperativeRefundOnFundedFailure(t *testing.T) {
	p := &fakeProvider{statuses: []RemoteStatus{{Failed: true, Refundable: true}}}
	s := &fakeStore{}
	c := Coordinator{Provider: p, Wallet: &fakeWallet{}, Store: s, Sealer: fakeSealer{}, MaxFeeSat: 100, MaxFeePPM: 50000}
	r, err := c.Execute(context.Background(), "r1", 2, 1000, 2000, 1000, 2000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateRefunded || p.refunds != 1 {
		t.Fatalf("record=%+v refunds=%d", r, p.refunds)
	}
}
func TestRecoveryNeverCreatesOrFunds(t *testing.T) {
	p := &fakeProvider{statuses: []RemoteStatus{{Settled: true}}}
	w := &fakeWallet{}
	s := &fakeStore{rows: map[string]Record{}, incomplete: []Record{{ID: "existing", State: StateFunded}}}
	c := Coordinator{Provider: p, Wallet: w, Store: s, Sealer: fakeSealer{}}
	if err := c.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.creates != 0 || w.calls != 0 {
		t.Fatalf("recovery created=%d funded=%d", p.creates, w.calls)
	}
}
