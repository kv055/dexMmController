package dexaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/bisq2"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
	"dexMmController/internal/exchangeconnectors/dex/mostro"
	"dexMmController/internal/exchangeconnectors/dex/robosats"
)

const (
	depositPollInterval = 3 * time.Second
	depositPollTimeout  = 2 * time.Minute
	defaultSatPerVbyte  = 2
)

// LNDRelay sends on-chain BTC, pays a lightning invoice, and reads the confirmed
// balance of a single hub address (to detect an incoming CEX withdrawal).
type LNDRelay interface {
	SendOnChain(ctx context.Context, toAddress string, amountSat, satPerVbyte int64) (string, error)
	PayInvoice(ctx context.Context, paymentRequest string, feeLimitSat int64) (paymentHash string, err error)
	ConfirmedBalanceForAddress(ctx context.Context, address string) (int64, error)
}

// Taker accepts DEX offers and resolves per-offer deposit destinations.
type Taker struct {
	repo     *db.TransactionRepository
	bisq     *bisq.Client
	bisq2    *bisq2.Client
	hodlhodl *hodlhodl.Client
	robosats *robosats.Client
	mostro   MostroTaker
}

type MostroTaker interface {
	Take(context.Context, mostro.TakeRequest) (*mostro.TakeResult, error)
	Ready() error
	Check(context.Context, mostro.TakeRequest) error
}

// NewTaker creates a Taker backed by configured DEX clients.
func NewTaker(
	repo *db.TransactionRepository,
	bisqClient *bisq.Client,
	hodlhodlClient *hodlhodl.Client,
	robosatsClient *robosats.Client,
	mostroClient MostroTaker,
) *Taker {
	return &Taker{
		repo:     repo,
		bisq:     bisqClient,
		hodlhodl: hodlhodlClient,
		robosats: robosatsClient,
		mostro:   mostroClient,
	}
}

// WithBisq2 attaches the Bisq Easy REST client used to take bids and resolve
// the buyer's bitcoinPaymentData.
func (t *Taker) WithBisq2(client *bisq2.Client) *Taker {
	t.bisq2 = client
	return t
}

// OfferInput identifies an offer to take.
type OfferInput struct {
	Exchange        string
	OfferID         string
	FiatAmount      float64
	BTCAmount       float64
	RequestID       uint64
	VenueNodePubKey string
	ProtocolVersion int
	SourceEventID   string
	FiatCurrency    string
	PaymentMethod   string
	MaxPaymentSat   int64
	MaxBondSat      int64
	RoutingFeeSat   int64
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
		return nil, fmt.Errorf("dexaccount: offer_id is required")
	}

	switch db.DexPlatformFromSlug(in.Exchange) {
	case db.DexHodlHodl:
		return t.takeHodlHodl(ctx, offerID, in.FiatAmount)
	case db.DexBisq:
		return t.takeBisq(ctx, offerID, in.BTCAmount)
	case db.DexBisq2:
		return t.takeBisq2(ctx, in)
	case db.DexRoboSats:
		return t.takeRoboSats(ctx, offerID)
	case db.DexMostro:
		return t.takeMostro(ctx, in)
	default:
		return nil, fmt.Errorf("dexaccount: unsupported exchange %q", in.Exchange)
	}
}

func (t *Taker) MostroReady() error {
	if t.mostro == nil {
		return fmt.Errorf("mostro client is not configured")
	}
	return t.mostro.Ready()
}
func (t *Taker) CheckMostro(ctx context.Context, in OfferInput) error {
	if err := t.MostroReady(); err != nil {
		return err
	}
	if math.IsNaN(in.FiatAmount) || math.IsInf(in.FiatAmount, 0) || in.FiatAmount <= 0 || in.FiatAmount >= float64(math.MaxInt64) || math.Trunc(in.FiatAmount) != in.FiatAmount {
		return fmt.Errorf("mostro: fiat amount must be a positive integer")
	}
	return t.mostro.Check(ctx, mostro.TakeRequest{RequestID: 1, Node: in.VenueNodePubKey, OrderID: in.OfferID, SourceEventID: in.SourceEventID, Protocol: in.ProtocolVersion, FiatAmount: int64(in.FiatAmount), FiatCurrency: in.FiatCurrency, PaymentMethod: in.PaymentMethod, MaxEscrowSat: in.MaxPaymentSat, MaxBondSat: in.MaxBondSat, RoutingFeeSat: in.RoutingFeeSat})
}
func (t *Taker) takeMostro(ctx context.Context, in OfferInput) (*TakeResult, error) {
	if err := t.MostroReady(); err != nil {
		return nil, err
	}
	if math.IsNaN(in.FiatAmount) || math.IsInf(in.FiatAmount, 0) || in.FiatAmount <= 0 || in.FiatAmount >= float64(math.MaxInt64) || math.Trunc(in.FiatAmount) != in.FiatAmount {
		return nil, fmt.Errorf("mostro: selected fiat amount must be a positive integer")
	}
	result, err := t.mostro.Take(ctx, mostro.TakeRequest{RequestID: in.RequestID, Node: in.VenueNodePubKey, OrderID: in.OfferID, SourceEventID: in.SourceEventID, Protocol: in.ProtocolVersion, FiatAmount: int64(in.FiatAmount), FiatCurrency: in.FiatCurrency, PaymentMethod: in.PaymentMethod, MaxEscrowSat: in.MaxPaymentSat, MaxBondSat: in.MaxBondSat, RoutingFeeSat: in.RoutingFeeSat})
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Funded {
		return nil, fmt.Errorf("mostro: escrow acceptance has not been confirmed")
	}
	return &TakeResult{Execution: map[string]any{"exchange": "mostro", "offer_id": in.OfferID, "request_id": in.RequestID, "mostro_session_id": result.SessionID, "mostro_trade_ref": fmt.Sprintf("%s:%s:%d", in.VenueNodePubKey, in.OfferID, in.RequestID), "mostro_funded": true, "payment_hash": result.PaymentHash, "amount_sat": result.AmountSat, "venue_node_pubkey": in.VenueNodePubKey}}, nil
}

// ResolveDeposit polls non-direct venue APIs until an on-chain address or LN
// invoice is available. Bisq is pre-funded through its integrated wallet and
// therefore has no per-trade destination to resolve.
func (t *Taker) ResolveDeposit(ctx context.Context, exchange string, execution map[string]any) (address, invoice string, err error) {
	if db.DexPlatformFromSlug(exchange) == db.DexMostro {
		return "", "", fmt.Errorf("mostro: funding must be reconciled by the Mostro coordinator")
	}
	if execution == nil {
		return "", "", fmt.Errorf("dexaccount: execution payload is required")
	}
	if addr := StringField(execution, "escrow_address"); addr != "" {
		return addr, "", nil
	}
	if addr := StringField(execution, "deposit_address"); addr != "" {
		return addr, "", nil
	}
	if inv := StringField(execution, "invoice"); inv != "" {
		return "", inv, nil
	}
	platform := db.DexPlatformFromSlug(exchange)
	if platform == db.DexBisq {
		return "", "", nil
	}
	if platform == db.DexBisq2 {
		return t.pollBisq2Deposit(ctx, execution)
	}
	if platform != db.DexRoboSats {
		return "", "", nil
	}

	deadline := time.Now().Add(depositPollTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}

		address, invoice, err = t.pollRoboSatsDeposit(ctx, execution)
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
	return "", "", fmt.Errorf("dexaccount: deposit destination not available for %s after %s", exchange, depositPollTimeout)
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
	params := bisq.TakeOfferParams{AmountSats: uint64(BTCToSats(btcAmount))}
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

func (t *Taker) takeBisq2(ctx context.Context, in OfferInput) (*TakeResult, error) {
	if t.bisq2 == nil {
		return nil, fmt.Errorf("bisq2 client is not configured")
	}
	quoteUnits := bisq2.QuoteAmountUnits(in.FiatAmount)
	baseSats := BTCToSats(in.BTCAmount)
	if quoteUnits <= 0 {
		return nil, fmt.Errorf("bisq2: fiat amount is required to take an offer")
	}
	if baseSats <= 0 {
		return nil, fmt.Errorf("bisq2: btc amount is required to take an offer")
	}

	offer, err := t.bisq2.GetOffer(ctx, in.FiatCurrency, in.OfferID)
	if err != nil {
		return nil, err
	}
	bitcoinMethod := bisq2.PickBitcoinRail(offer.BitcoinRails)
	fiatMethod := bisq2.MatchFiatRail(in.PaymentMethod, offer.FiatRails)
	if fiatMethod == "" {
		return nil, fmt.Errorf("bisq2: no fiat payment method selected for offer %s", in.OfferID)
	}

	taken, err := t.bisq2.TakeOffer(ctx, in.OfferID, bisq2.TakeOfferParams{
		BaseSideAmount:       baseSats,
		QuoteSideAmount:      quoteUnits,
		BitcoinPaymentMethod: bitcoinMethod,
		FiatPaymentMethod:    fiatMethod,
	})
	if err != nil {
		return nil, err
	}

	if accountData := t.bisq2PaymentAccountData(ctx, in.PaymentMethod); accountData != "" {
		if err := t.bisq2.SendSellerPaymentAccount(ctx, taken.TradeID, accountData); err != nil {
			log.Printf("dexaccount: bisq2 send seller payment account for trade %s: %v", taken.TradeID, err)
		}
	}

	return &TakeResult{
		Execution: map[string]any{
			"exchange":               "bisq2",
			"offer_id":               in.OfferID,
			"trade_id":               taken.TradeID,
			"requested_sats":         baseSats,
			"quote_side_amount":      quoteUnits,
			"bitcoin_payment_method": taken.BitcoinPaymentMethod,
			"fiat_payment_method":    taken.FiatPaymentMethod,
		},
	}, nil
}

func (t *Taker) bisq2PaymentAccountData(ctx context.Context, paymentMethod string) string {
	if t.repo == nil {
		return ""
	}
	vars, err := t.repo.GetChatTemplateVariables(ctx, paymentMethod)
	if err != nil {
		log.Printf("dexaccount: bisq2 payment account variables: %v", err)
		return ""
	}
	return formatBisq2PaymentAccount(paymentMethod, vars)
}

func formatBisq2PaymentAccount(method string, vars map[string]string) string {
	if vars == nil {
		vars = map[string]string{}
	}
	var b strings.Builder
	if method = strings.TrimSpace(method); method != "" {
		b.WriteString(method)
	}
	writeLine := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(value)
	}
	writeLine("Account holder", vars["owner_name"])
	if details := strings.TrimSpace(vars["details_encrypted"]); details != "" && details != "{}" && details != "null" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(details)
	}
	writeLine("Email", vars["email"])
	writeLine("Phone", vars["phone"])
	writeLine("Payment tag", vars["payment_tag"])
	return strings.TrimSpace(b.String())
}

func (t *Taker) pollBisq2Deposit(ctx context.Context, execution map[string]any) (string, string, error) {
	if t.bisq2 == nil {
		return "", "", fmt.Errorf("bisq2 client is not configured")
	}
	tradeID := StringField(execution, "trade_id")
	if tradeID == "" {
		return "", "", fmt.Errorf("bisq2: trade_id missing from execution")
	}
	data, err := t.bisq2.WaitForBitcoinPaymentData(ctx, tradeID, bisq2.DepositPollTimeout)
	if err != nil {
		return "", "", err
	}
	addr, invoice := bisq2.ClassifyBitcoinPaymentData(data)
	if addr == "" && invoice == "" {
		return "", "", fmt.Errorf("bisq2: bitcoin payment data for trade %s is empty", tradeID)
	}
	return addr, invoice, nil
}

// ReportBtcSent notifies a venue that we have sent the BTC leg. Bisq Easy needs
// SELLER_CONFIRM_BTC_SENT with the on-chain txid (or LN proof).
func (t *Taker) ReportBtcSent(ctx context.Context, exchange, tradeID, proof string) error {
	if db.DexPlatformFromSlug(exchange) != db.DexBisq2 {
		return nil
	}
	if t.bisq2 == nil {
		return fmt.Errorf("bisq2 client is not configured")
	}
	if err := t.bisq2.ConfirmBtcSent(ctx, tradeID, proof); err != nil {
		return fmt.Errorf("bisq2: confirm btc sent: %w", err)
	}
	return nil
}

func (t *Taker) takeRoboSats(ctx context.Context, offerID string) (*TakeResult, error) {
	if t.robosats == nil {
		return nil, fmt.Errorf("robosats client is not configured")
	}
	client := t.robosats
	if token, err := t.latestRoboSatsToken(ctx); err != nil {
		log.Printf("dexaccount: failed to refresh Robosats token from database; retaining current database-loaded client token: %v", err)
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
	positionID := StringField(execution, "position_id")
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
				if inv := StringField(raw, key); strings.HasPrefix(strings.ToLower(inv), "ln") || strings.HasPrefix(inv, "lntb") {
					return "", inv
				}
			}
			for _, key := range []string{"escrow_address", "deposit_address", "address", "bond_address"} {
				if addr := StringField(raw, key); LooksLikeOnChainAddress(addr) {
					return addr, ""
				}
			}
		}
	}
	return "", ""
}

// DefaultSatPerVbyte is the conservative on-chain fee rate used for LND→DEX relay.
func DefaultSatPerVbyte() int64 {
	return defaultSatPerVbyte
}
