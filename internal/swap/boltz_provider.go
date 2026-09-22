package swap

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	boltz "github.com/BoltzExchange/boltz-client/v2/pkg/boltz"
	"github.com/btcsuite/btcd/btcec/v2"
)

type Invoice struct{ PaymentRequest, PaymentHash string }
type InvoiceSource interface {
	CreateSwapInvoice(context.Context, int64) (Invoice, error)
}

// CooperativeRefunder is implemented with the official library's persisted
// swap-tree/key recovery code; it receives no API-serializable rescue data.
type CooperativeRefunder interface {
	Refund(context.Context, string) error
}

// OfficialBoltzProvider creates and monitors swaps through the official Boltz
// Client Go package. It never implements Taproot or MuSig itself.
type OfficialBoltzProvider struct {
	Quotes   BoltzQuoteSource
	Invoices InvoiceSource
	Refunder CooperativeRefunder
}

func (p OfficialBoltzProvider) Quote(ctx context.Context, sat int64) (Quote, error) {
	return p.Quotes.Quote(ctx, sat)
}
func (p OfficialBoltzProvider) Create(ctx context.Context, q Quote) (Prepared, error) {
	if p.Quotes.API == nil || p.Invoices == nil {
		return Prepared{}, fmt.Errorf("official Boltz provider is not configured")
	}
	invoice, err := p.Invoices.CreateSwapInvoice(ctx, q.LightningOutputSat)
	if err != nil {
		return Prepared{}, err
	}
	key, err := btcec.NewPrivateKey()
	if err != nil {
		return Prepared{}, err
	}
	pairs, err := p.Quotes.API.GetSubmarinePairs()
	if err != nil {
		return Prepared{}, err
	}
	pair := pairs[boltz.CurrencyBtc][boltz.CurrencyBtc]
	response, err := p.Quotes.API.CreateSwap(boltz.CreateSwapRequest{From: boltz.CurrencyBtc, To: boltz.CurrencyBtc, PairHash: pair.Hash, RefundPublicKey: key.PubKey().SerializeCompressed(), Invoice: invoice.PaymentRequest})
	if err != nil {
		return Prepared{}, err
	}
	if int64(response.ExpectedAmount) != q.LockupAmountSat {
		return Prepared{}, fmt.Errorf("boltz expected lockup changed from quote: quoted %d, actual %d", q.LockupAmountSat, response.ExpectedAmount)
	}
	rescue, err := json.Marshal(struct {
		PrivateKey     string                `json:"private_key"`
		SwapTree       *boltz.SerializedTree `json:"swap_tree"`
		Timeout        uint32                `json:"timeout_block_height"`
		ClaimPublicKey string                `json:"claim_public_key"`
		LockupAddress  string                `json:"lockup_address"`
	}{hex.EncodeToString(key.Serialize()), response.SwapTree, response.TimeoutBlockHeight, hex.EncodeToString(response.ClaimPublicKey), response.Address})
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{ID: response.Id, LockupAddress: response.Address, InvoiceHash: invoice.PaymentHash, RescueMaterial: string(rescue)}, nil
}
func (p OfficialBoltzProvider) Status(ctx context.Context, id string) (RemoteStatus, error) {
	if err := ctx.Err(); err != nil {
		return RemoteStatus{}, err
	}
	response, err := p.Quotes.API.SwapStatus(id)
	if err != nil {
		return RemoteStatus{}, err
	}
	event := boltz.ParseEvent(response.Status)
	return RemoteStatus{State: response.Status, Settled: event.IsCompletedStatus(), Failed: event.IsFailedStatus(), Refundable: response.Status != boltz.TransactionRefunded.String()}, nil
}
func (p OfficialBoltzProvider) Refund(ctx context.Context, id string) error {
	if p.Refunder == nil {
		return fmt.Errorf("official Boltz cooperative refunder is not configured")
	}
	return p.Refunder.Refund(ctx, id)
}

var _ Provider = OfficialBoltzProvider{}
