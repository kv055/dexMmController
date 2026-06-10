package db

import (
	"context"
	"testing"
)

func TestEnsureLNDSeedNoOpWhenMnemonicEmpty(t *testing.T) {
	repo := &TransactionRepository{}
	if err := repo.EnsureLNDSeed(context.Background(), 1, "   "); err != nil {
		t.Fatalf("EnsureLNDSeed() error = %v", err)
	}
}

func TestEnsureLNDSeedRequiresAccountID(t *testing.T) {
	repo := &TransactionRepository{}
	if err := repo.EnsureLNDSeed(context.Background(), 0, "alpha beta"); err == nil {
		t.Fatal("EnsureLNDSeed() error = nil, want account id required")
	}
}

func TestRecordLNDChannelBackupNoOpWhenEmpty(t *testing.T) {
	repo := &TransactionRepository{}
	if err := repo.RecordLNDChannelBackup(context.Background(), 1, 0, nil); err != nil {
		t.Fatalf("RecordLNDChannelBackup() error = %v", err)
	}
}
