package lndhealth

import (
	"context"
	"errors"
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
	healthErr   error
	nodeInfo    *lnd.NodeInfoResponse
	nodeInfoErr error
	channels    []lnd.Channel
	channelsErr error
	exportErr   error
	exportRaw   []byte
	exportCount int
}

func (f *fakeClient) HealthCheck(context.Context) error {
	return f.healthErr
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
