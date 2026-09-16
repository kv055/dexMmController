package dexaccount

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/bisq2"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl/escrow"
	"dexMmController/internal/exchangeconnectors/dex/mostro"
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
	bisq2    *bisq2.Client
	hodlhodl *hodlhodl.Client
	robosats *robosats.Client
	mostro   *mostro.Manager
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

func (r *Releaser) WithMostro(m *mostro.Manager) *Releaser { r.mostro = m; return r }

func (r *Releaser) WithBisq2(c *bisq2.Client) *Releaser { r.bisq2 = c; return r }

// ReleaseInput identifies the trade to release.
type ReleaseInput struct {
	RequestID uint64
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

// SecurityDepositReturnInput identifies the post-release collateral return for
// a hedgeless bid. Memo is stable across retries and acts as the Bisq wallet
// idempotency key.
type SecurityDepositReturnInput struct {
	RequestID   uint64
	Exchange    string
	ContractID  string
	PoolAddress string
	Memo        string
}

// SecurityDepositReturnResult records how a venue returned the collateral.
// RoboSats unlocks it in-place, while Bisq exposes a separate on-chain tx.
type SecurityDepositReturnResult struct {
	Method string
	TxID   string
}

// Release releases the escrowed BTC to the buyer on the given DEX.
func (r *Releaser) Release(ctx context.Context, in ReleaseInput) (*ReleaseResult, error) {
	contractID := strings.TrimSpace(in.ContractID)
	if contractID == "" {
		return nil, fmt.Errorf("dexaccount: contract_id is required to release")
	}

	switch db.DexPlatformFromSlug(in.Exchange) {
	case db.DexMostro:
		if err := r.mostro.Release(ctx, in.RequestID, contractID); err != nil {
			return nil, err
		}
		return &ReleaseResult{Raw: map[string]any{"exchange": "mostro", "order_id": contractID, "state": "released"}}, nil
	case db.DexHodlHodl:
		return r.releaseHodlHodl(ctx, contractID)
	case db.DexBisq:
		return r.releaseBisq(ctx, contractID)
	case db.DexBisq2:
		return r.releaseBisq2(ctx, contractID)
	case db.DexRoboSats:
		return r.releaseRoboSats(ctx, contractID)
	default:
		return nil, fmt.Errorf("dexaccount: release not supported for exchange %q", in.Exchange)
	}
}

// ReturnSecurityDeposit moves or unlocks hedgeless collateral after the DEX
// release. HodlHodl currently has no security deposit and is therefore a no-op.
func (r *Releaser) ReturnSecurityDeposit(ctx context.Context, in SecurityDepositReturnInput) (*SecurityDepositReturnResult, error) {
	contractID := strings.TrimSpace(in.ContractID)
	if contractID == "" {
		return nil, fmt.Errorf("dexaccount: contract_id is required to return security deposit")
	}
	switch db.DexPlatformFromSlug(in.Exchange) {
	case db.DexBisq:
		if r.bisq == nil {
			return nil, fmt.Errorf("bisq client is not configured")
		}
		address, memo := strings.TrimSpace(in.PoolAddress), strings.TrimSpace(in.Memo)
		if address == "" || memo == "" {
			return nil, fmt.Errorf("bisq pool address and stable memo are required")
		}
		if existing, err := r.bisq.TransactionByMemo(ctx, memo); err != nil {
			return nil, fmt.Errorf("find prior Bisq deposit return: %w", err)
		} else if existing != nil {
			return &SecurityDepositReturnResult{Method: "bisq_withdraw_funds", TxID: existing.TxID}, nil
		}
		if err := r.bisq.WithdrawFunds(ctx, contractID, address, memo); err != nil {
			// A lost RPC response is ambiguous. Re-read wallet history before
			// allowing the caller to retry the trade-specific withdrawal.
			if existing, lookupErr := r.bisq.TransactionByMemo(ctx, memo); lookupErr == nil && existing != nil {
				return &SecurityDepositReturnResult{Method: "bisq_withdraw_funds", TxID: existing.TxID}, nil
			}
			return nil, fmt.Errorf("withdraw Bisq trade funds: %w", err)
		}
		for attempt := 0; attempt < 10; attempt++ {
			existing, err := r.bisq.TransactionByMemo(ctx, memo)
			if err == nil && existing != nil {
				return &SecurityDepositReturnResult{Method: "bisq_withdraw_funds", TxID: existing.TxID}, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
		return nil, fmt.Errorf("Bisq deposit-return transaction was not observable by memo %q", memo)
	case db.DexMostro:
		if err := r.mostro.Release(ctx, in.RequestID, contractID); err != nil {
			return nil, err
		}
		return &SecurityDepositReturnResult{Method: "mostro_bond_cancellation_verified"}, nil
	case db.DexRoboSats:
		return &SecurityDepositReturnResult{Method: "robosats_automatic_bond_unlock"}, nil
	case db.DexHodlHodl:
		return &SecurityDepositReturnResult{Method: "not_required"}, nil
	case db.DexBisq2:
		return &SecurityDepositReturnResult{Method: "not_required"}, nil
	default:
		return nil, fmt.Errorf("dexaccount: security-deposit return not supported for exchange %q", in.Exchange)
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

func (r *Releaser) releaseBisq2(ctx context.Context, tradeID string) (*ReleaseResult, error) {
	if r.bisq2 == nil {
		return nil, fmt.Errorf("bisq2 client is not configured")
	}
	if err := r.bisq2.ConfirmFiatReceipt(ctx, tradeID); err != nil {
		return nil, fmt.Errorf("bisq2: confirm fiat receipt: %w", err)
	}
	return &ReleaseResult{Raw: map[string]any{"exchange": "bisq2", "trade_id": tradeID, "event": "SELLER_CONFIRM_FIAT_RECEIPT"}}, nil
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
