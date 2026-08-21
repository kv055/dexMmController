package lnd

import "testing"

func TestValidatedCEXWithdrawalDestination(t *testing.T) {
	address, err := validatedCEXWithdrawalDestination("  bc1qwhitelisted  ")
	if err != nil || address != "bc1qwhitelisted" {
		t.Fatalf("address=%q err=%v", address, err)
	}
	if _, err := validatedCEXWithdrawalDestination(" \t "); err == nil {
		t.Fatal("expected missing destination to be rejected")
	}
}
