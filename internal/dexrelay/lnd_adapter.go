package dexrelay

import (
	"context"

	lnd "dexMmController/internal/wallethub"
)

// LNDAdapter wraps wallethub.Client for dexoffer.LNDRelay.
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

// PayInvoice pays a BOLT11 invoice via LND and returns the payment preimage as receipt id.
func (a LNDAdapter) PayInvoice(ctx context.Context, paymentRequest string, feeLimitSat int64) (string, error) {
	if a.Client == nil {
		return "", nil
	}
	resp, err := a.Client.PayInvoice(ctx, paymentRequest, feeLimitSat)
	if err != nil {
		return "", err
	}
	if resp != nil {
		return resp.PaymentPreimage, nil
	}
	return "", nil
}
