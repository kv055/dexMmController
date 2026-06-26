package lnd

import (
	"context"

	"dexMmController/internal/exchangeconnectors/cex/binance"
)

// BinanceBridge represents a binance bridge.
type BinanceBridge struct {
	client *binance.Client
}

// NewBinanceBridge creates a binance bridge.
func NewBinanceBridge(client *binance.Client) *BinanceBridge {
	return &BinanceBridge{client: client}
}

// Name returns the exchange or bridge name.
func (b *BinanceBridge) Name() string {
	return "binance"
}

// GetDepositAddress retrieves a deposit address.
func (b *BinanceBridge) GetDepositAddress(ctx context.Context, coin, network string) (string, error) {
	resp, err := b.client.GetDepositAddress(ctx, coin, network)
	if err != nil {
		return "", err
	}
	return resp.Address, nil
}

// WithdrawToAddress withdraws funds using the configured bridge.
func (b *BinanceBridge) WithdrawToAddress(ctx context.Context, req ExchangeWithdrawRequest) (*ExchangeWithdrawResponse, error) {
	resp, err := b.client.ApplyWithdraw(ctx, binance.WithdrawRequest{
		Coin:            req.Coin,
		Network:         req.Network,
		Address:         req.Address,
		Amount:          req.Amount,
		AddressTag:      req.AddressTag,
		WithdrawOrderID: req.ClientRequestID,
	})
	if err != nil {
		return nil, err
	}
	return &ExchangeWithdrawResponse{ID: resp.ID}, nil
}

// GetDepositHistory retrieves a deposit history.
func (b *BinanceBridge) GetDepositHistory(ctx context.Context, params ExchangeDepositHistoryParams) ([]ExchangeDepositRecord, error) {
	records, err := b.client.GetDepositHistory(ctx, binance.DepositHistoryParams{
		Coin:      params.Coin,
		StartTime: params.StartTime,
		EndTime:   params.EndTime,
	})
	if err != nil {
		return nil, err
	}

	out := make([]ExchangeDepositRecord, 0, len(records))
	for _, record := range records {
		out = append(out, ExchangeDepositRecord{
			ID:         record.ID,
			Coin:       record.Coin,
			Network:    record.Network,
			Address:    record.Address,
			Amount:     record.Amount,
			Status:     record.Status,
			TxID:       record.TxID,
			InsertTime: record.InsertTime,
		})
	}
	return out, nil
}

// GetWithdrawHistory retrieves a withdraw history.
func (b *BinanceBridge) GetWithdrawHistory(ctx context.Context, params ExchangeWithdrawHistoryParams) ([]ExchangeWithdrawRecord, error) {
	records, err := b.client.GetWithdrawHistory(ctx, binance.WithdrawHistoryParams{
		Coin:            params.Coin,
		WithdrawOrderID: params.ClientRequestID,
		StartTime:       params.StartTime,
		EndTime:         params.EndTime,
	})
	if err != nil {
		return nil, err
	}

	out := make([]ExchangeWithdrawRecord, 0, len(records))
	for _, record := range records {
		out = append(out, ExchangeWithdrawRecord{
			ID:              record.ID,
			Coin:            record.Coin,
			Network:         record.Network,
			Address:         record.Address,
			Amount:          record.Amount,
			Status:          record.Status,
			TxID:            record.TxID,
			Fee:             record.TransactionFee,
			ApplyTime:       record.ApplyTime,
			ClientRequestID: record.WithdrawOrderID,
		})
	}
	return out, nil
}
