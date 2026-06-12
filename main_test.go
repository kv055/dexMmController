package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"dexMmController/internal/db"
)

func TestLoadLNDInitConfigFromMnemonicFiles(t *testing.T) {
	dir := t.TempDir()
	mnemonicPath := filepath.Join(dir, "mnemonic.txt")
	passphrasePath := filepath.Join(dir, "passphrase.txt")
	if err := os.WriteFile(mnemonicPath, []byte("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega\n"), 0600); err != nil {
		t.Fatalf("write mnemonic: %v", err)
	}
	if err := os.WriteFile(passphrasePath, []byte("secret-passphrase\n"), 0600); err != nil {
		t.Fatalf("write passphrase: %v", err)
	}

	cfg, err := loadLNDInitConfig(mnemonicPath, passphrasePath, "", "", "250", "")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	wantWords := []string{
		"alpha", "beta", "gamma", "delta", "epsilon", "zeta",
		"eta", "theta", "iota", "kappa", "lambda", "mu",
		"nu", "xi", "omicron", "pi", "rho", "sigma",
		"tau", "upsilon", "phi", "chi", "psi", "omega",
	}
	if !reflect.DeepEqual(cfg.CipherSeedMnemonic, wantWords) {
		t.Fatalf("unexpected mnemonic: got %#v want %#v", cfg.CipherSeedMnemonic, wantWords)
	}
	if cfg.AEZEEDPassphrase != "secret-passphrase" {
		t.Fatalf("unexpected passphrase: %q", cfg.AEZEEDPassphrase)
	}
	if cfg.RecoveryWindow != 250 {
		t.Fatalf("unexpected recovery window: %d", cfg.RecoveryWindow)
	}
}

func TestLoadLNDInitConfigFromExtendedMasterKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "xprv.txt")
	birthdayPath := filepath.Join(dir, "birthday.txt")
	if err := os.WriteFile(keyPath, []byte("xprv-example-key\n"), 0600); err != nil {
		t.Fatalf("write xprv: %v", err)
	}
	if err := os.WriteFile(birthdayPath, []byte("1710000000\n"), 0600); err != nil {
		t.Fatalf("write birthday timestamp: %v", err)
	}

	cfg, err := loadLNDInitConfig("", "", keyPath, birthdayPath, "", "")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.ExtendedMasterKey != "xprv-example-key" {
		t.Fatalf("unexpected extended master key: %q", cfg.ExtendedMasterKey)
	}
	if cfg.ExtendedMasterKeyBirthdayTimestamp != 1710000000 {
		t.Fatalf("unexpected birthday timestamp: %d", cfg.ExtendedMasterKeyBirthdayTimestamp)
	}
}

func TestHashFileSHA256UsesExactBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xprv.txt")
	contents := []byte("xprv-example-key\n")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatalf("write xprv: %v", err)
	}

	gotHash, gotSize, err := hashFileSHA256(path)
	if err != nil {
		t.Fatalf("hash file: %v", err)
	}
	wantSum := sha256.Sum256(contents)
	wantHash := hex.EncodeToString(wantSum[:])

	if gotHash != wantHash {
		t.Fatalf("unexpected hash: got %q want %q", gotHash, wantHash)
	}
	if gotSize != int64(len(contents)) {
		t.Fatalf("unexpected file size: got %d want %d", gotSize, len(contents))
	}
}

func TestParseAccountHealthInterval(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{raw: "", want: 10 * time.Minute},
		{raw: "15s", want: 15 * time.Second},
		{raw: "0s", want: 10 * time.Minute},
		{raw: "nope", want: 10 * time.Minute},
	}

	for _, tc := range tests {
		if got := parseAccountHealthInterval(tc.raw); got != tc.want {
			t.Fatalf("parseAccountHealthInterval(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}

type fakeRobosatsTokenRepo struct {
	keys []db.APIKey
	err  error
}

func (f fakeRobosatsTokenRepo) GetActiveAPIKeysForProvider(_ context.Context, provider string) ([]db.APIKey, error) {
	if provider != "Robosats" {
		return nil, errors.New("unexpected provider")
	}
	return f.keys, f.err
}

func TestRobosatsAuthTokenFromDBPrefersNewestActiveDBToken(t *testing.T) {
	got, err := robosatsAuthTokenFromDB(context.Background(), fakeRobosatsTokenRepo{
		keys: []db.APIKey{
			{ID: 2, APIKey: " db-token "},
			{ID: 1, APIKey: "older-token"},
		},
	}, "env-token")
	if err != nil {
		t.Fatalf("robosatsAuthTokenFromDB: %v", err)
	}
	if got != "db-token" {
		t.Fatalf("token = %q, want db-token", got)
	}
}

func TestRobosatsAuthTokenFromDBFallsBack(t *testing.T) {
	got, err := robosatsAuthTokenFromDB(context.Background(), fakeRobosatsTokenRepo{}, "env-token")
	if err != nil {
		t.Fatalf("robosatsAuthTokenFromDB: %v", err)
	}
	if got != "env-token" {
		t.Fatalf("token = %q, want env-token", got)
	}

	got, err = robosatsAuthTokenFromDB(context.Background(), fakeRobosatsTokenRepo{
		err: errors.New("db down"),
	}, "env-token")
	if err == nil {
		t.Fatal("err = nil, want db error")
	}
	if got != "env-token" {
		t.Fatalf("token = %q, want env-token after db error", got)
	}
}
