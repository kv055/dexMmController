package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"dexTakerController/internal/swap"
	lnd "dexTakerController/internal/wallethub"
)

// lndBoltzRuntime exposes only the wallet operations required by the embedded
// Boltz coordinator. Rescue material remains inside the encrypted swap store.
type lndBoltzRuntime struct{ client *lnd.Client }

func (a lndBoltzRuntime) CreateSwapInvoice(ctx context.Context, amountSat int64) (swap.Invoice, error) {
	if a.client == nil || amountSat <= 0 {
		return swap.Invoice{}, fmt.Errorf("invalid LND swap invoice request")
	}
	response, err := a.client.CreateInvoice(ctx, amountSat*1000, "Boltz submarine swap", 3600)
	if err != nil {
		return swap.Invoice{}, err
	}
	if response == nil || strings.TrimSpace(response.PaymentRequest) == "" || strings.TrimSpace(response.RHash) == "" {
		return swap.Invoice{}, fmt.Errorf("LND returned an incomplete swap invoice")
	}
	return swap.Invoice{PaymentRequest: response.PaymentRequest, PaymentHash: response.RHash}, nil
}

func (a lndBoltzRuntime) NewRefundAddress(ctx context.Context) (string, error) {
	if a.client == nil {
		return "", fmt.Errorf("LND refund wallet is not configured")
	}
	return a.client.NewAddress(ctx)
}

func (a lndBoltzRuntime) ConfirmedOnChainBTC(ctx context.Context) (float64, error) {
	if a.client == nil {
		return 0, fmt.Errorf("LND wallet is not configured")
	}
	balance, err := a.client.OnChainBalance(ctx)
	if err != nil {
		return 0, err
	}
	if balance == nil || strings.TrimSpace(balance.ConfirmedBalance) == "" {
		return 0, nil
	}
	sats, err := strconv.ParseInt(strings.TrimSpace(balance.ConfirmedBalance), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse LND confirmed balance: %w", err)
	}
	return float64(sats) / 1e8, nil
}
