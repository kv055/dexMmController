package dexoffer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/dex/bisq"
	"dexMmController/internal/exchanges/dex/hodlhodl"
	"dexMmController/internal/exchanges/dex/robosats"
)

const (
	depositPollInterval = 3 * time.Second
	depositPollTimeout  = 2 * time.Minute
	defaultSatPerVbyte  = 2
)

// LNDRelay sends on-chain BTC or pays a lightning invoice.
type LNDRelay interface {
	SendOnChain(ctx context.Context, toAddress string, amountSat, satPerVbyte int64) (string, error)
	PayInvoice(ctx context.Context, paymentRequest string, feeLimitSat int64) (paymentHash string, err error)
}

// Taker accepts DEX offers and resolves per-offer deposit destinations.
type Taker struct {
	repo     *db.TransactionRepository
	bisq     *bisq.Client
	hodlhodl *hodlhodl.Client
	robosats *robosats.Client
}

// NewTaker creates a Taker backed by configured DEX clients.
func NewTaker(
	repo *db.TransactionRepository,
	bisqClient *bisq.Client,
	hodlhodlClient *hodlhodl.Client,
	robosatsClient *robosats.Client,
) *Taker {
	return &Taker{
		repo:     repo,
		bisq:     bisqClient,
		hodlhodl: hodlhodlClient,
		robosats: robosatsClient,
	}
}

// OfferInput identifies an offer to take.
type OfferInput struct {
	Exchange   string
	OfferID    string
	FiatAmount float64
	BTCAmount  float64
}

// TakeResult is the outcome of TakeOffer plus optional deposit destination.
type TakeResult struct {
	Execution      map[string]any
	DepositAddress string
	LNInvoice      string
}

// Take accepts the offer on the given DEX and returns execution metadata.
func (t *Taker) Take(ctx context.Context, in OfferInput) (*TakeResult, error) {
	offerID := strings.TrimSpace(in.OfferID)
	if offerID == "" {
		return nil, fmt.Errorf("dexoffer: offer_id is required")
	}

	switch db.DexPlatformFromSlug(in.Exchange) {
	case db.DexHodlHodl:
		return t.takeHodlHodl(ctx, offerID, in.FiatAmount)
	case db.DexBisq:
		return t.takeBisq(ctx, offerID, in.BTCAmount)
	case db.DexRoboSats:
		return t.takeRoboSats(ctx, offerID)
	default:
		return nil, fmt.Errorf("dexoffer: unsupported exchange %q", in.Exchange)
	}
}

// ResolveDeposit polls trade/position APIs until an on-chain address or LN invoice is available.
func (t *Taker) ResolveDeposit(ctx context.Context, exchange string, execution map[string]any) (address, invoice string, err error) {
	if execution == nil {
		return "", "", fmt.Errorf("dexoffer: execution payload is required")
	}
	if addr := stringField(execution, "escrow_address"); addr != "" {
		return addr, "", nil
	}
	if addr := stringField(execution, "deposit_address"); addr != "" {
		return addr, "", nil
	}
	if inv := stringField(execution, "invoice"); inv != "" {
		return "", inv, nil
	}

	deadline := time.Now().Add(depositPollTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}

		switch db.DexPlatformFromSlug(exchange) {
		case db.DexRoboSats:
			address, invoice, err = t.pollRoboSatsDeposit(ctx, execution)
		case db.DexBisq:
			address, invoice, err = t.pollBisqDeposit(ctx, execution)
		default:
			return "", "", nil
		}
		if err != nil {
			return "", "", err
		}
		if address != "" || invoice != "" {
			return address, invoice, nil
		}

		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(depositPollInterval):
		}
	}
	return "", "", fmt.Errorf("dexoffer: deposit destination not available for %s after %s", exchange, depositPollTimeout)
}

func (t *Taker) takeHodlHodl(ctx context.Context, offerID string, fiatAmount float64) (*TakeResult, error) {
	if t.hodlhodl == nil {
		return nil, fmt.Errorf("hodlhodl client is not configured")
	}
	fiatStr := strconv.FormatFloat(fiatAmount, 'f', 2, 64)
	escrowAddress, contract, err := t.hodlhodl.TakeOffer(ctx, offerID, fiatStr)
	if err != nil {
		return nil, err
	}
	return &TakeResult{
		Execution: map[string]any{
			"exchange":       "hodlhodl",
			"offer_id":       offerID,
			"fiat_amount":    fiatStr,
			"escrow_address": escrowAddress,
			"contract_id":    contract.ID,
			"contract":       contract,
		},
		DepositAddress: escrowAddress,
	}, nil
}

func (t *Taker) takeBisq(ctx context.Context, offerID string, btcAmount float64) (*TakeResult, error) {
	if t.bisq == nil {
		return nil, fmt.Errorf("bisq client is not configured")
	}
	params := bisq.TakeOfferParams{AmountSats: btcToSats(btcAmount)}
	trade, err := t.bisq.TakeOffer(ctx, offerID, params)
	if err != nil {
		return nil, err
	}
	return &TakeResult{
		Execution: map[string]any{
			"exchange":       "bisq",
			"offer_id":       offerID,
			"trade_id":       trade.TradeID,
			"trade":          trade,
			"requested_sats": params.AmountSats,
		},
	}, nil
}

func (t *Taker) takeRoboSats(ctx context.Context, offerID string) (*TakeResult, error) {
	if t.robosats == nil {
		return nil, fmt.Errorf("robosats client is not configured")
	}
	client := t.robosats
	if token, err := t.latestRoboSatsToken(ctx); err != nil {
		log.Printf("dexoffer: failed to load Robosats token from database; using configured client token: %v", err)
	} else if token != "" {
		client = t.robosats.WithAuthToken(token)
	}

	position, err := client.TakeOffer(ctx, offerID)
	if err != nil {
		return nil, err
	}
	exec := map[string]any{
		"exchange":    "robosats",
		"offer_id":    offerID,
		"position_id": position.ID,
		"position":    position,
	}
	addr, inv := extractRoboSatsDeposit(position)
	return &TakeResult{
		Execution:      exec,
		DepositAddress: addr,
		LNInvoice:      inv,
	}, nil
}

func (t *Taker) pollRoboSatsDeposit(ctx context.Context, execution map[string]any) (string, string, error) {
	positionID := stringField(execution, "position_id")
	if positionID == "" {
		return "", "", fmt.Errorf("robosats: position_id missing from execution")
	}
	client := t.robosats
	if token, err := t.latestRoboSatsToken(ctx); err == nil && token != "" {
		client = t.robosats.WithAuthToken(token)
	}
	position, err := client.GetPosition(ctx, positionID)
	if err != nil {
		return "", "", err
	}
	addr, inv := extractRoboSatsDeposit(position)
	return addr, inv, nil
}

func (t *Taker) pollBisqDeposit(ctx context.Context, execution map[string]any) (string, string, error) {
	tradeID := stringField(execution, "trade_id")
	if tradeID == "" {
		return "", "", fmt.Errorf("bisq: trade_id missing from execution")
	}
	trade, err := t.bisq.GetPosition(ctx, tradeID)
	if err != nil {
		return "", "", err
	}
	_ = trade
	return "", "", nil
}

func (t *Taker) latestRoboSatsToken(ctx context.Context) (string, error) {
	if t.repo == nil {
		return "", nil
	}
	keys, err := t.repo.GetActiveAPIKeysForProvider(ctx, "Robosats")
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if token := strings.TrimSpace(key.APIKey); token != "" {
			return token, nil
		}
	}
	return "", nil
}

func extractRoboSatsDeposit(position *robosats.Position) (address, invoice string) {
	if position == nil {
		return "", ""
	}
	if len(position.Raw) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(position.Raw, &raw); err == nil {
			for _, key := range []string{"bond_invoice", "invoice", "bolt11", "ln_invoice", "payout_invoice"} {
				if inv := stringField(raw, key); strings.HasPrefix(strings.ToLower(inv), "ln") || strings.HasPrefix(inv, "lntb") {
					return "", inv
				}
			}
			for _, key := range []string{"escrow_address", "deposit_address", "address", "bond_address"} {
				if addr := stringField(raw, key); looksLikeOnChainAddress(addr) {
					return addr, ""
				}
			}
		}
	}
	return "", ""
}

func looksLikeOnChainAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	return strings.HasPrefix(addr, "bc1") || strings.HasPrefix(addr, "1") || strings.HasPrefix(addr, "3") || strings.HasPrefix(addr, "tb1")
}

func btcToSats(amount float64) uint64 {
	if amount <= 0 {
		return 0
	}
	return uint64(amount * 100_000_000)
}

func stringField(m map[string]any, key string) string {
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
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

// DefaultSatPerVbyte is the conservative on-chain fee rate used for LND→DEX relay.
func DefaultSatPerVbyte() int64 {
	return defaultSatPerVbyte
}
