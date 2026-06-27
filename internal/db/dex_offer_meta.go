package db

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DexOfferMeta is the structured DexOfferCreation JSON used by bid tradeflow and dexrelay.
type DexOfferMeta struct {
	SelectedOffer     json.RawMessage    `json:"selected_offer,omitempty"`
	LNDReceiveAddress string             `json:"lnd_receive_address,omitempty"`
	CexWithdrawal     *CexWithdrawalMeta `json:"cex_withdrawal,omitempty"`
	Execution         map[string]any     `json:"execution,omitempty"`
	TradeflowAt       *time.Time         `json:"tradeflow_at,omitempty"`
	// TradeflowOrder is an opaque JSON snapshot of the tradeflow.Order that started
	// this row, stashed at creation so the healthcheck recovery worker can rebuild
	// the original parameters (hedge venue/side/notional, provider, …) and resume a
	// stuck cycle. The db package only carries the bytes — it does not import tradeflow.
	TradeflowOrder json.RawMessage `json:"tradeflow_order,omitempty"`
}

// CexWithdrawalMeta tracks the CEX→LND withdrawal leg for confirmation polling.
type CexWithdrawalMeta struct {
	WithdrawalID    string `json:"withdrawal_id"`
	ClientRequestID string `json:"client_request_id,omitempty"`
	Provider        string `json:"provider"`
	TxHash          string `json:"tx_hash,omitempty"`
	Confirmations   int    `json:"confirmations,omitempty"`
}

// CexToDexRelayDetails records the LND→DEX on-chain (or LN) relay leg.
type CexToDexRelayDetails struct {
	TxID           string    `json:"txid,omitempty"`
	PaymentHash    string    `json:"payment_hash,omitempty"`
	AmountSat      int64     `json:"amount_sat"`
	SatPerVbyte    int64     `json:"sat_per_vbyte,omitempty"`
	DepositAddress string    `json:"deposit_address,omitempty"`
	Invoice        string    `json:"invoice,omitempty"`
	PaymentMethod  string    `json:"payment_method,omitempty"`
	SentAt         time.Time `json:"sent_at"`
}

// ParseDexOfferCreation unmarshals DexOfferCreation JSON into DexOfferMeta.
func ParseDexOfferCreation(raw json.RawMessage) (DexOfferMeta, error) {
	if len(raw) == 0 {
		return DexOfferMeta{}, nil
	}
	var meta DexOfferMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return DexOfferMeta{}, fmt.Errorf("parse DexOfferCreation: %w", err)
	}
	return meta, nil
}

// MarshalDexOfferCreation encodes DexOfferMeta as JSON for storage.
func MarshalDexOfferCreation(meta DexOfferMeta) (json.RawMessage, error) {
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal DexOfferCreation: %w", err)
	}
	return raw, nil
}

// OfferID returns the linked DEX offer id from execution or selected_offer.
func (m DexOfferMeta) OfferID() string {
	if id := stringFieldMap(m.Execution, "offer_id"); id != "" {
		return id
	}
	if len(m.SelectedOffer) == 0 {
		return ""
	}
	var flat struct {
		OfferID string `json:"offer_id"`
	}
	if err := json.Unmarshal(m.SelectedOffer, &flat); err == nil && flat.OfferID != "" {
		return flat.OfferID
	}
	var nested struct {
		OfferID *string `json:"offer_id"`
	}
	if err := json.Unmarshal(m.SelectedOffer, &nested); err == nil && nested.OfferID != nil {
		return strings.TrimSpace(*nested.OfferID)
	}
	return ""
}

// Exchange returns the DEX venue slug from execution or selected_offer.
func (m DexOfferMeta) Exchange() string {
	if ex := stringFieldMap(m.Execution, "exchange"); ex != "" {
		return ex
	}
	if len(m.SelectedOffer) == 0 {
		return ""
	}
	var flat struct {
		Exchange string `json:"exchange"`
	}
	if err := json.Unmarshal(m.SelectedOffer, &flat); err == nil && flat.Exchange != "" {
		return flat.Exchange
	}
	return ""
}

// ContractID returns HodlHodl contract_id or Bisq/RoboSats trade/position id from execution.
func (m DexOfferMeta) ContractID() string {
	if id := stringFieldMap(m.Execution, "contract_id"); id != "" {
		return id
	}
	if id := stringFieldMap(m.Execution, "trade_id"); id != "" {
		return id
	}
	if id := stringFieldMap(m.Execution, "position_id"); id != "" {
		return id
	}
	contract, ok := m.Execution["contract"].(map[string]any)
	if ok {
		if id := stringFieldMap(contract, "id"); id != "" {
			return id
		}
	}
	return ""
}

// EscrowOrDepositAddress returns the on-chain deposit target from execution if present.
func (m DexOfferMeta) EscrowOrDepositAddress() string {
	if addr := stringFieldMap(m.Execution, "escrow_address"); addr != "" {
		return addr
	}
	if addr := stringFieldMap(m.Execution, "deposit_address"); addr != "" {
		return addr
	}
	return ""
}

// LNInvoice returns a BOLT11 invoice from execution when the offer requires lightning.
func (m DexOfferMeta) LNInvoice() string {
	return stringFieldMap(m.Execution, "invoice")
}

// HasRelayCompleted reports whether CexToDexWithdrawalTxDetails already records a sent relay.
func HasRelayCompleted(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var details CexToDexRelayDetails
	if err := json.Unmarshal(raw, &details); err != nil {
		return false
	}
	return strings.TrimSpace(details.TxID) != "" || strings.TrimSpace(details.PaymentHash) != ""
}

// DexPlatformFromSlug maps a frontend/API exchange slug to DexPlatform.
func DexPlatformFromSlug(slug string) DexPlatform {
	switch strings.ToLower(strings.TrimSpace(slug)) {
	case "bisq":
		return DexBisq
	case "bisq2":
		return DexBisq2
	case "hodlhodl":
		return DexHodlHodl
	case "robosats":
		return DexRoboSats
	case "peachbitcoin":
		return DexPeachBitcoin
	default:
		return DexPlatform(slug)
	}
}

// CexExchangeFromProvider maps a CEX provider slug to CexExchange.
func CexExchangeFromProvider(provider string) CexExchange {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "binance":
		return CexBinance
	case "kraken":
		return CexKraken
	case "coinbase":
		return CexCoinbase
	case "bybit":
		return CexBybit
	case "okx":
		return CexOKX
	case "bitfinex":
		return CexBitfinex
	case "gemini":
		return CexGemini
	case "bitstamp":
		return CexBitStamp
	default:
		return CexExchange(provider)
	}
}

// WithdrawalConfirmationsMet is true when the CEX withdrawal has at least one on-chain confirmation.
func WithdrawalConfirmationsMet(confirmations int) bool {
	return confirmations >= 1
}

func stringFieldMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case *string:
		if t == nil {
			return ""
		}
		return strings.TrimSpace(*t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}
