package robosats

import (
	"strings"
	"testing"
)

func TestNewIdentity(t *testing.T) {
	identity, err := NewIdentity("robot-token")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	if identity.TokenSHA256 != AuthHeaderToken("robot-token") {
		t.Fatalf("TokenSHA256 = %q, want %q", identity.TokenSHA256, AuthHeaderToken("robot-token"))
	}
	// The nostr key is deterministic: x-only secp256k1 pubkey of
	// sha256(sha512(token)), matching the RoboSats web client derivation.
	if len(identity.NostrPubKey) != 64 {
		t.Fatalf("NostrPubKey length = %d, want 64", len(identity.NostrPubKey))
	}
	again, err := NewIdentity("robot-token")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	if identity.NostrPubKey != again.NostrPubKey {
		t.Fatalf("NostrPubKey not deterministic: %q vs %q", identity.NostrPubKey, again.NostrPubKey)
	}
	if !strings.Contains(identity.PublicKeyArmored, "BEGIN PGP PUBLIC KEY BLOCK") {
		t.Fatalf("PublicKeyArmored not armored: %q", identity.PublicKeyArmored[:60])
	}
	if !strings.Contains(identity.EncPrivKeyArmored, "BEGIN PGP PRIVATE KEY BLOCK") {
		t.Fatalf("EncPrivKeyArmored not armored: %q", identity.EncPrivKeyArmored[:60])
	}
}

func TestIdentityAuthHeader(t *testing.T) {
	identity := &Identity{
		TokenSHA256:       "hash",
		PublicKeyArmored:  "line1\nline2\n",
		EncPrivKeyArmored: "priv1\npriv2",
		NostrPubKey:       strings.Repeat("a", 64),
	}
	want := "Token hash | Public line1\\line2 | Private priv1\\priv2 | Nostr " + strings.Repeat("a", 64)
	if got := identity.AuthHeader(); got != want {
		t.Fatalf("AuthHeader = %q, want %q", got, want)
	}
}
