package lnd

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// ExchangeBridge defines the behavior required by this package.
type ExchangeBridge interface {
	Name() string
	GetDepositAddress(ctx context.Context, coin, network string) (string, error)
	WithdrawToAddress(ctx context.Context, req ExchangeWithdrawRequest) (*ExchangeWithdrawResponse, error)
	GetDepositHistory(ctx context.Context, params ExchangeDepositHistoryParams) ([]ExchangeDepositRecord, error)
	GetWithdrawHistory(ctx context.Context, params ExchangeWithdrawHistoryParams) ([]ExchangeWithdrawRecord, error)
}

// LightningExchangeBridge defines the behavior required by this package.
type LightningExchangeBridge interface {
	ExchangeBridge
	GetLightningDepositInvoice(ctx context.Context, req ExchangeLightningDepositRequest) (string, error)
	WithdrawByInvoice(ctx context.Context, req ExchangeLightningWithdrawRequest) (*ExchangeLightningWithdrawResponse, error)
}

// ExchangeWithdrawRequest contains the parameters for an on-chain exchange withdrawal.
type ExchangeWithdrawRequest struct {
	// Coin names the asset or network for the exchange withdraw request.
	Coin string
	// Network names the asset or network for the exchange withdraw request.
	Network string
	// Address is the wallet or peer address used by the exchange withdraw request.
	Address string
	// Amount is the amount value for the exchange withdraw request.
	Amount string
	// AddressTag is the wallet or peer address used by the exchange withdraw request.
	AddressTag string
	// ClientRequestID is the client request id value for the exchange withdraw request.
	ClientRequestID string
}

// ExchangeWithdrawResponse contains the exchange withdrawal identifier.
type ExchangeWithdrawResponse struct {
	// ID identifies the exchange withdraw response.
	ID string
}

// ExchangeLightningDepositRequest contains the parameters for creating a Lightning deposit invoice.
type ExchangeLightningDepositRequest struct {
	// Coin names the asset or network for the exchange lightning deposit request.
	Coin string
	// Network names the asset or network for the exchange lightning deposit request.
	Network string
	// AmountSat is the amount in satoshis.
	AmountSat int64
	// Memo carries a human-readable message for the exchange lightning deposit request.
	Memo string
}

// ExchangeLightningWithdrawRequest contains the parameters for paying a Lightning invoice from an exchange.
type ExchangeLightningWithdrawRequest struct {
	// Coin names the asset or network for the exchange lightning withdraw request.
	Coin string
	// Network names the asset or network for the exchange lightning withdraw request.
	Network string
	// Invoice is the invoice value for the exchange lightning withdraw request.
	Invoice string
	// Amount is the amount value for the exchange lightning withdraw request.
	Amount string
	// ClientRequestID is the client request id value for the exchange lightning withdraw request.
	ClientRequestID string
}

// ExchangeLightningWithdrawResponse contains the exchange Lightning withdrawal identifier.
type ExchangeLightningWithdrawResponse struct {
	// ID identifies the exchange lightning withdraw response.
	ID string
}

// ExchangeDepositHistoryParams filters exchange deposit history queries.
type ExchangeDepositHistoryParams struct {
	// Coin names the asset or network for the exchange deposit history params.
	Coin string
	// StartTime records the relevant time for the exchange deposit history params.
	StartTime *time.Time
	// EndTime records the relevant time for the exchange deposit history params.
	EndTime *time.Time
}

// ExchangeWithdrawHistoryParams filters exchange withdrawal history queries.
type ExchangeWithdrawHistoryParams struct {
	// Coin names the asset or network for the exchange withdraw history params.
	Coin string
	// ClientRequestID is the client request id value for the exchange withdraw history params.
	ClientRequestID string
	// StartTime records the relevant time for the exchange withdraw history params.
	StartTime *time.Time
	// EndTime records the relevant time for the exchange withdraw history params.
	EndTime *time.Time
}

// ExchangeDepositRecord is a normalized deposit-history entry from an exchange.
type ExchangeDepositRecord struct {
	// ID identifies the exchange deposit record.
	ID string
	// Coin names the asset or network for the exchange deposit record.
	Coin string
	// Network names the asset or network for the exchange deposit record.
	Network string
	// Address is the wallet or peer address used by the exchange deposit record.
	Address string
	// Amount is the amount value for the exchange deposit record.
	Amount string
	// Status is the current status of the exchange deposit record.
	Status int
	// TxID is the tx id value for the exchange deposit record.
	TxID string
	// InsertTime records the relevant time for the exchange deposit record.
	InsertTime int64
}

// ExchangeWithdrawRecord is a normalized withdrawal-history entry from an exchange.
type ExchangeWithdrawRecord struct {
	// ID identifies the exchange withdraw record.
	ID string
	// Coin names the asset or network for the exchange withdraw record.
	Coin string
	// Network names the asset or network for the exchange withdraw record.
	Network string
	// Address is the wallet or peer address used by the exchange withdraw record.
	Address string
	// Amount is the amount value for the exchange withdraw record.
	Amount string
	// Status is the current status of the exchange withdraw record.
	Status int
	// TxID is the tx id value for the exchange withdraw record.
	TxID string
	// ApplyTime records the relevant time for the exchange withdraw record.
	ApplyTime string
	// ClientRequestID is the client request id value for the exchange withdraw record.
	ClientRequestID string
}

// WalletExchangeBridge represents a wallet exchange bridge.
type WalletExchangeBridge struct {
	lnd      *Client
	exchange ExchangeBridge
}

// WithdrawFromExchangeRequest represents a withdraw from exchange request.
type WithdrawFromExchangeRequest struct {
	// Coin names the asset or network for the withdraw from exchange request.
	Coin string
	// Network names the asset or network for the withdraw from exchange request.
	Network string
	// Amount is the amount value for the withdraw from exchange request.
	Amount string
	// LNDDestination is the lnddestination value for the withdraw from exchange request.
	LNDDestination string
	// ClientRequestID is the client request id value for the withdraw from exchange request.
	ClientRequestID string
	// AddressTag carries exchange-specific destination metadata (Kraken withdrawal key).
	AddressTag string
	// GenerateNewLNDAdress is the generate new lndadress value for the withdraw from exchange request.
	GenerateNewLNDAdress bool
}

// WithdrawFromExchangeResult represents a withdraw from exchange result.
type WithdrawFromExchangeResult struct {
	// Exchange is the exchange value for the withdraw from exchange result.
	Exchange string
	// WithdrawalID preserves additional source data for the withdraw from exchange result.
	WithdrawalID string
	// WalletAddress is the wallet or peer address used by the withdraw from exchange result.
	WalletAddress string
	// ClientRequestID is the client request id value for the withdraw from exchange result.
	ClientRequestID string
	// Invoice is the invoice value for the withdraw from exchange result.
	Invoice string
}

// DepositToExchangeRequest represents a deposit to exchange request.
type DepositToExchangeRequest struct {
	// Coin names the asset or network for the deposit to exchange request.
	Coin string
	// Network names the asset or network for the deposit to exchange request.
	Network string
	// AmountSat is the amount in satoshis.
	AmountSat int64
	// SatPerVByte is the sat per vbyte value for the deposit to exchange request.
	SatPerVByte int64
}

// DepositToExchangeResult represents a deposit to exchange result.
type DepositToExchangeResult struct {
	// Exchange is the exchange value for the deposit to exchange result.
	Exchange string
	// DepositAddress is the wallet or peer address used by the deposit to exchange result.
	DepositAddress string
	// OnChainTxID is the on chain tx id value for the deposit to exchange result.
	OnChainTxID string
	// Invoice is the invoice value for the deposit to exchange result.
	Invoice string
	// PaymentPreimage is the payment preimage value for the deposit to exchange result.
	PaymentPreimage string
}

// NewWalletExchangeBridge creates a wallet exchange bridge.
func NewWalletExchangeBridge(lndClient *Client, exchange ExchangeBridge) *WalletExchangeBridge {
	return &WalletExchangeBridge{
		lnd:      lndClient,
		exchange: exchange,
	}
}

// WithdrawFromExchangeToLND withdraws funds using the configured bridge.
func (b *WalletExchangeBridge) WithdrawFromExchangeToLND(ctx context.Context, req WithdrawFromExchangeRequest) (*WithdrawFromExchangeResult, error) {
	if b == nil || b.lnd == nil || b.exchange == nil {
		return nil, fmt.Errorf("wallethub: exchange bridge is not fully configured")
	}

	address := req.LNDDestination
	if address == "" || req.GenerateNewLNDAdress {
		var err error
		address, err = b.lnd.NewAddress(ctx)
		if err != nil {
			return nil, fmt.Errorf("wallethub: generate LND address: %w", err)
		}
	}

	resp, err := b.exchange.WithdrawToAddress(ctx, ExchangeWithdrawRequest{
		Coin:            req.Coin,
		Network:         req.Network,
		Address:         address,
		Amount:          req.Amount,
		AddressTag:      req.AddressTag,
		ClientRequestID: req.ClientRequestID,
	})
	if err != nil {
		return nil, err
	}

	return &WithdrawFromExchangeResult{
		Exchange:        b.exchange.Name(),
		WithdrawalID:    resp.ID,
		WalletAddress:   address,
		ClientRequestID: req.ClientRequestID,
	}, nil
}

// DepositFromLNDToExchange deposits funds using the configured bridge.
func (b *WalletExchangeBridge) DepositFromLNDToExchange(ctx context.Context, req DepositToExchangeRequest) (*DepositToExchangeResult, error) {
	if b == nil || b.lnd == nil || b.exchange == nil {
		return nil, fmt.Errorf("wallethub: exchange bridge is not fully configured")
	}

	depositAddress, err := b.exchange.GetDepositAddress(ctx, req.Coin, req.Network)
	if err != nil {
		return nil, fmt.Errorf("wallethub: fetch exchange deposit address: %w", err)
	}

	txID, err := b.lnd.SendOnChain(ctx, depositAddress, req.AmountSat, req.SatPerVByte)
	if err != nil {
		return nil, fmt.Errorf("wallethub: send on-chain to exchange: %w", err)
	}

	return &DepositToExchangeResult{
		Exchange:       b.exchange.Name(),
		DepositAddress: depositAddress,
		OnChainTxID:    txID,
	}, nil
}

// WithdrawFromExchangeToLNDLightning withdraws funds using the configured bridge.
func (b *WalletExchangeBridge) WithdrawFromExchangeToLNDLightning(
	ctx context.Context,
	req WithdrawFromExchangeRequest,
) (*WithdrawFromExchangeResult, error) {
	if b == nil || b.lnd == nil || b.exchange == nil {
		return nil, fmt.Errorf("wallethub: exchange bridge is not fully configured")
	}

	lightningExchange, ok := b.exchange.(LightningExchangeBridge)
	if !ok {
		return nil, fmt.Errorf("wallethub: exchange %s does not support lightning withdrawals", b.exchange.Name())
	}

	amountSat, err := parseAmountToSats(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("wallethub: invalid lightning withdraw amount: %w", err)
	}

	invoice, err := b.lnd.CreateInvoice(ctx, amountSat*1000, req.ClientRequestID, 3600)
	if err != nil {
		return nil, fmt.Errorf("wallethub: create LND invoice: %w", err)
	}

	resp, err := lightningExchange.WithdrawByInvoice(ctx, ExchangeLightningWithdrawRequest{
		Coin:            req.Coin,
		Network:         req.Network,
		Invoice:         invoice.PaymentRequest,
		Amount:          req.Amount,
		ClientRequestID: req.ClientRequestID,
	})
	if err != nil {
		return nil, err
	}

	return &WithdrawFromExchangeResult{
		Exchange:        b.exchange.Name(),
		WithdrawalID:    resp.ID,
		ClientRequestID: req.ClientRequestID,
		Invoice:         invoice.PaymentRequest,
	}, nil
}

// DepositFromLNDToExchangeLightning deposits funds using the configured bridge.
func (b *WalletExchangeBridge) DepositFromLNDToExchangeLightning(
	ctx context.Context,
	req DepositToExchangeRequest,
) (*DepositToExchangeResult, error) {
	if b == nil || b.lnd == nil || b.exchange == nil {
		return nil, fmt.Errorf("wallethub: exchange bridge is not fully configured")
	}

	lightningExchange, ok := b.exchange.(LightningExchangeBridge)
	if !ok {
		return nil, fmt.Errorf("wallethub: exchange %s does not support lightning deposits", b.exchange.Name())
	}

	invoice, err := lightningExchange.GetLightningDepositInvoice(ctx, ExchangeLightningDepositRequest{
		Coin:      req.Coin,
		Network:   req.Network,
		AmountSat: req.AmountSat,
	})
	if err != nil {
		return nil, fmt.Errorf("wallethub: fetch exchange lightning invoice: %w", err)
	}

	payment, err := b.lnd.PayInvoice(ctx, invoice, 0)
	if err != nil {
		return nil, fmt.Errorf("wallethub: pay exchange lightning invoice: %w", err)
	}

	return &DepositToExchangeResult{
		Exchange:        b.exchange.Name(),
		Invoice:         invoice,
		PaymentPreimage: payment.PaymentPreimage,
	}, nil
}

func parseAmountToSats(amount string) (int64, error) {
	if amount == "" {
		return 0, fmt.Errorf("amount is required")
	}

	value, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}
	return int64(value * 100000000), nil
}
