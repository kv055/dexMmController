package lnd

import (
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
