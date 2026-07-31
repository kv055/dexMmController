package cexaccount

import (
	"fmt"
	"strings"
)

// AccessError is the safe, consistent error returned when a CEX API request
// cannot be completed. Cause is retained for logs but is never shown to the
// operator, since exchange responses often expose endpoint-specific jargon.
type AccessError struct {
	Provider  string
	Operation string
	Cause     error
}

// NewAccessError wraps an exchange request failure in a UI-safe error.
func NewAccessError(provider, operation string, cause error) *AccessError {
	return &AccessError{
		Provider:  strings.TrimSpace(provider),
		Operation: strings.TrimSpace(operation),
		Cause:     cause,
	}
}

// Error returns the operator-facing message.
func (e *AccessError) Error() string {
	provider := e.Provider
	if provider == "" {
		provider = "CEX"
	}
	operation := e.Operation
	if operation == "" {
		operation = "account data"
	}
	return fmt.Sprintf(
		"Unable to load %s from %s. Check that an active API key is configured, its IP allowlist includes this server, and it has the required permission.",
		operation, provider,
	)
}

// Unwrap preserves the exchange response for server-side logging and error inspection.
func (e *AccessError) Unwrap() error {
	return e.Cause
}
