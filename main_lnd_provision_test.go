package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"dexTakerController/internal/api"
)

func TestGenerateWalletPasswordFormat(t *testing.T) {
	password, err := generateWalletPassword()
	if err != nil {
		t.Fatalf("generateWalletPassword() error = %v", err)
	}
	// 32 random bytes -> 43 unpadded URL-safe base64 characters.
	if len(password) != 43 {
		t.Fatalf("generateWalletPassword() length = %d, want 43", len(password))
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(password) {
		t.Fatalf("generateWalletPassword() = %q, want URL-safe base64 only", password)
	}
}

func TestGenerateWalletPasswordUnique(t *testing.T) {
	first, err := generateWalletPassword()
	if err != nil {
		t.Fatalf("generateWalletPassword() error = %v", err)
	}
	second, err := generateWalletPassword()
	if err != nil {
		t.Fatalf("generateWalletPassword() error = %v", err)
	}
	if first == second {
		t.Fatal("generateWalletPassword() returned the same password twice")
	}
}

func TestIsPermanentLNDInitError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		permanent bool
	}{
		{"nil", nil, false},
		{"config incomplete direct", api.ErrLNDConfigIncomplete, true},
		{"config incomplete wrapped", fmt.Errorf("LND wallet client was not created: %w", api.ErrLNDConfigIncomplete), true},
		{"restore secret wrapped", fmt.Errorf("%w: mnemonic must contain 24 words", api.ErrLNDRestoreSecretInvalid), true},
		{"transient connection", errors.New("dial tcp 10.0.0.5:10009: connect: connection refused"), false},
		{"transient database", errors.New("load LND wallet credentials: database unavailable"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanentLNDInitError(tc.err); got != tc.permanent {
				t.Fatalf("isPermanentLNDInitError(%v) = %t, want %t", tc.err, got, tc.permanent)
			}
		})
	}
}

func TestDescribePermanentLNDInitError(t *testing.T) {
	if got := describePermanentLNDInitError(fmt.Errorf("%w (stored primary address bc1qxyz reused)", api.ErrLNDConfigIncomplete)); !strings.Contains(got, "incomplete") {
		t.Fatalf("describePermanentLNDInitError() = %q, want it to keep the sentinel text", got)
	}
	if got := describePermanentLNDInitError(errors.New("   ")); got == "" {
		t.Fatal("describePermanentLNDInitError() = empty, want fallback text")
	}
}
