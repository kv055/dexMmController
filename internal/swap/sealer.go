package swap

import "commons/credentialcrypto"

// CredentialSealer reuses the project's established encrypted-secret boundary.
type CredentialSealer struct{}

func (CredentialSealer) Seal(value string) ([]byte, error) {
	encrypted, err := credentialcrypto.Encrypt(value)
	if err != nil {
		return nil, err
	}
	return []byte(encrypted), nil
}
