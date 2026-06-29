package robosats

import (
	"strings"
	"testing"
)

// TestChatCryptoRoundTrip proves a message encrypted+signed by one robot and decrypted by the
// other survives intact, reusing the real Identity key derivation — independent of the live API.
func TestChatCryptoRoundTrip(t *testing.T) {
	const aliceToken = "alicealicealicealicealice00"
	const bobToken = "bobbobbobbobbobbobbobbob0011"

	aliceID, err := NewIdentity(aliceToken)
	if err != nil {
		t.Fatalf("alice identity: %v", err)
	}
	bobID, err := NewIdentity(bobToken)
	if err != nil {
		t.Fatalf("bob identity: %v", err)
	}

	alice, err := unlockedEntity(aliceID.EncPrivKeyArmored, aliceToken)
	if err != nil {
		t.Fatalf("unlock alice: %v", err)
	}
	bob, err := unlockedEntity(bobID.EncPrivKeyArmored, bobToken)
	if err != nil {
		t.Fatalf("unlock bob: %v", err)
	}
	alicePub, err := readArmoredEntity(aliceID.PublicKeyArmored)
	if err != nil {
		t.Fatalf("read alice pub: %v", err)
	}
	bobPub, err := readArmoredEntity(bobID.PublicKeyArmored)
	if err != nil {
		t.Fatalf("read bob pub: %v", err)
	}

	const plaintext = "Pay EUR to IBAN DE89370400440532013000, holder Alice"

	armored, err := encryptChatMessage(plaintext, alice, bobPub)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.Contains(armored, "BEGIN PGP MESSAGE") {
		t.Fatalf("expected an armored PGP message, got: %q", armored)
	}

	got, err := decryptChatMessage(armored, bob, alicePub)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("round-trip mismatch:\n got:  %q\n want: %q", got, plaintext)
	}
}

func TestChatCryptoUnlockWrongToken(t *testing.T) {
	id, err := NewIdentity("the-real-token-000000000000")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if _, err := unlockedEntity(id.EncPrivKeyArmored, "wrong-token-111111111111111"); err == nil {
		t.Fatal("expected unlock to fail with the wrong token")
	}
}

func TestArmorEscapeRoundTrip(t *testing.T) {
	armored := "-----BEGIN PGP MESSAGE-----\n\nABCDEF\n=ZZZZ\n-----END PGP MESSAGE-----"
	escaped := escapeArmor(armored)
	if strings.Contains(escaped, "\n") {
		t.Fatalf("escapeArmor left newlines: %q", escaped)
	}
	if got := unescapeArmor(escaped); got != strings.TrimSpace(armored) {
		t.Fatalf("unescape did not restore the block:\n got:  %q\n want: %q", got, armored)
	}
	// Blocks that already have real newlines are returned unchanged.
	if got := unescapeArmor(armored); got != strings.TrimSpace(armored) {
		t.Fatalf("unescape mangled a normal block: %q", got)
	}
}
