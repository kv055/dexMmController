package lnd

import (
	"context"
	"fmt"
	"strconv"

	"dexMmController/internal/exchangeconnectors/cex/kraken"
)

// KrakenBridge represents a kraken bridge.
type KrakenBridge struct {
	client                        *kraken.Client
	depositMethodByCoin           map[string]string
	withdrawKeyByCoin             map[string]string
	lightningDepositMethodByCoin  map[string]string
	lightningWithdrawMethodByCoin map[string]string
}

// NewKrakenBridge creates a kraken bridge.
func NewKrakenBridge(
	client *kraken.Client,
	depositMethodByCoin map[string]string,
	withdrawKeyByCoin map[string]string,
	lightningDepositMethodByCoin map[string]string,
	lightningWithdrawMethodByCoin map[string]string,
) *KrakenBridge {
	return &KrakenBridge{
		client:                        client,
		depositMethodByCoin:           depositMethodByCoin,
		withdrawKeyByCoin:             withdrawKeyByCoin,
		lightningDepositMethodByCoin:  lightningDepositMethodByCoin,
		lightningWithdrawMethodByCoin: lightningWithdrawMethodByCoin,
	}
}

// Name returns the exchange or bridge name.
func (k *KrakenBridge) Name() string {
	return "kraken"
}

// GetDepositAddress retrieves a deposit address.
func (k *KrakenBridge) GetDepositAddress(ctx context.Context, coin, network string) (string, error) {
	method := network
	if method == "" {
		method = k.depositMethodByCoin[coin]
	}
	if method == "" {
		return "", fmt.Errorf("kraken: no deposit method configured for coin %s", coin)
	}

	addresses, err := k.client.GetDepositAddresses(ctx, coin, method, true)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("kraken: no deposit address returned for %s via %s", coin, method)
	}
	return addresses[0].Address, nil
}

// WithdrawToAddress withdraws funds using the configured bridge.
func (k *KrakenBridge) WithdrawToAddress(ctx context.Context, req ExchangeWithdrawRequest) (*ExchangeWithdrawResponse, error) {
	withdrawKey := req.AddressTag
	if withdrawKey == "" {
		withdrawKey = k.withdrawKeyByCoin[req.Coin]
	}
	if withdrawKey == "" {
		return nil, fmt.Errorf("kraken: no withdraw key configured for coin %s", req.Coin)
	}

	resp, err := k.client.Withdraw(ctx, kraken.WithdrawRequest{
		Asset:  req.Coin,
		Key:    withdrawKey,
		Amount: req.Amount,
	})
	if err != nil {
		return nil, err
	}
	return &ExchangeWithdrawResponse{ID: resp.RefID}, nil
}

// GetLightningDepositInvoice retrieves a lightning deposit invoice.
func (k *KrakenBridge) GetLightningDepositInvoice(ctx context.Context, req ExchangeLightningDepositRequest) (string, error) {
	method := req.Network
	if method == "" {
		method = k.lightningDepositMethodByCoin[req.Coin]
	}
	if method == "" {
		return "", fmt.Errorf("kraken: no lightning deposit method configured for coin %s", req.Coin)
	}
	if req.AmountSat <= 0 {
		return "", fmt.Errorf("kraken: lightning deposit amount must be positive")
	}

	addresses, err := k.client.GetDepositAddressesWithOptions(ctx, kraken.DepositAddressRequest{
		Asset:     req.Coin,
		Method:    method,
		CreateNew: true,
		Amount:    satsToBTCString(req.AmountSat),
	})
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 || addresses[0].Address == "" {
		return "", fmt.Errorf("kraken: no lightning invoice returned for %s via %s", req.Coin, method)
	}
	return addresses[0].Address, nil
}

// WithdrawByInvoice withdraws funds using the configured bridge.
func (k *KrakenBridge) WithdrawByInvoice(ctx context.Context, req ExchangeLightningWithdrawRequest) (*ExchangeLightningWithdrawResponse, error) {
	method := req.Network
	if method == "" {
		method = k.lightningWithdrawMethodByCoin[req.Coin]
	}
	if method == "" {
		return nil, fmt.Errorf("kraken: no lightning withdraw method configured for coin %s", req.Coin)
	}
	if req.Invoice == "" {
		return nil, fmt.Errorf("kraken: lightning invoice is required")
	}

	resp, err := k.client.Withdraw(ctx, kraken.WithdrawRequest{
		Asset:   req.Coin,
		Address: req.Invoice,
		Method:  method,
		Amount:  req.Amount,
	})
	if err != nil {
		return nil, err
	}
	return &ExchangeLightningWithdrawResponse{ID: resp.RefID}, nil
}

// GetDepositHistory retrieves a deposit history.
func (k *KrakenBridge) GetDepositHistory(ctx context.Context, params ExchangeDepositHistoryParams) ([]ExchangeDepositRecord, error) {
	method := k.depositMethodByCoin[params.Coin]
	records, err := k.client.GetDepositStatus(ctx, kraken.DepositStatusRequest{
		Asset:     params.Coin,
		Method:    method,
		StartTime: params.StartTime,
		EndTime:   params.EndTime,
	})
	if err != nil {
		return nil, err
	}

	out := make([]ExchangeDepositRecord, 0, len(records))
	for _, record := range records {
		out = append(out, ExchangeDepositRecord{
			ID:         record.RefID,
			Coin:       record.Asset,
			Network:    record.Method,
			Address:    record.Info,
			Amount:     record.Amount,
			Status:     mapKrakenStatus(record.Status),
			TxID:       record.TxID,
			InsertTime: record.Time * 1000,
		})
	}
	return out, nil
}

// GetWithdrawHistory retrieves a withdraw history.
func (k *KrakenBridge) GetWithdrawHistory(ctx context.Context, params ExchangeWithdrawHistoryParams) ([]ExchangeWithdrawRecord, error) {
	method := k.depositMethodByCoin[params.Coin]
	records, err := k.client.GetWithdrawStatus(ctx, kraken.WithdrawStatusRequest{
		Asset:     params.Coin,
		Method:    method,
		StartTime: params.StartTime,
		EndTime:   params.EndTime,
	})
	if err != nil {
		return nil, err
	}

	out := make([]ExchangeWithdrawRecord, 0, len(records))
	for _, record := range records {
		out = append(out, ExchangeWithdrawRecord{
			ID:              record.RefID,
			Coin:            record.Asset,
			Network:         record.Method,
			Address:         record.Info,
			Amount:          record.Amount,
			Status:          mapKrakenStatus(record.Status),
			TxID:            record.TxID,
			ApplyTime:       strconv.FormatInt(record.Time, 10),
			ClientRequestID: record.RefID,
		})
	}
	return out, nil
}

func mapKrakenStatus(status string) int {
	switch status {
	case "Initial":
		return 10
	case "Pending":
		return 20
	case "Settled":
		return 30
	case "Success":
		return 40
	case "On hold":
		return 50
	case "Failure":
		return 60
	default:
		return 0
	}
}

func satsToBTCString(amountSat int64) string {
	return strconv.FormatFloat(float64(amountSat)/100000000, 'f', 8, 64)
}
