// Package escrow implements the local Bitcoin signing HodlHodl's non-custodial release
// requires: decrypt the account seed, derive the per-contract key, and sign the release
// transaction. It is isolated and unit-tested because it moves real BTC.
//
// IMPORTANT (pending live verification): three details cannot be pinned without a real
// HodlHodl contract and are implemented to the most standard interpretation, clearly
// marked: (1) the AES-GCM byte layout inside the `ES1:` seed blob, (2) that derivation is
// a single non-hardened BIP32 step m/<index> from the seed master, and (3) the position of
// our signature within each input's P2WSH multisig witness stack. Validate all three
// against a live release_transaction before mainnet use.
package escrow

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/pbkdf2"
)

const (
	// aes128KeyLen is the PBKDF2 output length for AES-128.
	aes128KeyLen = 16
	// gcmNonceLen is the standard AES-GCM nonce length; assumed prepended to the
	// ciphertext in the ES1 blob.
	gcmNonceLen = 12
)

// DecryptSeed decrypts a HodlHodl `ES1:<ciphertext_hex>:<salt_hex>:pbkdf2:<iters>` seed
// blob with the account payment password. The PBKDF2-HMAC-SHA256 output (16 bytes) is the
// AES-128-GCM key; the ciphertext is assumed to be nonce(12) || ciphertext || tag.
func DecryptSeed(encryptedSeed, paymentPassword string) ([]byte, error) {
	parts := strings.Split(strings.TrimSpace(encryptedSeed), ":")
	if len(parts) != 5 || parts[0] != "ES1" || parts[3] != "pbkdf2" {
		return nil, fmt.Errorf("escrow: unrecognized encrypted seed format (want ES1:<ct>:<salt>:pbkdf2:<iters>)")
	}
	ciphertext, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("escrow: decode ciphertext: %w", err)
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("escrow: decode salt: %w", err)
	}
	iters, err := strconv.Atoi(parts[4])
	if err != nil || iters <= 0 {
		return nil, fmt.Errorf("escrow: invalid pbkdf2 iteration count %q", parts[4])
	}

	key := pbkdf2.Key([]byte(paymentPassword), salt, iters, aes128KeyLen, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("escrow: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("escrow: aes-gcm: %w", err)
	}
	if len(ciphertext) < gcmNonceLen+gcm.Overhead() {
		return nil, errors.New("escrow: ciphertext too short for nonce+tag")
	}
	nonce, ct := ciphertext[:gcmNonceLen], ciphertext[gcmNonceLen:]
	seed, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("escrow: seed decrypt failed (wrong payment password or layout?): %w", err)
	}
	return seed, nil
}

// DeriveKey derives the per-contract escrow private key at BIP32 path m/<index> (a single
// non-hardened step) from the decrypted seed.
func DeriveKey(seed []byte, index uint32) (*secp256k1.PrivateKey, error) {
	if len(seed) == 0 {
		return nil, errors.New("escrow: empty seed")
	}
	if index >= 0x80000000 {
		return nil, fmt.Errorf("escrow: index %d is hardened; only non-hardened derivation is supported", index)
	}

	// BIP32 master from seed.
	h := hmac.New(sha512.New, []byte("Bitcoin seed"))
	_, _ = h.Write(seed)
	I := h.Sum(nil)
	var master secp256k1.ModNScalar
	if master.SetByteSlice(I[:32]) {
		return nil, errors.New("escrow: master key >= curve order")
	}
	if master.IsZero() {
		return nil, errors.New("escrow: master key is zero")
	}
	chainCode := I[32:]

	// Single non-hardened child m/index.
	parentPriv := secp256k1.NewPrivateKey(&master)
	var data []byte
	data = append(data, parentPriv.PubKey().SerializeCompressed()...)
	var idx [4]byte
	binary.BigEndian.PutUint32(idx[:], index)
	data = append(data, idx[:]...)

	ch := hmac.New(sha512.New, chainCode)
	_, _ = ch.Write(data)
	ci := ch.Sum(nil)
	var childScalar secp256k1.ModNScalar
	if childScalar.SetByteSlice(ci[:32]) {
		return nil, errors.New("escrow: derived IL >= curve order")
	}
	childScalar.Add(&master) // child = IL + parent (mod n)
	if childScalar.IsZero() {
		return nil, errors.New("escrow: derived child key is zero")
	}
	return secp256k1.NewPrivateKey(&childScalar), nil
}

// SignRelease signs every input of the unsigned release transaction with key, treating each
// input as a P2WSH multisig over witnessScript with the corresponding satoshi amount from
// inAmounts (input order). It returns the segwit-serialized signed transaction hex.
//
// Witness assembly (see package note): our signature is inserted immediately before the
// trailing witnessScript element, after any signature(s) the HodlHodl server pre-placed.
// This ordering must be confirmed against a live contract — CHECKMULTISIG requires the
// signatures to follow the script's pubkey order.
func SignRelease(unsignedHex string, inAmounts []int64, witnessScript []byte, key *secp256k1.PrivateKey) (string, error) {
	if key == nil {
		return "", errors.New("escrow: nil signing key")
	}
	if len(witnessScript) == 0 {
		return "", errors.New("escrow: empty witness script")
	}
	raw, err := hex.DecodeString(strings.TrimSpace(unsignedHex))
	if err != nil {
		return "", fmt.Errorf("escrow: decode tx hex: %w", err)
	}
	tx, err := parseTx(raw)
	if err != nil {
		return "", fmt.Errorf("escrow: parse tx: %w", err)
	}
	if len(inAmounts) != len(tx.Inputs) {
		return "", fmt.Errorf("escrow: have %d input amounts but tx has %d inputs", len(inAmounts), len(tx.Inputs))
	}

	for i := range tx.Inputs {
		sighash := tx.sighashSegwit(i, witnessScript, inAmounts[i])
		sig := ecdsa.Sign(key, sighash)
		sigWithType := append(sig.Serialize(), byte(sighashAll))
		tx.Inputs[i].Witness = insertSignature(tx.Inputs[i].Witness, sigWithType, witnessScript)
	}
	tx.HasWitness = true
	return hex.EncodeToString(tx.serialize()), nil
}

// insertSignature places sig into a P2WSH multisig witness stack. If the input already has
// a stack (server-presigned), sig is inserted just before the final witnessScript element;
// otherwise a fresh [OP_0 dummy, sig, witnessScript] stack is built (the leading empty is
// the CHECKMULTISIG off-by-one item).
func insertSignature(existing [][]byte, sig, witnessScript []byte) [][]byte {
	if len(existing) == 0 {
		return [][]byte{{}, sig, witnessScript}
	}
	// Drop a trailing witnessScript if present, re-append after our sig.
	last := existing[len(existing)-1]
	if string(last) == string(witnessScript) {
		head := existing[:len(existing)-1]
		out := make([][]byte, 0, len(existing)+1)
		out = append(out, head...)
		out = append(out, sig)
		out = append(out, witnessScript)
		return out
	}
	out := make([][]byte, 0, len(existing)+1)
	out = append(out, existing...)
	out = append(out, sig)
	return out
}
