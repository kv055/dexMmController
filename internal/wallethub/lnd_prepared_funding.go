package lnd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

type FundingLease struct {
	ID       string `json:"id"`
	Outpoint struct {
		TxID      string `json:"txid_str"`
		TxIDBytes string `json:"txid_bytes,omitempty"`
		Index     uint32 `json:"output_index"`
	} `json:"outpoint"`
	Expiration string `json:"expiration"`
}

// PreparedFunding is a signed, not broadcast transaction. Persist it durably
// before PublishPreparedFunding. Never recreate it after an ambiguous publish.
type PreparedFunding struct {
	TxID       string         `json:"txid"`
	RawTx      string         `json:"raw_tx"`
	Address    string         `json:"address"`
	AmountSats uint64         `json:"amount_sats"`
	FeeSats    uint64         `json:"fee_sats"`
	Leases     []FundingLease `json:"leases"`
}

// PrepareFunding uses WalletKit available in our pinned LND 0.18.4. In
// particular, it does not rely on newer custom-lock or fee-cap RPC fields that
// older LNDs silently ignore. The absolute fee cap is checked before signing.
func (c *Client) PrepareFunding(ctx context.Context, address string, amount, maxFee, satPerVByte uint64, deadline time.Time) (_ *PreparedFunding, err error) {
	if amount == 0 || amount > 21_000_000*100_000_000 || maxFee == 0 || maxFee > 21_000_000*100_000_000 || satPerVByte == 0 || !time.Now().Before(deadline) {
		return nil, errors.New("invalid funding amount, fee cap, or deadline")
	}
	a, err := btcutil.DecodeAddress(address, &chaincfg.MainNetParams)
	if err != nil || !a.IsForNet(&chaincfg.MainNetParams) {
		return nil, errors.New("invalid mainnet funding address")
	}
	script, err := txscript.PayToAddrScript(a)
	if err != nil {
		return nil, err
	}
	var funded struct {
		PSBT   string         `json:"funded_psbt"`
		Change int            `json:"change_output_index"`
		Leases []FundingLease `json:"locked_utxos"`
	}
	err = c.post(ctx, "/v2/wallet/psbt/fund", map[string]any{
		"raw":           map[string]any{"outputs": map[string]string{address: strconv.FormatUint(amount, 10)}},
		"sat_per_vbyte": strconv.FormatUint(satPerVByte, 10), "min_confs": 1, "spend_unconfirmed": false,
	}, &funded)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		for _, lease := range funded.Leases {
			var reply struct{}
			// Only release this preparation's leases, never another action's.
			_ = c.post(cleanup, "/v2/wallet/utxos/release", map[string]any{"id": lease.ID, "outpoint": lease.Outpoint}, &reply)
		}
	}()
	packet, err := psbt.NewFromRawBytes(bytes.NewBufferString(funded.PSBT), true)
	if err != nil {
		return nil, fmt.Errorf("funded PSBT: %w", err)
	}
	fee, err := validateFundedPSBT(packet, script, amount, maxFee, funded.Change)
	if err != nil {
		return nil, err
	}
	if len(funded.Leases) != len(packet.UnsignedTx.TxIn) {
		return nil, errors.New("funding inputs are not all leased")
	}
	for _, lease := range funded.Leases {
		expires, parseErr := strconv.ParseInt(lease.Expiration, 10, 64)
		if parseErr != nil || expires <= deadline.Unix() {
			return nil, errors.New("funding input lease does not cover execution deadline")
		}
	}
	if !time.Now().Before(deadline) {
		return nil, errors.New("funding deadline expired before signing")
	}
	var finalized struct {
		RawTx string `json:"raw_final_tx"`
	}
	if err := c.post(ctx, "/v2/wallet/psbt/finalize", map[string]string{"funded_psbt": funded.PSBT}, &finalized); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(finalized.RawTx)
	if err != nil {
		return nil, err
	}
	var signed wire.MsgTx
	reader := bytes.NewReader(raw)
	if err := signed.Deserialize(reader); err != nil {
		return nil, err
	}
	if reader.Len() != 0 {
		return nil, errors.New("trailing signed transaction data")
	}
	// Signing may add scriptSig/witness, but must not change any input,
	// sequence, output, version or locktime from the checked funded PSBT.
	unsigned := signed.Copy()
	for _, in := range unsigned.TxIn {
		in.SignatureScript = nil
		in.Witness = nil
	}
	var actual, expected bytes.Buffer
	if err := unsigned.Serialize(&actual); err != nil {
		return nil, err
	}
	if err := packet.UnsignedTx.Serialize(&expected); err != nil {
		return nil, err
	}
	if !bytes.Equal(actual.Bytes(), expected.Bytes()) {
		return nil, errors.New("finalization changed the checked transaction")
	}
	return &PreparedFunding{TxID: signed.TxHash().String(), RawTx: finalized.RawTx, Address: address, AmountSats: amount, FeeSats: fee, Leases: funded.Leases}, nil
}

func validateFundedPSBT(p *psbt.Packet, script []byte, amount, maxFee uint64, change int) (uint64, error) {
	if p == nil || len(p.Inputs) == 0 || len(p.Inputs) != len(p.UnsignedTx.TxIn) || len(p.UnsignedTx.TxOut) > 2 {
		return 0, errors.New("invalid funding PSBT")
	}
	if change < -1 || change >= len(p.UnsignedTx.TxOut) {
		return 0, errors.New("invalid change output")
	}
	var inTotal, outTotal, paid int64
	for i, in := range p.Inputs {
		utxo := in.WitnessUtxo
		if utxo == nil && in.NonWitnessUtxo != nil {
			prev := p.UnsignedTx.TxIn[i].PreviousOutPoint
			if in.NonWitnessUtxo.TxHash() != prev.Hash || int(prev.Index) >= len(in.NonWitnessUtxo.TxOut) {
				return 0, errors.New("invalid input transaction")
			}
			utxo = in.NonWitnessUtxo.TxOut[prev.Index]
		}
		if utxo == nil || utxo.Value <= 0 || utxo.Value > 21_000_000*100_000_000 {
			return 0, errors.New("missing or invalid input value")
		}
		inTotal += utxo.Value
		if inTotal > 21_000_000*100_000_000 {
			return 0, errors.New("input sum out of range")
		}
	}
	for i, out := range p.UnsignedTx.TxOut {
		if out.Value < 0 || out.Value > 21_000_000*100_000_000 {
			return 0, errors.New("output value out of range")
		}
		outTotal += out.Value
		if i == change {
			continue
		}
		if !bytes.Equal(out.PkScript, script) {
			return 0, errors.New("unexpected funding output")
		}
		paid += out.Value
	}
	if paid != int64(amount) || outTotal > inTotal || inTotal-outTotal <= 0 || uint64(inTotal-outTotal) > maxFee {
		return 0, errors.New("funding output or miner fee exceeds accepted limits")
	}
	return uint64(inTotal - outTotal), nil
}

func (c *Client) PublishPreparedFunding(ctx context.Context, p PreparedFunding, deadline time.Time) error {
	if !time.Now().Before(deadline) {
		return errors.New("funding deadline expired before broadcast")
	}
	raw, err := base64.StdEncoding.DecodeString(p.RawTx)
	if err != nil {
		return err
	}
	var tx wire.MsgTx
	reader := bytes.NewReader(raw)
	if err := tx.Deserialize(reader); err != nil {
		return err
	}
	if reader.Len() != 0 || tx.TxHash().String() != p.TxID {
		return errors.New("prepared funding hash mismatch")
	}
	var reply struct {
		PublishError string `json:"publish_error"`
	}
	if err := c.post(ctx, "/v2/wallet/tx", map[string]string{"tx_hex": p.RawTx, "label": "bisq-funding"}, &reply); err != nil {
		return err
	}
	if reply.PublishError != "" {
		return fmt.Errorf("publish funding: %s", reply.PublishError)
	}
	return nil
}

// PreparedFundingPublished only queries the saved hash; it never sends coins.
func (c *Client) PreparedFundingPublished(ctx context.Context, p PreparedFunding) (bool, error) {
	var reply struct {
		TxHash string `json:"tx_hash"`
	}
	if err := c.get(ctx, "/v2/wallet/tx?txid="+url.QueryEscape(p.TxID), &reply); err != nil {
		return false, err
	}
	if reply.TxHash != p.TxID {
		return false, errors.New("LND returned a different funding transaction")
	}
	return true, nil
}
