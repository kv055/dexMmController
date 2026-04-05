package lnd

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

type ExchangeBridge interface {
	Name() string
	GetDepositAddress(ctx context.Context, coin, network string) (string, error)
	WithdrawToAddress(ctx context.Context, req ExchangeWithdrawRequest) (*ExchangeWithdrawResponse, error)
	GetDepositHistory(ctx context.Context, params ExchangeDepositHistoryParams) ([]ExchangeDepositRecord, error)
	GetWithdrawHistory(ctx context.Context, params ExchangeWithdrawHistoryParams) ([]ExchangeWithdrawRecord, error)
}

type LightningExchangeBridge interface {
	ExchangeBridge
	GetLightningDepositInvoice(ctx context.Context, req ExchangeLightningDepositRequest) (string, error)
	WithdrawByInvoice(ctx context.Context, req ExchangeLightningWithdrawRequest) (*ExchangeLightningWithdrawResponse, error)
}

type ExchangeWithdrawRequest struct {
	Coin            string
	Network         string
	Address         string
	Amount          string
	AddressTag      string
	ClientRequestID string
}

type ExchangeWithdrawResponse struct {
	ID string
}

type ExchangeLightningDepositRequest struct {
	Coin      string
	Network   string
	AmountSat int64
	Memo      string
}

type ExchangeLightningWithdrawRequest struct {
	Coin            string
	Network         string
	Invoice         string
	Amount          string
	ClientRequestID string
}

type ExchangeLightningWithdrawResponse struct {
	ID string
}

type ExchangeDepositHistoryParams struct {
	Coin      string
	StartTime *time.Time
	EndTime   *time.Time
}

type ExchangeWithdrawHistoryParams struct {
	Coin            string
	ClientRequestID string
	StartTime       *time.Time
	EndTime         *time.Time
}

type ExchangeDepositRecord struct {
	ID         string
	Coin       string
	Network    string
	Address    string
	Amount     string
	Status     int
	TxID       string
	InsertTime int64
}

type ExchangeWithdrawRecord struct {
	ID              string
	Coin            string
	Network         string
	Address         string
	Amount          string
	Status          int
	TxID            string
	ApplyTime       string
	ClientRequestID string
}

type WalletExchangeBridge struct {
	lnd      *Client
	exchange ExchangeBridge
}

type WithdrawFromExchangeRequest struct {
	Coin                 string
	Network              string
	Amount               string
	LNDDestination       string
	ClientRequestID      string
	GenerateNewLNDAdress bool
}

type WithdrawFromExchangeResult struct {
	Exchange        string
	WithdrawalID    string
	WalletAddress   string
	ClientRequestID string
	Invoice         string
}

type DepositToExchangeRequest struct {
	Coin        string
	Network     string
	AmountSat   int64
	SatPerVByte int64
}

type DepositToExchangeResult struct {
	Exchange        string
	DepositAddress  string
	OnChainTxID     string
	Invoice         string
	PaymentPreimage string
}

func NewWalletExchangeBridge(lndClient *Client, exchange ExchangeBridge) *WalletExchangeBridge {
	return &WalletExchangeBridge{
		lnd:      lndClient,
		exchange: exchange,
	}
}

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
