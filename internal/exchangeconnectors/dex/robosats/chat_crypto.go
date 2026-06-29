package robosats

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// pgpMessageBlockType is the armor header for an encrypted RoboSats chat message.
const pgpMessageBlockType = "PGP MESSAGE"

// readArmoredEntity parses a single armored PGP entity (a public or private key block).
func readArmoredEntity(armored string) (*openpgp.Entity, error) {
	el, err := openpgp.ReadArmoredKeyRing(strings.NewReader(strings.TrimSpace(armored)))
	if err != nil {
		return nil, fmt.Errorf("robosats: read PGP key: %w", err)
	}
	if len(el) == 0 {
		return nil, errors.New("robosats: armored PGP key contained no entity")
	}
	return el[0], nil
}

// unlockedEntity parses the robot's armored private key and decrypts it (and its subkeys)
// with the raw robot token — the passphrase RoboSats locks the key with (see Identity). The
// returned entity can decrypt incoming chat messages and sign outgoing ones.
func unlockedEntity(encPrivKeyArmored, token string) (*openpgp.Entity, error) {
	entity, err := readArmoredEntity(encPrivKeyArmored)
	if err != nil {
		return nil, err
	}
	if err := entity.DecryptPrivateKeys([]byte(token)); err != nil {
		return nil, fmt.Errorf("robosats: unlock PGP private key with token: %w", err)
	}
	return entity, nil
}

// decryptChatMessage decrypts one armored RoboSats chat message with the robot's unlocked
// private key. The peer key, when supplied, lets ReadMessage verify the sender's signature;
// it is optional so a message can still be read when the peer key is not yet known.
func decryptChatMessage(armoredMsg string, self, peer *openpgp.Entity) (string, error) {
	if self == nil {
		return "", errors.New("robosats: own unlocked key is required to decrypt")
	}
	block, err := armor.Decode(strings.NewReader(strings.TrimSpace(armoredMsg)))
	if err != nil {
		return "", fmt.Errorf("robosats: de-armor chat message: %w", err)
	}
	keyring := openpgp.EntityList{self}
	if peer != nil {
		keyring = append(keyring, peer)
	}
	// Keys are pre-decrypted, so the prompt is never expected to fire.
	prompt := func([]openpgp.Key, bool) ([]byte, error) {
		return nil, errors.New("robosats: unexpected passphrase prompt")
	}
	md, err := openpgp.ReadMessage(block.Body, keyring, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("robosats: decrypt chat message: %w", err)
	}
	buf, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return "", fmt.Errorf("robosats: read decrypted chat message: %w", err)
	}
	return string(buf), nil
}

// encryptChatMessage encrypts and signs plaintext to the peer (and to ourselves, so the
// message round-trips and we can re-read our own history) with the robot's unlocked private
// key, returning an armored PGP message.
func encryptChatMessage(plaintext string, self, peer *openpgp.Entity) (string, error) {
	if self == nil || peer == nil {
		return "", errors.New("robosats: both own and peer keys are required to encrypt")
	}
	var out bytes.Buffer
	aw, err := armor.Encode(&out, pgpMessageBlockType, nil)
	if err != nil {
		return "", fmt.Errorf("robosats: armor chat message: %w", err)
	}
	w, err := openpgp.Encrypt(aw, []*openpgp.Entity{peer, self}, self, nil, nil)
	if err != nil {
		return "", fmt.Errorf("robosats: encrypt chat message: %w", err)
	}
	if _, err := io.WriteString(w, plaintext); err != nil {
		return "", fmt.Errorf("robosats: write chat plaintext: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("robosats: finalize encrypted chat message: %w", err)
	}
	if err := aw.Close(); err != nil {
		return "", fmt.Errorf("robosats: finalize armored chat message: %w", err)
	}
	return out.String(), nil
}

// unescapeArmor restores real newlines in an armored PGP block that a RoboSats coordinator
// returned with newlines escaped as backslashes (the same escaping the web client and our
// Identity.AuthHeader use). Blocks that already contain newlines are returned unchanged.
func unescapeArmor(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "\n") {
		return s
	}
	return strings.ReplaceAll(s, "\\", "\n")
}

// escapeArmor escapes newlines as backslashes for sending an armored block back to the
// coordinator, mirroring Identity.AuthHeader.
func escapeArmor(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\\")
}
