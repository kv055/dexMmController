package lnd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInitConfigHasRestoreSecret(t *testing.T) {
	empty := InitConfig{}
	if empty.HasRestoreSecret() {
		t.Fatal("empty InitConfig should not have restore secret")
	}
	cfg := InitConfig{ExtendedMasterKey: "xprv-example"}
	if !cfg.HasRestoreSecret() {
		t.Fatal("extended master key should count as restore secret")
	}
}

func TestWalletJustInitializedDefaultsFalse(t *testing.T) {
	client := &Client{}
	if client.WalletJustInitialized() {
		t.Fatal("WalletJustInitialized() = true, want false before init")
	}
}

func TestInitConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     InitConfig
		wantErr bool
	}{
		{
			name: "mnemonic only",
			cfg: InitConfig{
				CipherSeedMnemonic: []string{
					"alpha", "beta", "gamma", "delta", "epsilon", "zeta",
					"eta", "theta", "iota", "kappa", "lambda", "mu",
					"nu", "xi", "omicron", "pi", "rho", "sigma",
					"tau", "upsilon", "phi", "chi", "psi", "omega",
				},
			},
		},
		{
			name: "empty config for fresh wallet",
			cfg:  InitConfig{},
		},
		{
			name: "both secret types",
			cfg: InitConfig{
				CipherSeedMnemonic: []string{
					"alpha", "beta", "gamma", "delta", "epsilon", "zeta",
					"eta", "theta", "iota", "kappa", "lambda", "mu",
					"nu", "xi", "omicron", "pi", "rho", "sigma",
					"tau", "upsilon", "phi", "chi", "psi", "omega",
				},
				ExtendedMasterKey: "xprv-example",
			},
			wantErr: true,
		},
		{
			name: "short mnemonic",
			cfg: InitConfig{
				CipherSeedMnemonic: []string{"alpha", "beta"},
			},
			wantErr: true,
		},
		{
			name: "extended master key only",
			cfg: InitConfig{
				ExtendedMasterKey: "xprv-example",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestMultiBackupBytes(t *testing.T) {
	raw := []byte("encrypted-scb-payload")
	snapshot := &ChanBackupSnapshot{
		MultiChanBackup: &MultiChanBackup{
			MultiChanBackup: base64.StdEncoding.EncodeToString(raw),
			ChanPoints: []ChannelPoint{
				{FundingTxidStr: "abc", OutputIndex: 1},
			},
		},
	}

	got, count, err := MultiBackupBytes(snapshot)
	if err != nil {
		t.Fatalf("MultiBackupBytes() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("channel count = %d, want 1", count)
	}
	if string(got) != string(raw) {
		t.Fatalf("backup bytes = %q, want %q", got, raw)
	}
}

func TestMultiBackupBytesEmptySnapshot(t *testing.T) {
	got, count, err := MultiBackupBytes(nil)
	if err != nil {
		t.Fatalf("MultiBackupBytes(nil) error = %v", err)
	}
	if got != nil || count != 0 {
		t.Fatalf("got (%q, %d), want (nil, 0)", got, count)
	}
}

func TestSyncChannelBackupsRestoresOnlyAfterFreshInit(t *testing.T) {
	existing := []byte("stored-scb")
	exported := []byte("exported-scb")
	restoreCalled := false

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/channels/backup/restore":
			restoreCalled = true
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(restoreChanBackupResponse{NumRestored: 1})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/channels/backup":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(ChanBackupSnapshot{
				MultiChanBackup: &MultiChanBackup{
					MultiChanBackup: base64.StdEncoding.EncodeToString(exported),
					ChanPoints:      []ChannelPoint{{FundingTxidStr: "abc", OutputIndex: 0}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{host: server.URL, httpClient: server.Client()}

	if err := client.syncChannelBackups(context.Background(), existing); err != nil {
		t.Fatalf("syncChannelBackups() error = %v", err)
	}
	if restoreCalled {
		t.Fatal("restore called on normal restart")
	}
	got, count := client.LastExportedChannelBackup()
	if count != 1 || string(got) != string(exported) {
		t.Fatalf("export = (%q, %d), want (%q, 1)", got, count, exported)
	}

	restoreCalled = false
	client.walletJustInitialized = true
	if err := client.syncChannelBackups(context.Background(), existing); err != nil {
		t.Fatalf("syncChannelBackups() after init error = %v", err)
	}
	if !restoreCalled {
		t.Fatal("restore not called after fresh wallet init")
	}
}
