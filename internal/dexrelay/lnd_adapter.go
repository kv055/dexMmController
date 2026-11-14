package dexrelay

import (
	"context"

	lnd "dexTakerController/internal/wallethub"
)

// LNDAdapter wraps wallethub.Client for dexaccount.LNDRelay.
type LNDAdapter struct {
	Client *lnd.Client
}

// SendOnChain relays BTC on-chain via LND.
func (a LNDAdapter) SendOnChain(ctx context.Context, toAddress string, amountSat, satPerVbyte int64) (string, error) {
	if a.Client == nil {
		return "", nil
	}
	return a.Client.SendOnChain(ctx, toAddress, amountSat, satPerVbyte)
}

// PayInvoice pays a BOLT11 invoice without returning the secret preimage. Callers
// persist the decoded payment hash as the non-secret receipt identifier.
func (a LNDAdapter) PayInvoice(ctx context.Context, paymentRequest string, feeLimitSat int64) (string, error) {
	if a.Client == nil {
		return "", nil
	}
	_, err := a.Client.PayInvoice(ctx, paymentRequest, feeLimitSat)
	if err != nil {
		return "", err
	}
	return "", nil
}

func (a LNDAdapter) DecodeInvoice(ctx context.Context, paymentRequest string) (*lnd.DecodedInvoice, error) {
	return a.Client.DecodeInvoice(ctx, paymentRequest)
}

// ConfirmedBalanceForAddress returns the confirmed satoshi balance at a hub address.
func (a LNDAdapter) ConfirmedBalanceForAddress(ctx context.Context, address string) (int64, error) {
	if a.Client == nil {
		return 0, nil
	}
	return a.Client.ConfirmedBalanceForAddress(ctx, address)
}
