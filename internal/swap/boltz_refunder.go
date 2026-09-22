package swap

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	boltz "github.com/BoltzExchange/boltz-client/v2/pkg/boltz"
	"github.com/btcsuite/btcd/btcec/v2"
)

type RecoverySource interface {
	LoadRecovery(context.Context, string) (string, error)
}
type RefundDestination interface {
	NewRefundAddress(context.Context) (string, error)
}

// LibraryRefunder delegates swap-tree, transaction and MuSig work to the
// official Boltz Client library. Its constructor first attempts cooperation;
// the library automatically reconstructs a script-path fallback when needed.
type LibraryRefunder struct {
	API         *boltz.Api
	Recovery    RecoverySource
	Destination RefundDestination
	SatPerVByte float64
}

func (r LibraryRefunder) Refund(ctx context.Context, id string) error {
	if r.API == nil || r.Recovery == nil || r.Destination == nil {
		return fmt.Errorf("boltz refund recovery is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := r.Recovery.LoadRecovery(ctx, id)
	if err != nil {
		return err
	}
	var rescue struct {
		PrivateKey     string                `json:"private_key"`
		SwapTree       *boltz.SerializedTree `json:"swap_tree"`
		Timeout        uint32                `json:"timeout_block_height"`
		ClaimPublicKey string                `json:"claim_public_key"`
		LockupAddress  string                `json:"lockup_address"`
	}
	if err = json.Unmarshal([]byte(raw), &rescue); err != nil {
		return fmt.Errorf("decode encrypted Boltz rescue material: %w", err)
	}
	keyBytes, err := hex.DecodeString(rescue.PrivateKey)
	if err != nil {
		return err
	}
	key, _ := btcec.PrivKeyFromBytes(keyBytes)
	claimBytes, err := hex.DecodeString(rescue.ClaimPublicKey)
	if err != nil {
		return err
	}
	claimKey, err := btcec.ParsePubKey(claimBytes)
	if err != nil {
		return err
	}
	tree := rescue.SwapTree.Deserialize()
	if err = tree.Init(boltz.CurrencyBtc, false, key, claimKey); err != nil {
		return err
	}
	lockupResponse, err := r.API.GetSwapTransaction(id)
	if err != nil {
		return err
	}
	lockup, err := boltz.NewTxFromHex(boltz.CurrencyBtc, lockupResponse.Hex, nil)
	if err != nil {
		return err
	}
	vout, _, err := lockup.FindVout(boltz.MainNet, rescue.LockupAddress)
	if err != nil {
		return err
	}
	address, err := r.Destination.NewRefundAddress(ctx)
	if err != nil {
		return err
	}
	rate := r.SatPerVByte
	if rate <= 0 {
		rate = 2
	}
	transaction, results, err := boltz.ConstructTransaction(boltz.MainNet, boltz.CurrencyBtc, []boltz.OutputDetails{{LockupTransaction: lockup, Vout: vout, Address: address, PrivateKey: key, Preimage: []byte{}, TimeoutBlockHeight: rescue.Timeout, SwapTree: tree, Cooperative: true, SwapId: id, SwapType: boltz.NormalSwap}}, boltz.Fee{SatsPerVbyte: &rate}, r.API)
	if err != nil {
		return err
	}
	if result := results[id]; result.Err != nil {
		return result.Err
	}
	serialized, err := transaction.Serialize()
	if err != nil {
		return err
	}
	_, err = r.API.BroadcastTransaction(boltz.CurrencyBtc, serialized)
	return err
}
