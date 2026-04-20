package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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

	cfg, err := loadLNDInitConfig(mnemonicPath, passphrasePath, "", "250", "")
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
	if err := os.WriteFile(keyPath, []byte("xprv-example-key\n"), 0600); err != nil {
		t.Fatalf("write xprv: %v", err)
	}

	cfg, err := loadLNDInitConfig("", "", keyPath, "", "1710000000")
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
