package dexaccount

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl/escrow"
	"dexMmController/internal/exchangeconnectors/dex/robosats"
)

// HodlHodlEscrowConfig holds the secrets needed to sign a HodlHodl release locally: the
// account-level encrypted seed and the payment password that decrypts it. Both ship via
// dotenvx .env.enc; neither is logged.
type HodlHodlEscrowConfig struct {
	EncryptedSeed   string
	PaymentPassword string
}

// Releaser performs the Stage-6 BTC release on a DEX, dispatching per venue. It mirrors
// Taker: HodlHodl signs a multisig release locally, while Bisq and RoboSats issue a single
// confirm call (their daemon/coordinator holds the keys and settles).
type Releaser struct {
	repo     *db.TransactionRepository
	bisq     *bisq.Client
	hodlhodl *hodlhodl.Client
	robosats *robosats.Client
	hhEscrow HodlHodlEscrowConfig
}

// NewReleaser creates a Releaser backed by the configured DEX clients and HodlHodl escrow
// secrets.
func NewReleaser(
	repo *db.TransactionRepository,
	bisqClient *bisq.Client,
	hodlhodlClient *hodlhodl.Client,
	robosatsClient *robosats.Client,
	hhEscrow HodlHodlEscrowConfig,
) *Releaser {
	return &Releaser{
		repo:     repo,
		bisq:     bisqClient,
		hodlhodl: hodlhodlClient,
		robosats: robosatsClient,
		hhEscrow: hhEscrow,
	}
}

// ReleaseInput identifies the trade to release.
type ReleaseInput struct {
	// Exchange is the DEX venue slug (e.g. "hodlhodl", "bisq", "robosats").
	Exchange string
	// ContractID is the venue-native contract/trade/order id taken from execution.
	ContractID string
}

// ReleaseResult is the outcome of a release.
type ReleaseResult struct {
	// ReleaseTxID is the on-chain release/payout transaction id when the venue exposes one.
	ReleaseTxID string
	// Raw carries any extra venue-specific detail worth persisting.
	Raw map[string]any
}

// Release releases the escrowed BTC to the buyer on the given DEX.
func (r *Releaser) Release(ctx context.Context, in ReleaseInput) (*ReleaseResult, error) {
	contractID := strings.TrimSpace(in.ContractID)
	if contractID == "" {
		return nil, fmt.Errorf("dexaccount: contract_id is required to release")
	}

	switch db.DexPlatformFromSlug(in.Exchange) {
	case db.DexHodlHodl:
		return r.releaseHodlHodl(ctx, contractID)
	case db.DexBisq:
		return r.releaseBisq(ctx, contractID)
	case db.DexRoboSats:
		return r.releaseRoboSats(ctx, contractID)
	default:
		return nil, fmt.Errorf("dexaccount: release not supported for exchange %q", in.Exchange)
	}
}

// releaseHodlHodl signs and submits the multisig release: fetch the contract (for the
// escrow index + witness script) and the unsigned release transaction, decrypt+derive the
// per-contract key, sign locally, and submit the signed hex.
func (r *Releaser) releaseHodlHodl(ctx context.Context, contractID string) (*ReleaseResult, error) {
	if r.hodlhodl == nil {
		return nil, fmt.Errorf("hodlhodl client is not configured")
	}
	if strings.TrimSpace(r.hhEscrow.EncryptedSeed) == "" || strings.TrimSpace(r.hhEscrow.PaymentPassword) == "" {
		return nil, fmt.Errorf("hodlhodl escrow seed/payment password not configured")
	}

	contract, err := r.hodlhodl.GetContract(ctx, contractID)
	if err != nil {
		return nil, fmt.Errorf("get contract: %w", err)
	}
	witnessScript, err := hex.DecodeString(strings.TrimSpace(contract.Escrow.WitnessScript))
	if err != nil {
		return nil, fmt.Errorf("decode escrow witness script: %w", err)
	}

	rt, err := r.hodlhodl.GetReleaseTransaction(ctx, contractID)
	if err != nil {
		return nil, fmt.Errorf("get release transaction: %w", err)
	}

	seed, err := escrow.DecryptSeed(r.hhEscrow.EncryptedSeed, r.hhEscrow.PaymentPassword)
	if err != nil {
		return nil, err
	}
	key, err := escrow.DeriveKey(seed, contract.Escrow.Index)
	if err != nil {
		return nil, err
	}
	signedHex, err := escrow.SignRelease(rt.Hex, rt.InAmounts, witnessScript, key)
	if err != nil {
		return nil, err
	}

	updated, err := r.hodlhodl.SubmitReleaseTransaction(ctx, contractID, signedHex)
	if err != nil {
		return nil, fmt.Errorf("submit release transaction: %w", err)
	}
	return &ReleaseResult{
		ReleaseTxID: updated.Escrow.ReleaseTransactionID,
		Raw: map[string]any{
			"exchange":    "hodlhodl",
			"contract_id": contractID,
			"status":      string(updated.Status),
		},
	}, nil
}

// releaseBisq tells the bisq daemon the fiat arrived; it signs+broadcasts the payout
// internally. The reply is empty, so we read the resulting payout tx id back via GetPosition.
func (r *Releaser) releaseBisq(ctx context.Context, tradeID string) (*ReleaseResult, error) {
	if r.bisq == nil {
		return nil, fmt.Errorf("bisq client is not configured")
	}
	if err := r.bisq.ConfirmPaymentReceived(ctx, tradeID); err != nil {
		return nil, fmt.Errorf("confirm payment received: %w", err)
	}
	result := &ReleaseResult{Raw: map[string]any{"exchange": "bisq", "trade_id": tradeID}}
	if trade, err := r.bisq.GetPosition(ctx, tradeID); err == nil && trade != nil {
		result.ReleaseTxID = trade.PayoutTxID
		result.Raw["state"] = trade.State
		result.Raw["phase"] = trade.Phase
	}
	return result, nil
}

// releaseRoboSats confirms fiat received; the coordinator settles the hold invoice to the
// buyer. Uses the latest stored robot token (same lookup as Taker).
func (r *Releaser) releaseRoboSats(ctx context.Context, orderID string) (*ReleaseResult, error) {
	if r.robosats == nil {
		return nil, fmt.Errorf("robosats client is not configured")
	}
	client := r.robosats
	if token := robosatsTokenForRepo(ctx, r.repo); token != "" {
		client = r.robosats.WithAuthToken(token)
	}
	pos, err := client.ConfirmFiatReceived(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("confirm fiat received: %w", err)
	}
	return &ReleaseResult{
		Raw: map[string]any{"exchange": "robosats", "order_id": orderID, "status": pos.Status},
	}, nil
}

// robosatsTokenForRepo returns the first active stored Robosats robot token, or "" if none
// (best-effort; errors are logged and treated as no token).
func robosatsTokenForRepo(ctx context.Context, repo *db.TransactionRepository) string {
	if repo == nil {
		return ""
	}
	keys, err := repo.GetActiveAPIKeysForProvider(ctx, "Robosats")
	if err != nil {
		log.Printf("dexaccount: load Robosats token: %v", err)
		return ""
	}
	for _, key := range keys {
		if token := strings.TrimSpace(key.APIKey); token != "" {
			return token
		}
	}
	return ""
}
