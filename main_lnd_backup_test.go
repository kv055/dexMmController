package main

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"dexMmController/internal/db"
	lnd "dexMmController/internal/wallethub"
)

func TestMergeLNDInitConfigPrefersDatabaseMnemonic(t *testing.T) {
	fileCfg := lnd.InitConfig{
		ExtendedMasterKey: "xprv-example",
	}
	merged := mergeLNDInitConfig(fileCfg, "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty twentyone twentytwo twentythree twentyfour")
	if len(merged.CipherSeedMnemonic) != 24 {
		t.Fatalf("mnemonic words = %d, want 24", len(merged.CipherSeedMnemonic))
	}
	if merged.ExtendedMasterKey != "xprv-example" {
		t.Fatalf("extended master key was not preserved")
	}
}

func TestParseLNDBackupInterval(t *testing.T) {
	fallback := 10 * time.Minute
	if got := parseLNDBackupInterval("15m", fallback); got != 15*time.Minute {
		t.Fatalf("interval = %v, want 15m", got)
	}
	if got := parseLNDBackupInterval("invalid", fallback); got != fallback {
		t.Fatalf("invalid interval fallback = %v, want %v", got, fallback)
	}
}

type fakeLNDBackupClient struct {
	justInitialized bool
	restoredPayload []byte
	exportSnapshot  *lnd.ChanBackupSnapshot
}

func (f *fakeLNDBackupClient) WalletJustInitialized() bool {
	return f.justInitialized
}

func (f *fakeLNDBackupClient) RestoreChannelBackups(_ context.Context, multiChanBackup []byte) (uint32, error) {
	f.restoredPayload = append([]byte(nil), multiChanBackup...)
	return 1, nil
}

func (f *fakeLNDBackupClient) ExportAllChannelBackups(context.Context) (*lnd.ChanBackupSnapshot, error) {
	return f.exportSnapshot, nil
}

type fakeLNDBackupRepo struct {
	latest *db.LNDChannelBackup
	stored []byte
}

func (f *fakeLNDBackupRepo) LatestLNDChannelBackup(context.Context, int64) (*db.LNDChannelBackup, error) {
	return f.latest, nil
}

func (f *fakeLNDBackupRepo) RecordLNDChannelBackup(_ context.Context, _ int64, _ int, multiChanBackup []byte) error {
	f.stored = append([]byte(nil), multiChanBackup...)
	return nil
}

func TestRunLNDChannelBackupCycleRestoresOnlyAfterFreshInit(t *testing.T) {
	ctx := context.Background()
	existing := []byte("stored-scb")
	repo := &fakeLNDBackupRepo{
		latest: &db.LNDChannelBackup{
			ChannelCount:    1,
			MultiChanBackup: existing,
		},
	}
	raw := []byte("exported-scb")
	client := &fakeLNDBackupClient{
		exportSnapshot: &lnd.ChanBackupSnapshot{
			MultiChanBackup: &lnd.MultiChanBackup{
				MultiChanBackup: base64.StdEncoding.EncodeToString(raw),
				ChanPoints:      []lnd.ChannelPoint{{FundingTxidStr: "abc", OutputIndex: 0}},
			},
		},
	}

	if err := runLNDChannelBackupCycle(ctx, repo, client, 1); err != nil {
		t.Fatalf("runLNDChannelBackupCycle() error = %v", err)
	}
	if client.restoredPayload != nil {
		t.Fatalf("restore called on normal restart, payload = %q", client.restoredPayload)
	}
	if string(repo.stored) != string(raw) {
		t.Fatalf("stored backup = %q, want %q", repo.stored, raw)
	}

	client.restoredPayload = nil
	client.justInitialized = true
	repo.stored = nil
	if err := runLNDChannelBackupCycle(ctx, repo, client, 1); err != nil {
		t.Fatalf("runLNDChannelBackupCycle() after init error = %v", err)
	}
	if string(client.restoredPayload) != string(existing) {
		t.Fatalf("restored payload = %q, want %q", client.restoredPayload, existing)
	}
}
