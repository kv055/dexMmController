package lndhealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dexMmController/internal/db"
	lnd "dexMmController/internal/wallethub"
)

type fakeRepo struct {
	account         *db.Account
	password        string
	mnemonic        string
	wallet          *db.WalletAddress
	walletErr       error
	storedBackup    *db.LNDChannelBackup
	updatedProvider string
	updatedHealthy  bool
	checkedAt       time.Time
}

func (f *fakeRepo) GetAccountByProvider(context.Context, string) (*db.Account, error) {
	if f.account == nil {
		return &db.Account{ID: 1, Provider: db.LNDAccountProvider}, nil
	}
	return f.account, nil
}

func (f *fakeRepo) LNDWalletCredentials(context.Context) (string, string, error) {
	return f.password, f.mnemonic, nil
}

func (f *fakeRepo) GetPrimaryWalletAddress() (*db.WalletAddress, error) {
	return f.wallet, f.walletErr
}

func (f *fakeRepo) LatestLNDChannelBackup(context.Context, int64) (*db.LNDChannelBackup, error) {
	return f.storedBackup, nil
}

func (f *fakeRepo) UpdateAccountHealthCheckedAt(_ context.Context, provider string, healthy bool) (time.Time, error) {
	f.updatedProvider = provider
	f.updatedHealthy = healthy
	if f.checkedAt.IsZero() {
		f.checkedAt = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	}
	return f.checkedAt, nil
}

type fakeClient struct {
	healthErr    error
	unlockCalled bool
	unlockResult bool
	unlockErr    error
	nodeInfo     *lnd.NodeInfoResponse
	nodeInfoErr  error
	channels     []lnd.Channel
	channelsErr  error
	exportErr    error
	exportRaw    []byte
	exportCount  int
}

type fakeChainBackend struct {
	pingCalled bool
	pingErr    error
}

func (f *fakeChainBackend) Ping(context.Context) error {
	f.pingCalled = true
	return f.pingErr
}

func (f *fakeClient) HealthCheck(context.Context) error {
	return f.healthErr
}

func (f *fakeClient) UnlockIfLocked(context.Context, string) (bool, error) {
	f.unlockCalled = true
	if f.unlockErr == nil && f.unlockResult {
		f.healthErr = nil
	}
	return f.unlockResult, f.unlockErr
}

func (f *fakeClient) NodeInfo(context.Context) (*lnd.NodeInfoResponse, error) {
	if f.nodeInfoErr != nil {
		return nil, f.nodeInfoErr
	}
	if f.nodeInfo != nil {
		return f.nodeInfo, nil
	}
	return &lnd.NodeInfoResponse{SyncedToChain: true, SyncedToGraph: true, BlockHeight: 900000}, nil
}

func (f *fakeClient) ListChannels(context.Context) ([]lnd.Channel, error) {
	if f.channelsErr != nil {
		return nil, f.channelsErr
	}
	return f.channels, nil
}

func (f *fakeClient) ExportChannelBackup(context.Context) error {
	return f.exportErr
}

func (f *fakeClient) LastExportedChannelBackup() ([]byte, int) {
	return f.exportRaw, f.exportCount
}

func TestIsLNDWalletProvider(t *testing.T) {
	for _, provider := range []string{"LND wallet", "lnd wallet", "lnd", "LND"} {
		if !IsLNDWalletProvider(provider) {
			t.Fatalf("IsLNDWalletProvider(%q) = false, want true", provider)
		}
	}
	if IsLNDWalletProvider("Binance") {
		t.Fatal("IsLNDWalletProvider(Binance) = true, want false")
	}
}

func TestCheckHealthyZeroChannels(t *testing.T) {
	repo := &fakeRepo{
		password: "secret",
		mnemonic: "word " + "word " + "word",
		wallet:   &db.WalletAddress{Address: "bc1qexample"},
	}
	checker := NewChecker(repo, &fakeClient{})
	result, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.Healthy {
		t.Fatalf("healthy = false, want true; message=%s", result.Message)
	}
	if repo.updatedProvider != db.LNDAccountProvider || !repo.updatedHealthy {
		t.Fatalf("persisted provider=%q healthy=%v", repo.updatedProvider, repo.updatedHealthy)
	}
}

func TestCheckUnhealthyWithoutClient(t *testing.T) {
	repo := &fakeRepo{
		password: "secret",
		wallet:   &db.WalletAddress{Address: "bc1qexample"},
	}
	result, err := NewChecker(repo, nil).Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Healthy {
		t.Fatal("healthy = true, want false")
	}
	if repo.updatedHealthy {
		t.Fatal("updatedHealthy = true, want false")
	}
}

func TestCheckUnhealthyWhenLNDDown(t *testing.T) {
	repo := &fakeRepo{
		password: "secret",
		wallet:   &db.WalletAddress{Address: "bc1qexample"},
	}
	client := &fakeClient{healthErr: errors.New("lnd down")}
	result, err := NewChecker(repo, client).Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Healthy {
		t.Fatalf("healthy = true, want false; message=%s", result.Message)
	}
}

func TestCheckAndUnlockRestoresLockedWallet(t *testing.T) {
	repo := &fakeRepo{
		password: "secret",
		wallet:   &db.WalletAddress{Address: "bc1qexample"},
	}
	client := &fakeClient{healthErr: errors.New("wallet locked"), unlockResult: true}
	chain := &fakeChainBackend{}
	result, err := NewChecker(repo, client).WithChainBackend(chain).CheckAndUnlock(context.Background())
	if err != nil {
		t.Fatalf("CheckAndUnlock: %v", err)
	}
	if !client.unlockCalled {
		t.Fatal("expected operator health check to attempt wallet unlock")
	}
	if !chain.pingCalled {
		t.Fatal("expected operator health check to ping the configured Bitcoin Core RPC")
	}
	if !result.Healthy {
		t.Fatalf("healthy = false, want true; message=%s", result.Message)
	}
	if !strings.Contains(result.Message, "wallet unlock ✓") {
		t.Fatalf("message = %q, want wallet unlock result", result.Message)
	}
}

func TestCheckAndUnlockFailsWhenBitcoinCoreIsUnavailable(t *testing.T) {
	repo := &fakeRepo{password: "secret", wallet: &db.WalletAddress{Address: "bc1qexample"}}
	chain := &fakeChainBackend{pingErr: errors.New("connection refused")}
	result, err := NewChecker(repo, &fakeClient{}).WithChainBackend(chain).CheckAndUnlock(context.Background())
	if err != nil {
		t.Fatalf("CheckAndUnlock: %v", err)
	}
	if result.Healthy {
		t.Fatalf("healthy = true, want false; message=%s", result.Message)
	}
	if !strings.Contains(result.Message, "Bitcoin Core RPC ✗") {
		t.Fatalf("message = %q, want Bitcoin Core RPC failure", result.Message)
	}
}

func TestCheckDoesNotUnlockWallet(t *testing.T) {
	repo := &fakeRepo{password: "secret", wallet: &db.WalletAddress{Address: "bc1qexample"}}
	client := &fakeClient{healthErr: errors.New("wallet locked")}
	_, err := NewChecker(repo, client).Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if client.unlockCalled {
		t.Fatal("periodic health check must not unlock the wallet")
	}
}

func TestCheckUnhealthyBackupMismatch(t *testing.T) {
	liveRaw := []byte("live-backup")
	liveHash := hashBackupBytes(liveRaw)
	repo := &fakeRepo{
		password: "secret",
		mnemonic: "seed words here",
		wallet:   &db.WalletAddress{Address: "bc1qexample"},
		storedBackup: &db.LNDChannelBackup{
			BackupSHA256: "deadbeef",
			ChannelCount: 1,
		},
	}
	client := &fakeClient{
		channels:    []lnd.Channel{{Active: true}},
		exportRaw:   liveRaw,
		exportCount: 1,
	}
	result, err := NewChecker(repo, client).Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Healthy {
		t.Fatalf("healthy = true, want false; message=%s", result.Message)
	}
	if liveHash == repo.storedBackup.BackupSHA256 {
		t.Fatal("test setup error: hashes should differ")
	}
}

func TestCheckHealthyMatchingBackup(t *testing.T) {
	liveRaw := []byte("live-backup")
	liveHash := hashBackupBytes(liveRaw)
	repo := &fakeRepo{
		password: "secret",
		mnemonic: "seed words here",
		wallet:   &db.WalletAddress{Address: "bc1qexample"},
		storedBackup: &db.LNDChannelBackup{
			BackupSHA256: liveHash,
			ChannelCount: 2,
		},
	}
	client := &fakeClient{
		channels:    []lnd.Channel{{}, {}},
		exportRaw:   liveRaw,
		exportCount: 2,
	}
	result, err := NewChecker(repo, client).Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.Healthy {
		t.Fatalf("healthy = false, want true; message=%s", result.Message)
	}
}
