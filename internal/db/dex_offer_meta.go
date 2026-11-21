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
	ResolvedRoute     *ResolvedRoute     `json:"resolved_route,omitempty"`
	LNDReceiveAddress string             `json:"lnd_receive_address,omitempty"`
	CexWithdrawal     *CexWithdrawalMeta `json:"cex_withdrawal,omitempty"`
	Execution         map[string]any     `json:"execution,omitempty"`
	TradeflowAt       *time.Time         `json:"tradeflow_at,omitempty"`
	// TradeflowOrder is an opaque JSON snapshot of the tradeflow.Order that started
	// this row, stashed at creation so the healthcheck recovery worker can rebuild
	// the original parameters (hedge venue/side/notional, provider, …) and resume a
	// stuck cycle. The db package only carries the bytes — it does not import tradeflow.
	TradeflowOrder json.RawMessage `json:"tradeflow_order,omitempty"`
	// BisqExecution is the versioned Bisq v1 quote, limits, and stage metadata.
	BisqExecution *BisqExecutionPlan `json:"bisq_execution,omitempty"`
}

// ResolvedRoute is the immutable execution route selected by Unified API and
// persisted with the transaction for relay and recovery. It contains no DEX
// routing matrix: consumers use Settlement exactly as supplied.
type ResolvedRoute struct {
	ConfigVersion          int64           `json:"config_version"`
	SnapshotRevision       int64           `json:"snapshot_revision"`
	ResolvedAt             string          `json:"resolved_at"`
	TradeMode              string          `json:"trade_mode"`
	TargetDirection        string          `json:"target_direction"`
	MarketSide             string          `json:"market_side"`
	Provider               string          `json:"provider"`
	HedgeVenue             string          `json:"hedge_venue,omitempty"`
	HedgeSide              string          `json:"hedge_side,omitempty"`
	HedgeNotional          float64         `json:"hedge_notional_btc,omitempty"`
	Leverage               float64         `json:"leverage,omitempty"`
	Settlement             string          `json:"settlement"`
	NeedsHub               bool            `json:"needs_hub"`
	NeedsLightning         bool            `json:"needs_lightning"`
	FiatCurrency           string          `json:"fiat_currency"`
	FiatAmount             float64         `json:"fiat_amount"`
	BTCAmount              float64         `json:"btc_amount"`
	RequiredCapital        float64         `json:"required_capital"`
	RequiredBTCLiquidity   float64         `json:"required_btc_liquidity"`
	SecurityDepositBTC     float64         `json:"security_deposit_btc"`
	RoutingFeeAllowanceSat int64           `json:"routing_fee_allowance_sat"`
	CEXWithdrawalFeeBTC    float64         `json:"cex_withdrawal_fee_btc"`
	SwapSafetyHeadroomBTC  float64         `json:"swap_safety_headroom_btc"`
	TotalCostBTC           float64         `json:"total_cost_btc"`
	TotalCostPercent       float64         `json:"total_cost_percent"`
	NetPremiumPercent      float64         `json:"net_premium_percent"`
	CostInputs             RouteCostInputs `json:"cost_inputs"`
}

type RouteCostInputs struct {
	ReferencePrice         float64 `json:"reference_price"`
	FeeTier                string  `json:"fee_tier"`
	CoupledHedge           bool    `json:"coupled_hedge"`
	SpotTakerFeeBTC        float64 `json:"spot_taker_fee_btc"`
	WithdrawalFeeBTC       float64 `json:"withdrawal_fee_btc"`
	HedgeOpenFeeBTC        float64 `json:"hedge_open_fee_btc"`
	HedgeCloseFeeBTC       float64 `json:"hedge_close_fee_btc"`
	ProjectedFundingFeeBTC float64 `json:"projected_funding_fee_btc"`
	FundingCostAvailable   bool    `json:"funding_cost_available"`
	DEXFeeBTC              float64 `json:"dex_fee_btc"`
	MinerFeeBTC            float64 `json:"miner_fee_btc"`
	ConversionFeeBTC       float64 `json:"conversion_fee_btc"`
}

func (r ResolvedRoute) ValidForExecution() bool {
	if r.ConfigVersion <= 0 || r.Provider == "" || r.BTCAmount <= 0 || r.RequiredCapital <= 0 || r.RoutingFeeAllowanceSat < 0 || r.CEXWithdrawalFeeBTC < 0 || r.SwapSafetyHeadroomBTC < 0 {
		return false
	}
	switch r.Settlement {
	case "onchain_direct", "onchain_hub", "onchain_swap":
		return true
	default:
		return false
	}
}

// ValidateAuthoritativeSnapshot applies the stricter contract required at
// public pre-trade and execution boundaries. ValidForExecution intentionally
// remains compatible with routes already persisted by older deployments so
// recovery workers can finish in-flight transactions.
func (r ResolvedRoute) ValidateAuthoritativeSnapshot() error {
	if !r.ValidForExecution() || r.SnapshotRevision <= 0 {
		return fmt.Errorf("base execution fields and snapshot_revision must be valid")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.ResolvedAt); err != nil {
		return fmt.Errorf("resolved_at must be an RFC3339 timestamp")
	}
	r.TargetDirection = strings.ToLower(strings.TrimSpace(r.TargetDirection))
	r.MarketSide = strings.ToLower(strings.TrimSpace(r.MarketSide))
	if (r.MarketSide != "bid" && r.MarketSide != "ask") || r.TargetDirection != r.MarketSide {
		return fmt.Errorf("target_direction and market_side must match and be bid or ask")
	}
	if strings.TrimSpace(r.FiatCurrency) == "" || r.FiatAmount <= 0 || r.RequiredBTCLiquidity < 0 || r.SecurityDepositBTC < 0 || r.TotalCostBTC < 0 || r.TotalCostPercent < 0 {
		return fmt.Errorf("fiat, BTC liquidity, deposit, and cost fields must be valid")
	}
	cost := r.CostInputs
	if cost.ReferencePrice <= 0 || cost.FeeTier != "economic" || cost.FundingCostAvailable || cost.ProjectedFundingFeeBTC != 0 ||
		cost.SpotTakerFeeBTC < 0 || cost.WithdrawalFeeBTC < 0 || cost.HedgeOpenFeeBTC < 0 || cost.HedgeCloseFeeBTC < 0 ||
		cost.DEXFeeBTC < 0 || cost.MinerFeeBTC < 0 || cost.ConversionFeeBTC < 0 {
		return fmt.Errorf("cost_inputs do not match the executable economic-fee policy")
	}
	wantHub := r.Settlement == "onchain_hub" || r.Settlement == "onchain_swap"
	wantLightning := r.Settlement == "onchain_swap"
	if r.NeedsHub != wantHub || r.NeedsLightning != wantLightning {
		return fmt.Errorf("needs_hub and needs_lightning do not match settlement %q", r.Settlement)
	}
	switch r.TradeMode {
	case "hedgefull_single_collateral":
		if strings.TrimSpace(r.HedgeVenue) == "" || r.HedgeNotional <= 0 || r.Leverage <= 0 {
			return fmt.Errorf("hedgefull route requires hedge venue, notional, and leverage")
		}
		if (r.MarketSide == "bid" && r.HedgeSide != "short") || (r.MarketSide == "ask" && r.HedgeSide != "long") {
			return fmt.Errorf("hedge_side does not match market_side")
		}
	case "hedgeless_double_collateral":
		if r.MarketSide != "bid" || r.RequiredBTCLiquidity <= 0 || r.HedgeVenue != "" || r.HedgeSide != "" || r.HedgeNotional != 0 {
			return fmt.Errorf("hedgeless route must be bid-side, funded with BTC liquidity, and contain no hedge leg")
		}
	default:
		return fmt.Errorf("unsupported trade_mode %q", r.TradeMode)
	}
	if (wantLightning || r.TradeMode == "hedgeless_double_collateral") && r.RequiredBTCLiquidity+1e-12 < r.BTCAmount+r.SecurityDepositBTC {
		return fmt.Errorf("required_btc_liquidity must cover btc_amount plus security_deposit_btc")
	}
	return nil
}

func (r ResolvedRoute) ValidAuthoritativeSnapshot() bool {
	return r.ValidateAuthoritativeSnapshot() == nil
}

func (r ResolvedRoute) UsesHub() bool {
	return r.Settlement == "onchain_hub" || r.Settlement == "onchain_swap"
}
func (r ResolvedRoute) UsesLightning() bool { return r.Settlement == "onchain_swap" }
func (r ResolvedRoute) IsDirect() bool      { return r.Settlement == "onchain_direct" }

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
		if stringFieldMap(m.Execution, "exchange") == "robosats" {
			if ref := stringFieldMap(m.Execution, "robosats_trade_ref"); ref != "" {
				return ref
			}
			if coord := firstNonEmpty(stringFieldMap(m.Execution, "coordinator_id"), selectedOfferString(m.SelectedOffer, "venue_coordinator_id")); coord != "" {
				return coord + ":" + id
			}
		}
		return id
	}
	if stringFieldMap(m.Execution, "exchange") == "mostro" {
		if ref := stringFieldMap(m.Execution, "mostro_trade_ref"); ref != "" {
			return ref
		}
		return stringFieldMap(m.Execution, "offer_id")
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
	case "mostro":
		return DexMostro
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

func selectedOfferString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var offer map[string]any
	if json.Unmarshal(raw, &offer) != nil {
		return ""
	}
	return stringFieldMap(offer, key)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
