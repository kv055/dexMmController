package db

import (
	"context"
	"dexTakerController/internal/exchangeconnectors/dex/robosats"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"time"
)

type RoboSatsJob struct {
	TransactionID uint
	OrderData     json.RawMessage
	State         string
	LastError     string
}

// AdmitRoboSats commits identity, reservations, order and quote binding together.
// The caller has performed read-only preflight; no network I/O runs under this lock.
func (r *TransactionRepository) AdmitRoboSats(ctx context.Context, quoteID string, order json.RawMessage, session *robosats.Session) (uint, error) {
	var id uint
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked struct {
			TransactionID *uint
			ExpiresAt     time.Time
		}
		res := tx.Raw(`SELECT transaction_id, expires_at FROM execution_quotes WHERE id=? FOR UPDATE`, quoteID).Scan(&locked)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("quote not found")
		}
		if locked.TransactionID != nil {
			id = *locked.TransactionID
			return nil
		}
		if !time.Now().Before(locked.ExpiresAt) {
			return fmt.Errorf("execution quote expired")
		}
		repo := &TransactionRepository{db: tx}
		q, err := repo.LoadExecutionQuote(ctx, quoteID)
		if err != nil {
			return err
		}
		if q.Exchange != "robosats" || q.Route == nil {
			return fmt.Errorf("RoboSats quote required")
		}
		if session == nil || session.RecoveryEncrypted == "" || session.Phase != robosats.PhaseCreated || session.Request.QuoteID != q.ID || session.Request.CoordinatorID != q.CoordinatorID || session.Request.OrderID != q.OfferID || session.Request.MaxBondSat != q.MaxBondSat || session.Request.MaxEscrowSat != q.MaxEscrowSat || session.Request.BondFeeSat != q.BondFeeSat || session.Request.EscrowFeeSat != q.EscrowFeeSat {
			return fmt.Errorf("RoboSats session does not match the confirmed quote")
		}
		// Conservatively serialize RoboSats pool commitments, including review states.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('robosats:admission',0))`).Error; err != nil {
			return err
		}
		var occupied int64
		if err := tx.Raw(`SELECT count(*) FROM robosats_trade_sessions WHERE reservation_active`).Scan(&occupied).Error; err != nil {
			return err
		}
		if occupied > 0 {
			return fmt.Errorf("RoboSats liquidity reservation already active; reconcile it first")
		}
		selected, _ := json.Marshal(map[string]any{"exchange": q.Exchange, "offer_id": q.OfferID, "venue_coordinator_id": q.CoordinatorID, "quote_id": q.ID, "max_bond_sat": q.MaxBondSat, "max_escrow_sat": q.MaxEscrowSat, "bond_fee_sat": q.BondFeeSat, "escrow_fee_sat": q.EscrowFeeSat, "direction": "bid", "native_terms": q.NativeTerms})
		meta, _ := MarshalDexOfferCreation(DexOfferMeta{SelectedOffer: selected, ResolvedRoute: q.Route, TradeflowOrder: order})
		transaction := &Transaction{Direction: "bid", Dex: DexRoboSats, DexOfferID: q.OfferID, FiatAmount: q.FiatAmount, FiatCurrency: q.Route.FiatCurrency, PaymentMethod: q.PaymentMethod, Cex: CexExchangeFromProvider(q.Route.Provider), TradeMode: q.Route.TradeMode, Status: StatusTransactionNeedsReview, DexOfferCreation: meta}
		reason := "RoboSats execution admitted; awaiting worker"
		transaction.StatusReason = &reason
		transaction.BTCRequiredLiquidity = q.Route.RequiredBTCLiquidity
		if q.Route.TradeMode == "hedgeless_double_collateral" {
			transaction.BTCPoolRefillStatus = "pending"
		}
		insert := tx.Omit("CexHedgingPosition") // nullable cex_provider, never the empty enum value
		if transaction.BTCPoolRefillStatus == "" {
			insert = insert.Omit("CexHedgingPosition", "BTCPoolRefillStatus")
		}
		if err := insert.Create(transaction).Error; err != nil {
			return err
		}
		id = transaction.ID
		session.Request.RequestID = uint64(id)
		if _, err := repo.CreateRoboSatsSession(ctx, session); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO robosats_execution_jobs(transaction_id,quote_id,order_data) VALUES(?,?,?::jsonb)`, id, q.ID, string(order)).Error; err != nil {
			return err
		}
		return repo.BindExecutionQuote(ctx, q.ID, id)
	})
	return id, err
}

// IsDurableRoboSats identifies jobs owned by the dedicated runner. Legacy
// workers must not race that runner's financial side effects.
func IsDurableRoboSats(tx *Transaction) bool {
	if tx == nil || tx.Dex != DexRoboSats {
		return false
	}
	meta, err := ParseDexOfferCreation(tx.DexOfferCreation)
	if err != nil {
		return false
	}
	var selected struct {
		QuoteID string `json:"quote_id"`
	}
	return json.Unmarshal(meta.SelectedOffer, &selected) == nil && selected.QuoteID != ""
}

func (r *TransactionRepository) RoboSatsJobs(ctx context.Context) ([]RoboSatsJob, error) {
	var jobs []RoboSatsJob
	err := r.db.WithContext(ctx).Raw(`SELECT j.transaction_id,j.order_data,j.state,COALESCE(j.last_error,'') AS last_error FROM robosats_execution_jobs j WHERE j.state <> 'done' ORDER BY j.transaction_id`).Scan(&jobs).Error
	return jobs, err
}

// CompleteRoboSats releases only a completed, externally reconciled job. The
// caller holds the execution lock and has checked robot and LND hold states.
func (r *TransactionRepository) CompleteRoboSats(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var complete bool
		if err := tx.Raw(`SELECT transaction_complete FROM transactions WHERE id=? FOR UPDATE`, id).Scan(&complete).Error; err != nil {
			return err
		}
		if !complete {
			return fmt.Errorf("RoboSats transaction is not complete")
		}
		res := tx.Exec(`UPDATE robosats_trade_sessions SET reservation_active=FALSE WHERE transaction_id=? AND phase IN ('chat_sent','needs_review')`, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("RoboSats session requires final operator reconciliation")
		}
		return tx.Exec(`UPDATE robosats_execution_jobs SET state='done',last_error=NULL,updated_at=CURRENT_TIMESTAMP WHERE transaction_id=?`, id).Error
	})
}
func (r *TransactionRepository) SetRoboSatsJob(ctx context.Context, id uint, state, reason string) error {
	return r.db.WithContext(ctx).Exec(`UPDATE robosats_execution_jobs SET state=?,last_error=NULLIF(?,''),updated_at=CURRENT_TIMESTAMP WHERE transaction_id=?`, state, reason, id).Error
}
func (r *TransactionRepository) LockRoboSatsExecution(ctx context.Context, id uint) (func(), error) {
	return r.LockRoboSats(ctx, uint64(id)+(1<<32))
}
func (r *TransactionRepository) RoboSatsSessionForRef(ctx context.Context, coordinator, order string) (*robosats.Session, error) {
	var row struct{ TransactionID uint64 }
	res := r.db.WithContext(ctx).Raw(`SELECT transaction_id FROM robosats_trade_sessions WHERE coordinator_id=? AND order_id=? AND reservation_active ORDER BY id DESC LIMIT 1`, coordinator, order).Scan(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if row.TransactionID == 0 {
		return nil, fmt.Errorf("robosats: no pinned trade identity")
	}
	return r.LoadRoboSatsSession(ctx, row.TransactionID)
}
