package cexaccount

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/cex/binance"
	"dexMmController/internal/exchanges/cex/kraken"
	lnd "dexMmController/internal/wallethub"
)

// WithdrawalStatus is a normalized CEX withdrawal status snapshot.
type WithdrawalStatus struct {
	Provider      string `json:"provider"`
	WithdrawalID  string `json:"withdrawal_id"`
	CexStatus     string `json:"cex_status"`
	CexStatusCode int    `json:"cex_status_code"`
	TxHash        string `json:"tx_hash,omitempty"`
	Address       string `json:"address,omitempty"`
	AmountBTC     string `json:"amount_btc,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}

// GetWithdrawalStatus looks up a withdrawal by exchange id (and optional client id).
func (s *Service) GetWithdrawalStatus(ctx context.Context, rawProvider, withdrawalID, clientRequestID string) (*WithdrawalStatus, error) {
	withdrawalID = strings.TrimSpace(withdrawalID)
	if withdrawalID == "" {
		return nil, fmt.Errorf("withdrawal_id is required")
	}

	provider, key, err := s.firstActiveKey(ctx, rawProvider)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(provider) {
	case "binance":
		return s.binanceWithdrawalStatus(ctx, key, withdrawalID, clientRequestID)
	case "kraken":
		return s.krakenWithdrawalStatus(ctx, key, withdrawalID)
	default:
		return nil, fmt.Errorf("withdrawal status not supported for %s", provider)
	}
}

func (s *Service) binanceWithdrawalStatus(ctx context.Context, key db.APIKey, withdrawalID, clientRequestID string) (*WithdrawalStatus, error) {
	client := binance.NewClient(s.cfg.BinanceBaseURL, key.APIKey, key.APISecret)
	bridge := lnd.NewBinanceBridge(client)

	if strings.TrimSpace(clientRequestID) != "" {
		byClient, err := bridge.GetWithdrawHistory(ctx, lnd.ExchangeWithdrawHistoryParams{
			Coin:            "BTC",
			ClientRequestID: strings.TrimSpace(clientRequestID),
		})
		if err != nil {
			return nil, fmt.Errorf("binance: withdraw history: %w", err)
		}
		if rec, ok := matchBinanceWithdrawRecord(byClient, withdrawalID); ok {
			return binanceWithdrawalStatusFromRecord(rec), nil
		}
	}

	records, err := bridge.GetWithdrawHistory(ctx, lnd.ExchangeWithdrawHistoryParams{Coin: "BTC"})
	if err != nil {
		return nil, fmt.Errorf("binance: withdraw history: %w", err)
	}
	rec, ok := matchBinanceWithdrawRecord(records, withdrawalID)
	if !ok {
		return nil, fmt.Errorf("binance: withdrawal %q not found in recent history", withdrawalID)
	}
	return binanceWithdrawalStatusFromRecord(rec), nil
}

func matchBinanceWithdrawRecord(records []lnd.ExchangeWithdrawRecord, withdrawalID string) (lnd.ExchangeWithdrawRecord, bool) {
	for _, rec := range records {
		if rec.ID == withdrawalID {
			return rec, true
		}
	}
	return lnd.ExchangeWithdrawRecord{}, false
}

func binanceWithdrawalStatusFromRecord(rec lnd.ExchangeWithdrawRecord) *WithdrawalStatus {
	label := binanceWithdrawStatusLabel(rec.Status)
	updatedAt := rec.ApplyTime
	if updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return &WithdrawalStatus{
		Provider:      "binance",
		WithdrawalID:  rec.ID,
		CexStatus:     label,
		CexStatusCode: rec.Status,
		TxHash:        strings.TrimSpace(rec.TxID),
		Address:       rec.Address,
		AmountBTC:     rec.Amount,
		UpdatedAt:     updatedAt,
	}
}

func binanceWithdrawStatusLabel(status int) string {
	switch status {
	case 0:
		return "email_sent"
	case 1:
		return "cancelled"
	case 2:
		return "awaiting_approval"
	case 3:
		return "rejected"
	case 4:
		return "processing"
	case 5:
		return "failed"
	case 6:
		return "completed"
	default:
		return "unknown"
	}
}

func (s *Service) krakenWithdrawalStatus(ctx context.Context, key db.APIKey, withdrawalID string) (*WithdrawalStatus, error) {
	client := kraken.NewClient(s.cfg.KrakenBaseURL, key.APIKey, key.APISecret)
	end := time.Now()
	start := end.Add(-7 * 24 * time.Hour)
	records, err := client.GetWithdrawStatus(ctx, kraken.WithdrawStatusRequest{
		Asset:     "XBT",
		Method:    "Bitcoin",
		StartTime: &start,
		EndTime:   &end,
	})
	if err != nil {
		return nil, fmt.Errorf("kraken: withdraw status: %w", err)
	}
	for _, rec := range records {
		if rec.RefID != withdrawalID {
			continue
		}
		updatedAt := time.Unix(rec.Time, 0).UTC().Format(time.RFC3339)
		return &WithdrawalStatus{
			Provider:      "kraken",
			WithdrawalID:  rec.RefID,
			CexStatus:     krakenWithdrawStatusLabel(rec.Status),
			CexStatusCode: krakenWithdrawStatusCode(rec.Status),
			TxHash:        strings.TrimSpace(rec.TxID),
			Address:       rec.Info,
			AmountBTC:     rec.Amount,
			UpdatedAt:     updatedAt,
		}, nil
	}
	return nil, fmt.Errorf("kraken: withdrawal %q not found in recent history", withdrawalID)
}

func krakenWithdrawStatusLabel(status string) string {
	switch strings.TrimSpace(status) {
	case "Initial":
		return "initial"
	case "Pending":
		return "pending"
	case "Settled":
		return "settled"
	case "Success":
		return "completed"
	case "On hold":
		return "on_hold"
	case "Failure":
		return "failed"
	default:
		if status == "" {
			return "unknown"
		}
		return strings.ToLower(strings.ReplaceAll(status, " ", "_"))
	}
}

func krakenWithdrawStatusCode(status string) int {
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
