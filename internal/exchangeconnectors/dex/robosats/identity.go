package robosats

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// Identity is the full robot identity RoboSats coordinators require on the
// first authenticated request, mirroring what the RoboSats web client
// derives from a robot token. Coordinators create the robot from these
// values and return the stored keys on subsequent requests, so a robot
// provisioned here can later be recovered in the RoboSats app with the same
// token (the PGP private key is encrypted with the token as passphrase).
type Identity struct {
	// TokenSHA256 is base91(sha256(token)), the Authorization credential.
	TokenSHA256 string
	// PublicKeyArmored is the armored PGP public key.
	PublicKeyArmored string
	// EncPrivKeyArmored is the armored PGP private key, locked with the
	// raw token as passphrase.
	EncPrivKeyArmored string
	// NostrPubKey is the x-only secp256k1 public key (64 hex chars) of the
	// nostr secret key sha256(sha512(token)).
	NostrPubKey string
}

// NewIdentity derives a robot identity from a raw robot token. The PGP
// keypair is freshly generated (as in the web client); coordinators keep the
// keys presented when the robot is first created and ignore them afterwards.
func NewIdentity(token string) (*Identity, error) {
	nostrPubKey, err := nostrPubKeyFromToken(token)
	if err != nil {
		return nil, err
	}
	publicKey, encPrivKey, err := generatePGPKeyPair(token)
	if err != nil {
		return nil, err
	}
	return &Identity{
		TokenSHA256:       AuthHeaderToken(token),
		PublicKeyArmored:  publicKey,
		EncPrivKeyArmored: encPrivKey,
		NostrPubKey:       nostrPubKey,
	}, nil
}

// AuthHeader renders the full Authorization header value used to create (or
// fetch) the robot. Armored keys are sent with newlines escaped as
// backslashes, which the coordinator unescapes.
func (id *Identity) AuthHeader() string {
	escape := func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\\") }
	return fmt.Sprintf(
		"Token %s | Public %s | Private %s | Nostr %s",
		id.TokenSHA256, escape(id.PublicKeyArmored), escape(id.EncPrivKeyArmored), id.NostrPubKey,
	)
}

// nostrPubKeyFromToken derives the deterministic nostr public key the web
// client computes: x-only secp256k1 pubkey of sha256(sha512(token)).
func nostrPubKeyFromToken(token string) (string, error) {
	outer := sha512.Sum512([]byte(token))
	seed := sha256.Sum256(outer[:])
	privKey := secp256k1.PrivKeyFromBytes(seed[:])
	if privKey.Key.IsZero() {
		return "", fmt.Errorf("robosats: token derives an invalid nostr key")
	}
	compressed := privKey.PubKey().SerializeCompressed()
	return hex.EncodeToString(compressed[1:33]), nil
}

// generatePGPKeyPair creates the curve25519 PGP keypair for a robot: user ID
// "RoboSats ID <sha256hex(sha256hex(token))>", private key encrypted with the
// token, both armored. Creation time is backdated one day, matching the web
// client, to avoid rejection when the local clock runs ahead.
func generatePGPKeyPair(token string) (publicKey, encPrivKey string, err error) {
	innerHex := hex.EncodeToString(func() []byte { h := sha256.Sum256([]byte(token)); return h[:] }())
	hashID := sha256.Sum256([]byte(innerHex))
	name := "RoboSats ID " + hex.EncodeToString(hashID[:])

	created := time.Now().Add(-24 * time.Hour)
	cfg := &packet.Config{
		Algorithm: packet.PubKeyAlgoEdDSA,
		Curve:     packet.Curve25519,
		Time:      func() time.Time { return created },
	}
	entity, err := openpgp.NewEntity(name, "", "", cfg)
	if err != nil {
		return "", "", fmt.Errorf("robosats: generate PGP key: %w", err)
	}

	if err := entity.PrivateKey.Encrypt([]byte(token)); err != nil {
		return "", "", fmt.Errorf("robosats: encrypt PGP private key: %w", err)
	}
	for _, subkey := range entity.Subkeys {
		if subkey.PrivateKey != nil {
			if err := subkey.PrivateKey.Encrypt([]byte(token)); err != nil {
				return "", "", fmt.Errorf("robosats: encrypt PGP subkey: %w", err)
			}
		}
	}

	publicKey, err = armorEntity(openpgp.PublicKeyType, func(w *bytes.Buffer) error {
		return entity.Serialize(w)
	})
	if err != nil {
		return "", "", err
	}
	encPrivKey, err = armorEntity(openpgp.PrivateKeyType, func(w *bytes.Buffer) error {
		// The self-signatures from NewEntity are reused: re-signing is
		// impossible once the private key material is encrypted.
		return entity.SerializePrivateWithoutSigning(w, nil)
	})
	if err != nil {
		return "", "", err
	}
	return publicKey, encPrivKey, nil
}

// IdentityFromStoredKeys rebuilds a robot identity from the coordinator's
// registered public_key and encrypted_private_key instead of generating a new pair.
func IdentityFromStoredKeys(token, publicKey, encPrivKey string) (*Identity, error) {
	publicKey = unescapeArmor(strings.TrimSpace(publicKey))
	encPrivKey = unescapeArmor(strings.TrimSpace(encPrivKey))
	if publicKey == "" || encPrivKey == "" {
		return nil, fmt.Errorf("robosats: registered robot keys are required")
	}
	if _, err := unlockedEntity(encPrivKey, token); err != nil {
		return nil, err
	}
	nostrPubKey, err := nostrPubKeyFromToken(token)
	if err != nil {
		return nil, err
	}
	return &Identity{
		TokenSHA256:       AuthHeaderToken(token),
		PublicKeyArmored:  publicKey,
		EncPrivKeyArmored: encPrivKey,
		NostrPubKey:       nostrPubKey,
	}, nil
}

func armorEntity(blockType string, serialize func(w *bytes.Buffer) error) (string, error) {
	var payload bytes.Buffer
	if err := serialize(&payload); err != nil {
		return "", fmt.Errorf("robosats: serialize PGP key: %w", err)
	}
	var out bytes.Buffer
	w, err := armor.Encode(&out, blockType, nil)
	if err != nil {
		return "", fmt.Errorf("robosats: armor PGP key: %w", err)
	}
	if _, err := w.Write(payload.Bytes()); err != nil {
		return "", fmt.Errorf("robosats: armor PGP key: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("robosats: armor PGP key: %w", err)
	}
	return out.String(), nil
}
