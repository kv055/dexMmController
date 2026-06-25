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

// Worker advances bid-side tradeflow part 2: confirm CEX→LND withdrawal, take offer, relay to DEX.
type Worker struct {
	repo      *db.TransactionRepository
	cex       *cexaccount.Service
	btc       *bitcoin.NetworkDataFetcher
	taker     *dexaccount.Taker
	lnd       dexaccount.LNDRelay
	satPerVb  int64
}

// NewWorker creates a dexrelay worker.
func NewWorker(
	repo *db.TransactionRepository,
	cex *cexaccount.Service,
	btc *bitcoin.NetworkDataFetcher,
	taker *dexaccount.Taker,
) *Worker {
	return &Worker{
		repo:     repo,
		cex:      cex,
		btc:      btc,
		taker:    taker,
		satPerVb: dexaccount.DefaultSatPerVbyte(),
	}
}

// SetLND wires the live LND client used for on-chain/LN relay. Safe to call after wallet init.
func (w *Worker) SetLND(client dexaccount.LNDRelay) {
	w.lnd = client
}

// RunPeriodic polls Initiated and Arrived BUY transactions on interval until ctx is cancelled.
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
	txs, err := w.repo.GetTransactionsByStatuses(statuses, "BUY")
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

func (w *Worker) confirmWithdrawal(ctx context.Context, tx *db.Transaction) error {
	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return err
	}
	if meta.CexWithdrawal == nil || meta.CexWithdrawal.WithdrawalID == "" {
		return fmt.Errorf("missing cex_withdrawal metadata")
	}

	wd := meta.CexWithdrawal
	status, err := w.cex.GetWithdrawalStatus(ctx, wd.Provider, wd.WithdrawalID, wd.ClientRequestID)
	if err != nil {
		return fmt.Errorf("withdrawal status: %w", err)
	}

	confirmations := 0
	if status.TxHash != "" {
		wd.TxHash = status.TxHash
		if w.btc != nil {
			if chainTx, txErr := w.btc.FetchTransaction(ctx, status.TxHash); txErr == nil {
				confirmations = chainTx.Confirmations
				wd.Confirmations = confirmations
			}
		}
	}

	if !db.WithdrawalConfirmationsMet(confirmations) {
		if w.btc != nil && meta.LNDReceiveAddress != "" {
			bal, balErr := w.btc.FetchAddressBalance(ctx, meta.LNDReceiveAddress)
			if balErr == nil && bal >= tx.CexBitcoinAmount*0.99 {
				confirmations = 1
				wd.Confirmations = confirmations
			}
		}
	}
	if !db.WithdrawalConfirmationsMet(confirmations) {
		return nil
	}

	meta.CexWithdrawal = wd
	raw, err := db.MarshalDexOfferCreation(meta)
	if err != nil {
		return err
	}
	tx.DexOfferCreation = raw
	tx.Status = db.StatusCexCryptoWithdrawalArrived
	return w.repo.UpdateTransaction(tx)
}

func (w *Worker) relayToDEX(ctx context.Context, tx *db.Transaction) error {
	if db.HasRelayCompleted(tx.CexToDexWithdrawalTxDetails) {
		return nil
	}
	if w.lnd == nil {
		return fmt.Errorf("LND client is not configured")
	}
	if w.taker == nil {
		return fmt.Errorf("DEX taker is not configured")
	}

	meta, err := db.ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return err
	}

	offerID := meta.OfferID()
	exchange := meta.Exchange()
	if offerID == "" {
		offerID = strings.TrimSpace(stringFromSelectedOffer(meta.SelectedOffer, "offer_id"))
	}
	if exchange == "" {
		exchange = strings.TrimSpace(stringFromSelectedOffer(meta.SelectedOffer, "exchange"))
	}
	if exchange == "" {
		switch tx.Dex {
		case db.DexHodlHodl:
			exchange = "hodlhodl"
		case db.DexBisq:
			exchange = "bisq"
		case db.DexRoboSats:
			exchange = "robosats"
		default:
			exchange = strings.ToLower(string(tx.Dex))
		}
	}
	if offerID == "" {
		return fmt.Errorf("no offer_id in DexOfferCreation")
	}

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

	amountSat := dexaccount.BTCToSats(tx.CexBitcoinAmount)
	if amountSat <= 0 {
		return fmt.Errorf("invalid relay amount")
	}

	var relayDetails db.CexToDexRelayDetails
	relayDetails.AmountSat = amountSat
	relayDetails.SatPerVbyte = w.satPerVb
	relayDetails.SentAt = time.Now().UTC()

	switch {
	case depositAddr != "":
		txid, sendErr := w.lnd.SendOnChain(ctx, depositAddr, amountSat, w.satPerVb)
		if sendErr != nil {
			return fmt.Errorf("LND send on-chain: %w", sendErr)
		}
		relayDetails.TxID = txid
		relayDetails.DepositAddress = depositAddr
		relayDetails.PaymentMethod = "onchain"
		tx.DexBtcAddress = depositAddr
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
