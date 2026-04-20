package lnd

import "testing"

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
			name:    "missing secret",
			cfg:     InitConfig{},
			wantErr: true,
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
