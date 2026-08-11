package dexrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"dexMmController/internal/bitcoin"
	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
)

// Worker advances bid-side tradeflow part 2: confirm the CEX withdrawal arrived, take
// the DEX offer, and (on hub/swap routes) relay the BTC on to the DEX destination.
//
// The route decides where the BTC lands and how it is forwarded:
//   - Direct (bisq v1): the CEX withdraws straight into our bisq wallet (the
//     destination); we watch that address and the bisq daemon funds the trade itself —
//     no LND relay.
//   - Hub (bisq2/hodlhodl/peach): the CEX withdraws to our LND hub; we watch the hub,
//     then forward on-chain to the counterparty's per-trade destination.
//   - Swap (robosats): the CEX withdraws to our LND hub; we watch the hub, then pay the
//     offer's hold invoice over Lightning.
type Worker struct {
	repo     txStore
	cex      *cexaccount.Service
	btc      *bitcoin.NetworkDataFetcher
	taker    offerTaker
	resolver paymentResolver
	lnd      dexaccount.LNDRelay
	satPerVb int64
}

// txStore is the transaction-row access the worker needs (satisfied by
// *db.TransactionRepository).
type txStore interface {
	GetTransactionsByStatuses(statuses []db.TransactionStatus, direction string) ([]db.Transaction, error)
	UpdateTransaction(tx *db.Transaction) error
}

// offerTaker takes a DEX offer and resolves its deposit destination (satisfied by
// *dexaccount.Taker).
type offerTaker interface {
	Take(ctx context.Context, in dexaccount.OfferInput) (*dexaccount.TakeResult, error)
	ResolveDeposit(ctx context.Context, exchange string, execution map[string]any) (string, string, error)
}

// paymentResolver fetches a structured venue's counterparty fiat details (satisfied by
// *dexaccount.PaymentResolver). Chat venues (Robosats) are handled by the healthcheck, not here,
// because their details arrive over a conversation rather than in the trade object. Optional: a
// nil resolver disables the fetch.
type paymentResolver interface {
	IsStructuredVenue(exchange string) bool
	FetchCounterparty(ctx context.Context, exchange string, execution map[string]any) (json.RawMessage, error)
}

// NewWorker creates a dexrelay worker. resolver may be nil to disable the counterparty
// fiat-detail fetch/share steps.
func NewWorker(
	repo *db.TransactionRepository,
	cex *cexaccount.Service,
	btc *bitcoin.NetworkDataFetcher,
	taker *dexaccount.Taker,
	resolver *dexaccount.PaymentResolver,
) *Worker {
	w := &Worker{
		repo:     repo,
		cex:      cex,
		btc:      btc,
		taker:    taker,
		satPerVb: dexaccount.DefaultSatPerVbyte(),
	}
	// Assign through the interface only when non-nil to avoid a typed-nil interface that
	// would defeat the nil guards in the fetch/share helpers.
	if resolver != nil {
		w.resolver = resolver
	}
	return w
}

// SetLND wires the live LND client used for on-chain/LN relay. Safe to call after wallet init.
func (w *Worker) SetLND(client dexaccount.LNDRelay) {
	w.lnd = client
}

// RunPeriodic polls initiated and arrived buy transactions on interval until ctx is cancelled.
func (w *Worker) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		w.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	statuses := []db.TransactionStatus{
		db.StatusCexCryptoWithdrawalInitiated,
		db.StatusCexCryptoWithdrawalArrived,
	}
	txs, err := w.repo.GetTransactionsByStatuses(statuses, "buy")
	if err != nil {
		log.Printf("dexrelay: list transactions: %v", err)
		return
	}
	for i := range txs {
		tx := &txs[i]
		if err := w.process(ctx, tx); err != nil {
			log.Printf("dexrelay: tx %d (%s): %v", tx.ID, tx.Status, err)
		}
	}
}

func (w *Worker) process(ctx context.Context, tx *db.Transaction) error {
	switch tx.Status {
	case db.StatusCexCryptoWithdrawalInitiated:
		return w.confirmWithdrawal(ctx, tx)
	case db.StatusCexCryptoWithdrawalArrived:
		return w.relayToDEX(ctx, tx)
	default:
		return nil
	}
}

// confirmWithdrawal advances Initiated → Arrived once the required BTC have landed at
// the address the route watches: the LND hub (hub/swap) or the bisq wallet (direct).
func (w *Worker) confirmWithdrawal(ctx context.Context, tx *db.Transaction) error {
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return err
	}
	if meta.ResolvedRoute == nil || !meta.ResolvedRoute.ValidForExecution() {
		return fmt.Errorf("transaction has no valid resolved_route")
	}

	var arrived bool
	if meta.ResolvedRoute.UsesHub() {
		arrived, err = w.hubFundsArrived(ctx, tx, &meta)
	} else {
		arrived, err = w.destinationFundsArrived(ctx, tx)
	}
	if err != nil {
		return err
	}
	if !arrived {
		return nil
	}

	raw, err := db.MarshalDexOfferCreation(meta)
	if err != nil {
		return err
	}
	tx.DexOfferCreation = raw
	tx.Status = db.StatusCexCryptoWithdrawalArrived
	return w.repo.UpdateTransaction(tx)
}

// hubFundsArrived checks our LND hub for the incoming CEX withdrawal. It primarily
// reads the hub address balance from our own wallet, and cross-checks the CEX
// withdrawal status / on-chain confirmations when available.
func (w *Worker) hubFundsArrived(ctx context.Context, tx *db.Transaction, meta *db.DexOfferMeta) (bool, error) {
	// Cross-check via the CEX withdrawal status + on-chain confirmations (informational;
	// keeps wd.TxHash/Confirmations fresh and confirms via the chain when reachable).
	confirmations := 0
	if meta.CexWithdrawal != nil && meta.CexWithdrawal.WithdrawalID != "" {
		wd := meta.CexWithdrawal
		if status, err := w.cex.GetWithdrawalStatus(ctx, wd.Provider, wd.WithdrawalID, wd.ClientRequestID); err == nil {
			if status.TxHash != "" {
				wd.TxHash = status.TxHash
				if w.btc != nil {
					if chainTx, txErr := w.btc.FetchTransaction(ctx, status.TxHash); txErr == nil {
						confirmations = chainTx.Confirmations
						wd.Confirmations = confirmations
					}
				}
			}
			if status.FeeBTC > 0 {
				tx.CexWithdrawalFee = status.FeeBTC
			}
		}
		meta.CexWithdrawal = wd
	}
	if db.WithdrawalConfirmationsMet(confirmations) {
		return true, nil
	}

	// Primary: read the hub address balance from our own LND wallet.
	hub := strings.TrimSpace(tx.HubAddress)
	if hub == "" {
		hub = strings.TrimSpace(meta.LNDReceiveAddress)
	}
	if w.lnd == nil || hub == "" {
		return false, nil
	}
	balSat, err := w.lnd.ConfirmedBalanceForAddress(ctx, hub)
	if err != nil {
		return false, fmt.Errorf("hub balance: %w", err)
	}
	return balSat >= requiredSat(tx.CexBitcoinAmount), nil
}

// destinationFundsArrived checks the bisq wallet (direct route) for the incoming CEX
// withdrawal via mempool.space, since we don't drive that wallet's API here.
func (w *Worker) destinationFundsArrived(ctx context.Context, tx *db.Transaction) (bool, error) {
	dest := strings.TrimSpace(tx.DestinationAddress)
	if w.btc == nil || dest == "" {
		return false, nil
	}
	bal, err := w.btc.FetchAddressBalance(ctx, dest)
	if err != nil {
		return false, fmt.Errorf("destination balance: %w", err)
	}
	return bal >= tx.CexBitcoinAmount*0.99, nil
}

// relayToDEX takes the offer and, per route, forwards the BTC to the DEX: on-chain to
// the per-trade destination (hub), over Lightning to the hold invoice (swap), or not at
// all (direct — the bisq wallet already holds the funds and funds the trade itself).
func (w *Worker) relayToDEX(ctx context.Context, tx *db.Transaction) error {
	if db.HasRelayCompleted(tx.CexToDexWithdrawalTxDetails) {
		return nil
	}
	if w.taker == nil {
		return fmt.Errorf("DEX taker is not configured")
	}

	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return err
	}

	exchange := deriveExchange(tx, meta)
	if meta.ResolvedRoute == nil || !meta.ResolvedRoute.ValidForExecution() {
		return fmt.Errorf("transaction has no valid resolved_route")
	}
	offerID := meta.OfferID()
	if offerID == "" {
		offerID = strings.TrimSpace(stringFromSelectedOffer(meta.SelectedOffer, "offer_id"))
	}
	if offerID == "" {
		return fmt.Errorf("no offer_id in DexOfferCreation")
	}
	if strings.TrimSpace(tx.DexOfferID) == "" {
		tx.DexOfferID = offerID
	}

	// Take the offer (idempotent: only when no execution recorded yet).
	if meta.Execution == nil {
		takeResult, takeErr := w.taker.Take(ctx, dexaccount.OfferInput{
			Exchange:   exchange,
			OfferID:    offerID,
			FiatAmount: tx.FiatAmount,
			BTCAmount:  tx.CexBitcoinAmount,
		})
		if takeErr != nil {
			return fmt.Errorf("take offer: %w", takeErr)
		}
		meta.Execution = takeResult.Execution
		if takeResult.DepositAddress != "" {
			meta.Execution["deposit_address"] = takeResult.DepositAddress
		}
		if takeResult.LNInvoice != "" {
			meta.Execution["invoice"] = takeResult.LNInvoice
		}
		raw, marshalErr := db.MarshalDexOfferCreation(meta)
		if marshalErr != nil {
			return marshalErr
		}
		tx.DexOfferCreation = raw
		if err := w.repo.UpdateTransaction(tx); err != nil {
			return err
		}
	}

	// Structured venues (Bisq) expose the counterparty's fiat details in the trade contract
	// the moment the offer is taken, so capture them here before any BTC moves. Chat venues
	// (Robosats) are handled by the healthcheck instead — their details arrive over a
	// conversation. Best-effort: a miss is logged, never fatal to the BTC relay.
	w.populateCounterpartyPayment(ctx, tx, meta, exchange)

	relayDetails := db.CexToDexRelayDetails{
		AmountSat:   dexaccount.BTCToSats(tx.CexBitcoinAmount),
		SatPerVbyte: w.satPerVb,
		SentAt:      time.Now().UTC(),
	}

	// Direct route (bisq v1): the funds are already in our bisq wallet; the daemon funds
	// the multisig from there. Nothing to send on-chain — just record the taken offer.
	if meta.ResolvedRoute.IsDirect() {
		relayDetails.DepositAddress = strings.TrimSpace(tx.DestinationAddress)
		relayDetails.PaymentMethod = "direct"
		return w.finishRelay(tx, meta, relayDetails)
	}

	// Hub/Swap: we must forward from the LND hub. Resolve the per-trade target.
	if w.lnd == nil {
		return fmt.Errorf("LND client is not configured")
	}
	depositAddr := meta.EscrowOrDepositAddress()
	invoice := meta.LNInvoice()
	if depositAddr == "" && invoice == "" {
		var resolveErr error
		depositAddr, invoice, resolveErr = w.taker.ResolveDeposit(ctx, exchange, meta.Execution)
		if resolveErr != nil {
			return resolveErr
		}
		if depositAddr != "" {
			meta.Execution["deposit_address"] = depositAddr
		}
		if invoice != "" {
			meta.Execution["invoice"] = invoice
		}
	}

	if relayDetails.AmountSat <= 0 {
		return fmt.Errorf("invalid relay amount")
	}

	switch {
	case depositAddr != "":
		txid, sendErr := w.lnd.SendOnChain(ctx, depositAddr, relayDetails.AmountSat, w.satPerVb)
		if sendErr != nil {
			return fmt.Errorf("LND send on-chain: %w", sendErr)
		}
		relayDetails.TxID = txid
		relayDetails.DepositAddress = depositAddr
		relayDetails.PaymentMethod = "onchain"
		tx.DestinationAddress = depositAddr
	case invoice != "":
		paymentHash, payErr := w.lnd.PayInvoice(ctx, invoice, 0)
		if payErr != nil {
			return fmt.Errorf("LND pay invoice: %w", payErr)
		}
		relayDetails.PaymentHash = paymentHash
		relayDetails.Invoice = invoice
		relayDetails.PaymentMethod = "lightning"
	default:
		return fmt.Errorf("no deposit address or invoice available for relay")
	}

	return w.finishRelay(tx, meta, relayDetails)
}

// populateCounterpartyPayment fetches a structured venue's counterparty fiat details once and
// stores them on the row's counterparty_payment_info column. Chat venues are skipped (the
// healthcheck drives their conversation). Best-effort: failures (e.g. contract not ready yet)
// are logged, not fatal, so they never block the BTC relay.
func (w *Worker) populateCounterpartyPayment(ctx context.Context, tx *db.Transaction, meta db.DexOfferMeta, exchange string) {
	if w.resolver == nil || meta.Execution == nil || len(tx.CounterpartyPaymentInfo) > 0 {
		return
	}
	if !w.resolver.IsStructuredVenue(exchange) {
		return
	}
	info, err := w.resolver.FetchCounterparty(ctx, exchange, meta.Execution)
	if err != nil {
		log.Printf("dexrelay: tx %d fetch counterparty payment (%s): %v", tx.ID, exchange, err)
		return
	}
	tx.CounterpartyPaymentInfo = info
	if err := w.repo.UpdateTransaction(tx); err != nil {
		log.Printf("dexrelay: tx %d persist counterparty payment (%s): %v", tx.ID, exchange, err)
	}
}

// finishRelay persists the relay outcome and advances the row to DexPositionOpen.
func (w *Worker) finishRelay(tx *db.Transaction, meta db.DexOfferMeta, relayDetails db.CexToDexRelayDetails) error {
	relayJSON, err := json.Marshal(relayDetails)
	if err != nil {
		return err
	}
	metaRaw, err := db.MarshalDexOfferCreation(meta)
	if err != nil {
		return err
	}

	tx.CexToDexWithdrawalTxDetails = relayJSON
	tx.DexOfferCreation = metaRaw
	tx.Status = db.StatusDexPositionOpen
	tx.DexOfferTaken = time.Now().UTC()
	return w.repo.UpdateTransaction(tx)
}

// deriveExchange resolves the DEX venue slug from the offer metadata, falling back to
// the transaction's Dex column. The slug carries the bisq vs bisq2 distinction that the
// Dex column ("bisq") does not.
func deriveExchange(tx *db.Transaction, meta db.DexOfferMeta) string {
	if ex := strings.TrimSpace(meta.Exchange()); ex != "" {
		return ex
	}
	if ex := strings.TrimSpace(stringFromSelectedOffer(meta.SelectedOffer, "exchange")); ex != "" {
		return ex
	}
	switch tx.Dex {
	case db.DexHodlHodl:
		return "hodlhodl"
	case db.DexBisq:
		return "bisq"
	case db.DexRoboSats:
		return "robosats"
	default:
		return strings.ToLower(string(tx.Dex))
	}
}

// requiredSat is the minimum satoshi balance we accept as "arrived", allowing a 1%
// shortfall for the on-chain withdrawal fee deducted by the CEX.
func requiredSat(btcAmount float64) int64 {
	full := dexaccount.BTCToSats(btcAmount)
	return full * 99 / 100
}

func stringFromSelectedOffer(raw json.RawMessage, field string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return dexaccount.StringField(m, field)
}
