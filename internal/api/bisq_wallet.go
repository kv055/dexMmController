package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"strings"

	"dexMmController/internal/exchangeconnectors/dex/bisq"
)

const (
	maxBisqWalletBodyBytes = 8 << 10
	maxBisqMemoBytes       = 512
	// defaultUnlockTimeoutSeconds matches the daemon-side unlock window used by the UI:
	// long enough for an operator session, short enough to auto-relock.
	defaultUnlockTimeoutSeconds = 300
)

var btcAmountPattern = regexp.MustCompile(`^(0|[0-9]{1,4})(\.[0-9]{1,8})?$`)

// bisqWalletClient is the wallet subset of the bisq daemon gRPC client used by the
// wallet-management endpoints.
type bisqWalletClient interface {
	GetBalances(ctx context.Context, currencyCode string) (*bisq.WalletBalances, error)
	GetFundingAddresses(ctx context.Context) ([]bisq.AddressBalance, error)
	GetTransactions(ctx context.Context) ([]bisq.WalletTransaction, error)
	SendBtc(ctx context.Context, address, amountBTC, txFeeRate, memo string) (*bisq.WalletTransaction, error)
	UnlockWallet(ctx context.Context, password string, timeoutSeconds uint64) error
	RequireFundingAddress(ctx context.Context, address string) error
}

type bisqBTCBalanceDTO struct {
	AvailableSats      uint64 `json:"available_sats"`
	ReservedSats       uint64 `json:"reserved_sats"`
	TotalAvailableSats uint64 `json:"total_available_sats"`
	LockedSats         uint64 `json:"locked_sats"`
}

type bisqBSQBalanceDTO struct {
	AvailableConfirmedSats uint64 `json:"available_confirmed_sats"`
	UnverifiedSats         uint64 `json:"unverified_sats"`
	UnconfirmedChangeSats  uint64 `json:"unconfirmed_change_sats"`
	LockedForVotingSats    uint64 `json:"locked_for_voting_sats"`
	LockupBondsSats        uint64 `json:"lockup_bonds_sats"`
	UnlockingBondsSats     uint64 `json:"unlocking_bonds_sats"`
}

type bisqWalletBalancesDTO struct {
	BTC *bisqBTCBalanceDTO `json:"btc"`
	BSQ *bisqBSQBalanceDTO `json:"bsq"`
}

type bisqWalletAddressDTO struct {
	Address          string `json:"address"`
	BalanceSats      int64  `json:"balance_sats"`
	NumConfirmations int64  `json:"num_confirmations"`
	IsAddressUnused  bool   `json:"is_address_unused"`
}

type bisqWalletTransactionDTO struct {
	TxID          string `json:"tx_id"`
	InputSumSats  uint64 `json:"input_sum_sats"`
	OutputSumSats uint64 `json:"output_sum_sats"`
	FeeSats       uint64 `json:"fee_sats"`
	SizeBytes     int64  `json:"size_bytes"`
	IsPending     bool   `json:"is_pending"`
	Memo          string `json:"memo,omitempty"`
}

type bisqWithdrawRequest struct {
	Address   string  `json:"address"`
	AmountBTC string  `json:"amount_btc"`
	TxFeeRate *string `json:"tx_fee_rate"`
	Memo      string  `json:"memo"`
	// Password optionally unlocks an encrypted wallet before the send.
	Password string `json:"password"`
}

func toBisqWalletTransactionDTO(tx bisq.WalletTransaction) bisqWalletTransactionDTO {
	return bisqWalletTransactionDTO{
		TxID: tx.TxID, InputSumSats: tx.InputSumSats, OutputSumSats: tx.OutputSumSats,
		FeeSats: tx.FeeSats, SizeBytes: tx.SizeBytes, IsPending: tx.IsPending, Memo: tx.Memo,
	}
}

// handleBisqWalletBalances serves GET /api/bisq/wallet/balances with both currency views.
// The BSQ leg is best-effort so a DAO-less deployment still reports BTC balances.
func (s *Server) handleBisqWalletBalances(w http.ResponseWriter, r *http.Request) {
	if s.bisqWallet == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq wallet is unavailable"))
		return
	}
	out := bisqWalletBalancesDTO{}
	if btc, err := s.bisqWallet.GetBalances(r.Context(), "BTC"); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("fetch BTC balance: %w", err))
		return
	} else if btc.BTC != nil {
		out.BTC = &bisqBTCBalanceDTO{
			AvailableSats:      btc.BTC.AvailableSats,
			ReservedSats:       btc.BTC.ReservedSats,
			TotalAvailableSats: btc.BTC.TotalAvailableSats,
			LockedSats:         btc.BTC.LockedSats,
		}
	}
	if bsq, err := s.bisqWallet.GetBalances(r.Context(), "BSQ"); err != nil {
		log.Printf("bisq wallet balances: BSQ leg unavailable: %v", err)
	} else if bsq.BSQ != nil {
		out.BSQ = &bisqBSQBalanceDTO{
			AvailableConfirmedSats: bsq.BSQ.AvailableConfirmedSats,
			UnverifiedSats:         bsq.BSQ.UnverifiedSats,
			UnconfirmedChangeSats:  bsq.BSQ.UnconfirmedChangeSats,
			LockedForVotingSats:    bsq.BSQ.LockedForVotingSats,
			LockupBondsSats:        bsq.BSQ.LockupBondsSats,
			UnlockingBondsSats:     bsq.BSQ.UnlockingBondsSats,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleBisqWalletAddresses serves GET /api/bisq/wallet/addresses.
func (s *Server) handleBisqWalletAddresses(w http.ResponseWriter, r *http.Request) {
	if s.bisqWallet == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq wallet is unavailable"))
		return
	}
	addrs, err := s.bisqWallet.GetFundingAddresses(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := make([]bisqWalletAddressDTO, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, bisqWalletAddressDTO{
			Address: a.Address, BalanceSats: a.BalanceSats,
			NumConfirmations: a.NumConfirmations, IsAddressUnused: a.IsAddressUnused,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"addresses": out})
}

// handleBisqWalletTransactions serves GET /api/bisq/wallet/transactions.
func (s *Server) handleBisqWalletTransactions(w http.ResponseWriter, r *http.Request) {
	if s.bisqWallet == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq wallet is unavailable"))
		return
	}
	txs, err := s.bisqWallet.GetTransactions(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	out := make([]bisqWalletTransactionDTO, 0, len(txs))
	for _, tx := range txs {
		out = append(out, toBisqWalletTransactionDTO(tx))
	}
	writeJSON(w, http.StatusOK, map[string]any{"transactions": out})
}

// handleBisqWalletWithdraw serves POST /api/bisq/wallet/withdraw.
func (s *Server) handleBisqWalletWithdraw(w http.ResponseWriter, r *http.Request) {
	if s.bisqWallet == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq wallet is unavailable"))
		return
	}
	req, ok := decodeBisqWithdrawRequest(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Password) != "" {
		if err := s.bisqWallet.UnlockWallet(r.Context(), req.Password, defaultUnlockTimeoutSeconds); err != nil {
			writeError(w, http.StatusBadGateway, fmt.Errorf("unlock wallet: %w", err))
			return
		}
	}
	tx, err := s.bisqWallet.SendBtc(r.Context(), req.Address, req.AmountBTC, derefString(req.TxFeeRate), req.Memo)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transaction": toBisqWalletTransactionDTO(*tx)})
}

// handleBisqWalletUnlock serves POST /api/bisq/wallet/unlock for encrypted wallets.
func (s *Server) handleBisqWalletUnlock(w http.ResponseWriter, r *http.Request) {
	if s.bisqWallet == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("bisq wallet is unavailable"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBisqWalletBodyBytes)
	var req struct {
		Password       string  `json:"password"`
		TimeoutSeconds *uint64 `json:"timeout_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	password := strings.TrimSpace(req.Password)
	if password == "" {
		writeError(w, http.StatusBadRequest, errors.New("password is required"))
		return
	}
	timeout := uint64(defaultUnlockTimeoutSeconds)
	if req.TimeoutSeconds != nil && *req.TimeoutSeconds > 0 {
		if *req.TimeoutSeconds > math.MaxInt32 {
			writeError(w, http.StatusBadRequest, errors.New("timeout_seconds is too large"))
			return
		}
		timeout = *req.TimeoutSeconds
	}
	if err := s.bisqWallet.UnlockWallet(r.Context(), password, timeout); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func decodeBisqWithdrawRequest(w http.ResponseWriter, r *http.Request) (bisqWithdrawRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBisqWalletBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req bisqWithdrawRequest
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return req, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, errors.New("request body must contain one JSON object"))
		return req, false
	}
	req.Address = strings.TrimSpace(req.Address)
	if req.Address == "" || len(req.Address) > 128 {
		writeError(w, http.StatusBadRequest, errors.New("address is required and must be at most 128 bytes"))
		return req, false
	}
	req.AmountBTC = strings.TrimSpace(req.AmountBTC)
	if !btcAmountPattern.MatchString(req.AmountBTC) ||
		strings.ReplaceAll(req.AmountBTC, ".", "") == "" ||
		req.AmountBTC == "0" || req.AmountBTC == "0.00000000" {
		writeError(w, http.StatusBadRequest, errors.New("amount_btc must be a positive decimal BTC amount"))
		return req, false
	}
	if len(req.Memo) > maxBisqMemoBytes {
		writeError(w, http.StatusBadRequest, fmt.Errorf("memo must be at most %d bytes", maxBisqMemoBytes))
		return req, false
	}
	if req.TxFeeRate != nil {
		trimmed := strings.TrimSpace(*req.TxFeeRate)
		if trimmed == "" {
			req.TxFeeRate = nil
		} else {
			var fee float64
			if _, err := fmt.Sscanf(trimmed, "%g", &fee); err != nil || fee <= 0 || fee > 10000 {
				writeError(w, http.StatusBadRequest, errors.New("tx_fee_rate must be a positive sats/byte number"))
				return req, false
			}
			req.TxFeeRate = &trimmed
		}
	}
	return req, true
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
