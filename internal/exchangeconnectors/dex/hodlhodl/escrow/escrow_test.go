package escrow

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// unsignedBIP143 is the unsigned transaction from the BIP143 P2WPKH worked example.
const unsignedBIP143 = "0100000002fff7f7881a8099afa6940d42d1e7f6362bec38171ea3edf433541db4e4ad969f" +
	"0000000000eeffffffef51e1b804cc89d182d279655c3aa89e815b1b309fe287d9b2b55d57b90ec68a" +
	"0100000000ffffffff02202cb206000000001976a9148280b37df378db99f66f85c95a783a76ac7a6d59" +
	"88ac9093510d000000001976a9143bde42dbee7e4dbe6a21b2d50ce2f0167faa815988ac11000000"

// TestParseSerializeRoundTrip confirms the legacy-serialized BIP143 tx round-trips byte for
// byte through parse → serialize (no witness present).
func TestParseSerializeRoundTrip(t *testing.T) {
	raw, err := hex.DecodeString(unsignedBIP143)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	tx, err := parseTx(raw)
	if err != nil {
		t.Fatalf("parseTx: %v", err)
	}
	if got := len(tx.Inputs); got != 2 {
		t.Fatalf("inputs = %d, want 2", got)
	}
	if got := len(tx.Outputs); got != 2 {
		t.Fatalf("outputs = %d, want 2", got)
	}
	if !bytes.Equal(tx.serialize(), raw) {
		t.Fatalf("round-trip mismatch:\n got %x\nwant %x", tx.serialize(), raw)
	}
}

// TestSighashSegwitBIP143Vector checks the BIP143 segwit sighash against the published
// digest for input index 1 (P2WPKH, value 6 BTC). The scriptCode is the implied P2PKH
// script for the input's pubkey hash.
func TestSighashSegwitBIP143Vector(t *testing.T) {
	raw, _ := hex.DecodeString(unsignedBIP143)
	tx, err := parseTx(raw)
	if err != nil {
		t.Fatalf("parseTx: %v", err)
	}
	scriptCode, _ := hex.DecodeString("76a9141d0f172a0ecb48aee1be1f2687d2963ae33f71a188ac")
	const amount int64 = 600000000

	got := hex.EncodeToString(tx.sighashSegwit(1, scriptCode, amount))
	const want = "c37af31116d1b27caf68aae9e3ac82f1477929014d5b917657d0eb49478cb670"
	if got != want {
		t.Fatalf("sighash = %s, want %s", got, want)
	}
}

// TestDeriveKeyDeterministicAndSigns confirms derivation is deterministic and the derived
// key produces a verifiable signature over a sighash.
func TestDeriveKeyDeterministicAndSigns(t *testing.T) {
	seed := []byte("hodlhodl-escrow-test-seed-000001")
	k1, err := DeriveKey(seed, 7)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	k2, err := DeriveKey(seed, 7)
	if err != nil {
		t.Fatalf("DeriveKey (2): %v", err)
	}
	if !bytes.Equal(k1.Serialize(), k2.Serialize()) {
		t.Fatal("derivation is not deterministic")
	}
	if other, _ := DeriveKey(seed, 8); bytes.Equal(k1.Serialize(), other.Serialize()) {
		t.Fatal("different indices derived the same key")
	}

	raw, _ := hex.DecodeString(unsignedBIP143)
	tx, _ := parseTx(raw)
	scriptCode, _ := hex.DecodeString("76a9141d0f172a0ecb48aee1be1f2687d2963ae33f71a188ac")
	digest := tx.sighashSegwit(0, scriptCode, 1000)
	sig := ecdsa.Sign(k1, digest)
	if !sig.Verify(digest, k1.PubKey()) {
		t.Fatal("signature did not verify against derived pubkey")
	}
}

// TestSignReleaseProducesWitness signs a release tx and confirms each input gains a witness
// stack ending in the witness script, and the result re-parses as segwit.
func TestSignReleaseProducesWitness(t *testing.T) {
	key, err := DeriveKey([]byte("seed-for-signrelease-test-00001"), 1)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	witnessScript, _ := hex.DecodeString("5221" + // OP_2 push33
		hex.EncodeToString(key.PubKey().SerializeCompressed()) + "21" +
		hex.EncodeToString(key.PubKey().SerializeCompressed()) + "52ae") // OP_2 OP_CHECKMULTISIG
	signedHex, err := SignRelease(unsignedBIP143, []int64{500, 600000000}, witnessScript, key)
	if err != nil {
		t.Fatalf("SignRelease: %v", err)
	}
	raw, err := hex.DecodeString(signedHex)
	if err != nil {
		t.Fatalf("decode signed: %v", err)
	}
	tx, err := parseTx(raw)
	if err != nil {
		t.Fatalf("re-parse signed: %v", err)
	}
	if !tx.HasWitness {
		t.Fatal("signed tx is not segwit-serialized")
	}
	for i, in := range tx.Inputs {
		if len(in.Witness) == 0 {
			t.Fatalf("input %d has no witness", i)
		}
		if last := in.Witness[len(in.Witness)-1]; !bytes.Equal(last, witnessScript) {
			t.Fatalf("input %d witness does not end in witnessScript", i)
		}
	}
}
