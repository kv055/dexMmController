package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"dexTakerController/internal/api"
)

// generateWalletPassword returns a fresh 256-bit secret encoded as URL-safe
// base64 (43 characters, no padding). It is used to auto-provision the LND
// wallet password when the operator has not configured one.
func generateWalletPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate LND wallet password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// isPermanentLNDInitError reports whether a bootstrap failure can never resolve
// on its own. The retry supervisor stops instead of spinning on these until the
// operator fixes the configuration and restarts the controller.
func isPermanentLNDInitError(err error) bool {
	return errors.Is(err, api.ErrLNDConfigIncomplete) ||
		errors.Is(err, api.ErrLNDRestoreSecretInvalid)
}

func describePermanentLNDInitError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "unknown configuration error"
	}
	return msg
}
