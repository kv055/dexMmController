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

type fakeLNDHealthClient struct {
	err error
}

func (f fakeLNDHealthClient) HealthCheck(context.Context) error {
	return f.err
}

type fakeLNDAccountHealthRepo struct {
	wallet          *db.WalletAddress
	walletErr       error
	updatedProvider string
	updatedHealthy  bool
}

func (f *fakeLNDAccountHealthRepo) GetPrimaryWalletAddress() (*db.WalletAddress, error) {
	return f.wallet, f.walletErr
}

func (f *fakeLNDAccountHealthRepo) UpdateAccountHealth(_ context.Context, provider string, healthy bool) error {
	f.updatedProvider = provider
	f.updatedHealthy = healthy
	return nil
}

func TestCheckLNDAccountHealth(t *testing.T) {
	tests := []struct {
		name    string
		repo    *fakeLNDAccountHealthRepo
		client  lndHealthClient
		wantErr bool
	}{
		{
			name:   "healthy with lnd client and primary address",
			repo:   &fakeLNDAccountHealthRepo{wallet: &db.WalletAddress{Address: "bc1qexample"}},
			client: fakeLNDHealthClient{},
		},
		{
			name:    "unhealthy without lnd client",
			repo:    &fakeLNDAccountHealthRepo{wallet: &db.WalletAddress{Address: "bc1qexample"}},
			wantErr: true,
		},
		{
			name:    "unhealthy when lnd health fails",
			repo:    &fakeLNDAccountHealthRepo{wallet: &db.WalletAddress{Address: "bc1qexample"}},
			client:  fakeLNDHealthClient{err: errors.New("lnd down")},
			wantErr: true,
		},
		{
			name:    "unhealthy without primary address",
			repo:    &fakeLNDAccountHealthRepo{walletErr: db.ErrWalletAddressNotFound},
			client:  fakeLNDHealthClient{},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLNDAccountHealth(context.Background(), tc.repo, tc.client)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

func TestRunLNDAccountHealthCheckPersistsCanonicalProvider(t *testing.T) {
	repo := &fakeLNDAccountHealthRepo{wallet: &db.WalletAddress{Address: "bc1qexample"}}
	runLNDAccountHealthCheck(context.Background(), repo, fakeLNDHealthClient{})

	if repo.updatedProvider != db.LNDAccountProvider {
		t.Fatalf("provider = %q, want %q", repo.updatedProvider, db.LNDAccountProvider)
	}
	if !repo.updatedHealthy {
		t.Fatal("healthy = false, want true")
	}

	runLNDAccountHealthCheck(context.Background(), repo, nil)
	if repo.updatedHealthy {
		t.Fatal("healthy = true, want false")
	}
}

func TestParseAccountHealthInterval(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{raw: "", want: time.Minute},
		{raw: "15s", want: 15 * time.Second},
		{raw: "0s", want: time.Minute},
		{raw: "nope", want: time.Minute},
	}

	for _, tc := range tests {
		if got := parseAccountHealthInterval(tc.raw); got != tc.want {
			t.Fatalf("parseAccountHealthInterval(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}
