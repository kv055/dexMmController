package main

import (
	"context"
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

	cfg, err := loadLNDInitConfig(mnemonicPath, passphrasePath, "250")
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

func TestParseDurationOr(t *testing.T) {
	fallback := 2 * time.Hour
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{raw: "45m", want: 45 * time.Minute},
		{raw: "0", want: 0},
		{raw: "-1s", want: -time.Second},
		{raw: "invalid", want: fallback},
		{raw: "", want: fallback},
	} {
		if got := parseDurationOr(tc.raw, fallback); got != tc.want {
			t.Fatalf("parseDurationOr(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}

// withEnv sets env vars for the duration of a subtest and returns a cleanup.
// t.Setenv would be ideal, but resolveDatabaseURL reads several vars, so a
// helper keeps the table-driven cases below readable.
func setDBEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestResolveDatabaseURL(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "POSTGRES_HOST set builds URL from individual vars",
			env: map[string]string{
				"POSTGRES_HOST":     "postgres",
				"POSTGRES_PORT":     "5432",
				"POSTGRES_DB":       "dexarb",
				"POSTGRES_USER":     "dex_mm_controller",
				"POSTGRES_PASSWORD": "s3cret",
				"DATABASE_URL":      "postgres://ignored:ignored@localhost:5432/ignored?sslmode=disable",
			},
			want: "postgres://dex_mm_controller:s3cret@postgres:5432/dexarb?sslmode=disable",
		},
		{
			name: "POSTGRES_HOST wins over DATABASE_URL",
			env: map[string]string{
				"POSTGRES_HOST":     "db.internal",
				"POSTGRES_USER":     "u",
				"POSTGRES_PASSWORD": "p",
				"POSTGRES_DB":       "mydb",
				"DATABASE_URL":      "postgres://dex_mm_controller:oldpw@localhost:5432/dexarb?sslmode=disable",
			},
			want: "postgres://u:p@db.internal:5432/mydb?sslmode=disable",
		},
		{
			name: "POSTGRES_HOST set applies defaults for missing vars",
			env: map[string]string{
				"POSTGRES_HOST":     "postgres",
				"POSTGRES_PASSWORD": "p",
			},
			want: "postgres://postgres:p@postgres:5432/dexarb?sslmode=disable",
		},
		{
			name: "POSTGRES_HOST empty falls back to DATABASE_URL",
			env: map[string]string{
				"POSTGRES_HOST": "",
				"DATABASE_URL":  "postgres://dex_mm_controller:pw@localhost:5432/dexarb?sslmode=disable",
			},
			want: "postgres://dex_mm_controller:pw@localhost:5432/dexarb?sslmode=disable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Clear all DB-related env vars first so each case starts clean,
			// then apply the case's vars. t.Setenv restores prior values on
			// cleanup, including the "unset" state we establish here.
			for _, k := range []string{"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD", "DATABASE_URL"} {
				t.Setenv(k, "")
			}
			setDBEnv(t, tc.env)
			if got := resolveDatabaseURL(); got != tc.want {
				t.Fatalf("resolveDatabaseURL() = %q, want %q", got, tc.want)
			}
		})
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
	})
	if err != nil {
		t.Fatalf("robosatsAuthTokenFromDB: %v", err)
	}
	if got != "db-token" {
		t.Fatalf("token = %q, want db-token", got)
	}
}

func TestRobosatsAuthTokenFromDBReturnsEmptyWithoutDBToken(t *testing.T) {
	got, err := robosatsAuthTokenFromDB(context.Background(), fakeRobosatsTokenRepo{})
	if err != nil {
		t.Fatalf("robosatsAuthTokenFromDB: %v", err)
	}
	if got != "" {
		t.Fatalf("token = %q, want empty", got)
	}

	got, err = robosatsAuthTokenFromDB(context.Background(), fakeRobosatsTokenRepo{
		err: errors.New("db down"),
	})
	if err == nil {
		t.Fatal("err = nil, want db error")
	}
	if got != "" {
		t.Fatalf("token = %q, want empty after db error", got)
	}
}
