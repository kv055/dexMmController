package main

import (
	"context"
	"testing"
	"time"

	"dexTakerController/internal/db"
	lnd "dexTakerController/internal/wallethub"
)

func TestMergeLNDInitConfigPrefersDatabaseMnemonic(t *testing.T) {
	fileCfg := lnd.InitConfig{}
	merged := mergeLNDInitConfig(fileCfg, "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty twentyone twentytwo twentythree twentyfour")
	if len(merged.CipherSeedMnemonic) != 24 {
		t.Fatalf("mnemonic words = %d, want 24", len(merged.CipherSeedMnemonic))
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

type fakeLNDChannelBackupRepo struct {
	latest *db.LNDChannelBackup
	stored []byte
}

func (f *fakeLNDChannelBackupRepo) LatestLNDChannelBackup(context.Context, int64) (*db.LNDChannelBackup, error) {
	return f.latest, nil
}

func (f *fakeLNDChannelBackupRepo) RecordLNDChannelBackup(_ context.Context, _ int64, _ int, multiChanBackup []byte) error {
	f.stored = append([]byte(nil), multiChanBackup...)
	return nil
}

func TestLoadLNDChannelBackupRestore(t *testing.T) {
	repo := &fakeLNDChannelBackupRepo{
		latest: &db.LNDChannelBackup{
			MultiChanBackup: []byte("stored-scb"),
			ChannelCount:    2,
		},
	}
	got := loadLNDChannelBackupRestore(context.Background(), repo, 1)
	if string(got) != "stored-scb" {
		t.Fatalf("restore bytes = %q, want stored-scb", got)
	}
}
