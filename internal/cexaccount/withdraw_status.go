package cexaccount

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/cex/binance"
	"dexMmController/internal/exchangeconnectors/cex/kraken"
	lnd "dexMmController/internal/wallethub"
)

// WithdrawalStatus is a normalized CEX withdrawal status snapshot.
type WithdrawalStatus struct {
	Provider      string  `json:"provider"`
	WithdrawalID  string  `json:"withdrawal_id"`
	CexStatus     string  `json:"cex_status"`
	CexStatusCode int     `json:"cex_status_code"`
	TxHash        string  `json:"tx_hash,omitempty"`
	Address       string  `json:"address,omitempty"`
	AmountBTC     string  `json:"amount_btc,omitempty"`
	FeeBTC        float64 `json:"fee_btc,omitempty"` // network/withdrawal fee in BTC, 0 when not reported
	UpdatedAt     string  `json:"updated_at"`
}

// parseDecimal parses a decimal string to float64, returning 0 on empty/invalid input.
func parseDecimal(raw string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return v
}

// GetWithdrawalStatus looks up a withdrawal by exchange id (and optional client id).
func (s *Service) GetWithdrawalStatus(ctx context.Context, rawProvider, withdrawalID, clientRequestID string) (*WithdrawalStatus, error) {
	withdrawalID = strings.TrimSpace(withdrawalID)
	if withdrawalID == "" {
		return nil, fmt.Errorf("withdrawal_id is required")
	}

	provider, key, err := s.spotAPIKeyFor(ctx, rawProvider, PermWithdraw)
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

// WithdrawalAlreadySent reports whether a BTC withdrawal matching this trade has
// already gone out, so a recovery retry doesn't double-withdraw. It returns the
// matched status (non-nil) when one is found, or nil when it is safe to (re)send.
//
// Binance is matched exactly by clientRequestID (it stores withdrawOrderId). Kraken
// has no client key, so it is matched heuristically by destination address + amount
// within the given lookback window. Records in a cancelled/rejected/failed state do
// not count as sent (the funds never left), so a retry is allowed.
func (s *Service) WithdrawalAlreadySent(ctx context.Context, rawProvider, clientRequestID string, amountBTC float64, address string, within time.Duration) (*WithdrawalStatus, error) {
	provider, key, err := s.spotAPIKeyFor(ctx, rawProvider, PermWithdraw)
	if err != nil {
		return nil, err
	}
	if within <= 0 {
		within = 24 * time.Hour
	}

	switch strings.ToLower(provider) {
	case "binance":
		return s.binanceWithdrawalAlreadySent(ctx, key, clientRequestID, amountBTC, address, within)
	case "kraken":
		return s.krakenWithdrawalAlreadySent(ctx, key, amountBTC, address, within)
	default:
		return nil, fmt.Errorf("withdrawal lookup not supported for %s", provider)
	}
}

func (s *Service) binanceWithdrawalAlreadySent(ctx context.Context, key db.APIKey, clientRequestID string, amountBTC float64, address string, within time.Duration) (*WithdrawalStatus, error) {
	client := binance.NewClient(s.cfg.BinanceBaseURL, key.APIKey, key.APISecret)
	bridge := lnd.NewBinanceBridge(client)

	// Exact match by our client request id when available.
	if strings.TrimSpace(clientRequestID) != "" {
		recs, err := bridge.GetWithdrawHistory(ctx, lnd.ExchangeWithdrawHistoryParams{
			Coin:            "BTC",
			ClientRequestID: strings.TrimSpace(clientRequestID),
		})
		if err != nil {
			return nil, fmt.Errorf("binance: withdraw history: %w", err)
		}
		for _, rec := range recs {
			if binanceWithdrawCountsAsSent(rec.Status) {
				return binanceWithdrawalStatusFromRecord(rec), nil
			}
		}
	}

	// Fallback: address + amount within the window over recent history.
	recs, err := bridge.GetWithdrawHistory(ctx, lnd.ExchangeWithdrawHistoryParams{Coin: "BTC"})
	if err != nil {
		return nil, fmt.Errorf("binance: withdraw history: %w", err)
	}
	for _, rec := range recs {
		if !binanceWithdrawCountsAsSent(rec.Status) {
			continue
		}
		if addressMatches(rec.Address, address) && amountMatches(rec.Amount, amountBTC) {
			return binanceWithdrawalStatusFromRecord(rec), nil
		}
	}
	return nil, nil
}

func (s *Service) krakenWithdrawalAlreadySent(ctx context.Context, key db.APIKey, amountBTC float64, address string, within time.Duration) (*WithdrawalStatus, error) {
	client := kraken.NewClient(s.cfg.KrakenBaseURL, key.APIKey, key.APISecret)
	end := time.Now()
	start := end.Add(-within)
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
		if !krakenWithdrawCountsAsSent(rec.Status) {
			continue
		}
		if addressMatches(rec.Info, address) && amountMatches(rec.Amount, amountBTC) {
			return &WithdrawalStatus{
				Provider:      "kraken",
				WithdrawalID:  rec.RefID,
				CexStatus:     krakenWithdrawStatusLabel(rec.Status),
				CexStatusCode: krakenWithdrawStatusCode(rec.Status),
				TxHash:        strings.TrimSpace(rec.TxID),
				Address:       rec.Info,
				AmountBTC:     rec.Amount,
				FeeBTC:        parseDecimal(rec.Fee),
				UpdatedAt:     time.Unix(rec.Time, 0).UTC().Format(time.RFC3339),
			}, nil
		}
	}
	return nil, nil
}

// binanceWithdrawCountsAsSent is false for cancelled(1)/rejected(3)/failed(5), where
// the funds never left and a retry is safe.
func binanceWithdrawCountsAsSent(status int) bool {
	switch status {
	case 1, 3, 5:
		return false
	default:
		return true
	}
}

// krakenWithdrawCountsAsSent is false only for an explicit Failure.
func krakenWithdrawCountsAsSent(status string) bool {
	return strings.TrimSpace(status) != "Failure"
}

func addressMatches(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && a == b
}

// amountMatches compares a decimal-string amount to a target BTC amount, tolerating
// small fee/rounding differences.
func amountMatches(raw string, target float64) bool {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return false
	}
	diff := v - target
	if diff < 0 {
		diff = -diff
	}
	tol := target * 0.01
	if tol < 1e-5 {
		tol = 1e-5
	}
	return diff <= tol
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
		FeeBTC:        parseDecimal(rec.Fee),
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
			FeeBTC:        parseDecimal(rec.Fee),
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
