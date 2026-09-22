package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrBisqConflict  = errors.New("Bisq execution conflict")
	ErrBisqLeaseLost = errors.New("Bisq executor lease lost")
	ErrBisqExpired   = errors.New("Bisq execution expired")
)

type BisqExecutionRun struct {
	TransactionID uint `gorm:"primaryKey"`
	QuoteID       string
	RequestKey    string
	Version       int
	Stage         string
	DeadlineAt    time.Time
	ReviewReason  string
	UpdatedAt     time.Time
}

func (BisqExecutionRun) TableName() string { return "bisq_execution_runs" }

type BisqAction struct {
	TransactionID uint            `gorm:"primaryKey" json:"transaction_id"`
	Kind          string          `gorm:"primaryKey" json:"kind"`
	ExternalKey   string          `json:"external_key"`
	Request       json.RawMessage `gorm:"type:jsonb" json:"request"`
	State         string          `json:"state"`
	Receipt       json.RawMessage `gorm:"type:jsonb" json:"receipt,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (BisqAction) TableName() string { return "bisq_execution_actions" }

type BisqLease struct {
	TransactionID uint `gorm:"primaryKey"`
	Owner         string
	Fence         int64
	LeaseUntil    time.Time
}

func (BisqLease) TableName() string { return "bisq_execution_leases" }

// AcceptBisqQuote commits the quote, transaction and financial reservations together.
// The callback is pure: it must not call a venue or write any database state.
func (r *TransactionRepository) AcceptBisqQuote(ctx context.Context, quoteID, key string, build func(*BisqExecutionQuote, time.Time) (*Transaction, error)) (*Transaction, error) {
	var result Transaction
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Acceptance is rare; serialize it to make cross-quote resource arbitration
		// and idempotency deterministic, including concurrent use of different keys.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext('bisq-v2-accept'))").Error; err != nil {
			return err
		}
		var run BisqExecutionRun
		err := tx.Where("request_key = ? OR quote_id = ?", key, quoteID).First(&run).Error
		if err == nil {
			if run.RequestKey != key || run.QuoteID != quoteID {
				return ErrBisqConflict
			}
			return tx.First(&result, run.TransactionID).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var quote BisqExecutionQuote
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&quote, "id = ?", quoteID).Error; err != nil {
			return err
		}
		var now time.Time
		if err := tx.Raw("SELECT clock_timestamp()").Scan(&now).Error; err != nil {
			return err
		}
		if quote.AcceptedAt != nil || quote.TransactionID != nil {
			return ErrBisqConflict
		}
		if !now.Before(quote.ExpiresAt) {
			return ErrBisqExpired
		}
		var legacy int64
		if err := tx.Model(&Transaction{}).
			Where("dex = ? AND transaction_complete IS NOT TRUE", DexBisq).
			Where("dex_offer_creation->'bisq_execution' IS NOT NULL AND COALESCE(dex_offer_creation->'bisq_execution'->>'version', '') <> '2'").
			Where("status NOT IN ?", []TransactionStatus{StatusDexPositionTaken, StatusDexFiatHasArrived, StatusDexPositionReleased}).
			Count(&legacy).Error; err != nil {
			return err
		}
		if legacy != 0 {
			return fmt.Errorf("%w: an older Bisq execution requires explicit reconciliation", ErrBisqConflict)
		}
		resources := []string{"bisq-v1"}
		if quote.TradeMode == "hedgeless_double_collateral" {
			resources = append(resources, "lnd-pool")
			var active int64
			if err := tx.Model(&Transaction{}).Where("trade_mode = ? AND btc_pool_refill_status IS DISTINCT FROM ?", quote.TradeMode, "restored").Count(&active).Error; err != nil {
				return err
			}
			if active != 0 {
				return fmt.Errorf("%w: LND pool is already reserved", ErrBisqConflict)
			}
		}
		var occupied int64
		if err := tx.Table("bisq_execution_reservations").Where("resource IN ?", resources).Count(&occupied).Error; err != nil {
			return err
		}
		if occupied != 0 {
			return fmt.Errorf("%w: Bisq execution is already active", ErrBisqConflict)
		}
		row, err := build(&quote, now)
		if err != nil {
			return err
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		run = BisqExecutionRun{TransactionID: row.ID, QuoteID: quote.ID, RequestKey: key, Version: 2, Stage: BisqStageAccepted, DeadlineAt: now.Add(30 * time.Minute)}
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		for _, resource := range resources {
			if err := tx.Exec("INSERT INTO bisq_execution_reservations(resource, transaction_id) VALUES (?, ?)", resource, row.ID).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&quote).Updates(map[string]any{"idempotency_key": key, "transaction_id": row.ID, "accepted_at": now, "deadline_at": run.DeadlineAt}).Error; err != nil {
			return err
		}
		result = *row
		return nil
	})
	return &result, err
}

func (r *TransactionRepository) ClaimBisqRun(ctx context.Context, id uint, owner string) (*BisqLease, error) {
	var lease BisqLease
	err := r.db.WithContext(ctx).Raw(`INSERT INTO bisq_execution_leases(transaction_id,owner,fence,lease_until)
 VALUES (?, ?::uuid, 1, clock_timestamp()+interval '2 minutes')
 ON CONFLICT(transaction_id) DO UPDATE SET owner=EXCLUDED.owner,
 fence=bisq_execution_leases.fence+1, lease_until=EXCLUDED.lease_until
 WHERE bisq_execution_leases.lease_until <= clock_timestamp()
 RETURNING *`, id, owner).Scan(&lease).Error
	if err != nil {
		return nil, err
	}
	if lease.TransactionID == 0 {
		return nil, ErrBisqLeaseLost
	}
	return &lease, nil
}

func (r *TransactionRepository) RenewBisqLease(ctx context.Context, l BisqLease) error {
	res := r.db.WithContext(ctx).Exec(`UPDATE bisq_execution_leases SET lease_until=clock_timestamp()+interval '2 minutes'
 WHERE transaction_id=? AND owner=?::uuid AND fence=? AND lease_until>clock_timestamp()`, l.TransactionID, l.Owner, l.Fence)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrBisqLeaseLost
	}
	return nil
}
func (r *TransactionRepository) ReleaseBisqLease(ctx context.Context, l BisqLease) error {
	// Keep the row so fences never go backwards.
	return r.db.WithContext(ctx).Exec(`UPDATE bisq_execution_leases SET lease_until=clock_timestamp()
 WHERE transaction_id=? AND owner=?::uuid AND fence=?`, l.TransactionID, l.Owner, l.Fence).Error
}

func lockBisqLease(tx *gorm.DB, l BisqLease) error {
	var current BisqLease
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("transaction_id=? AND owner=?::uuid AND fence=? AND lease_until>clock_timestamp()", l.TransactionID, l.Owner, l.Fence).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrBisqLeaseLost
		}
		return err
	}
	return nil
}

func (r *TransactionRepository) BeginBisqAction(ctx context.Context, l BisqLease, action BisqAction) (bool, error) {
	var request map[string]json.RawMessage
	if action.Kind == "" || action.ExternalKey == "" || json.Unmarshal(action.Request, &request) != nil || request == nil {
		return false, errors.New("action kind, external key and JSON object request required")
	}
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockBisqLease(tx, l); err != nil {
			return err
		}
		var run BisqExecutionRun
		if err := tx.First(&run, l.TransactionID).Error; err != nil {
			return err
		}
		if run.Stage == BisqStageReviewRequired || run.Stage == BisqStageTaken {
			return ErrBisqConflict
		}
		var allowed bool
		if err := tx.Raw("SELECT ?::timestamptz > clock_timestamp()", run.DeadlineAt).Scan(&allowed).Error; err != nil {
			return err
		}
		if !allowed {
			return ErrBisqExpired
		}
		action.TransactionID = l.TransactionID
		action.State = "intent"
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&action)
		created = res.RowsAffected == 1
		return res.Error
	})
	return created, err
}

func (r *TransactionRepository) CompleteBisqAction(ctx context.Context, l BisqLease, kind string, receipt json.RawMessage) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(receipt, &object) != nil || object == nil {
		return errors.New("valid action receipt required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockBisqLease(tx, l); err != nil {
			return err
		}
		res := tx.Model(&BisqAction{}).Where("transaction_id=? AND kind=? AND state='intent'", l.TransactionID, kind).Updates(map[string]any{"state": "confirmed", "receipt": receipt, "updated_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrBisqConflict
		}
		return nil
	})
}

func (r *TransactionRepository) GetBisqRun(ctx context.Context, id uint) (*BisqExecutionRun, []BisqAction, error) {
	var run BisqExecutionRun
	var actions []BisqAction
	if err := r.db.WithContext(ctx).First(&run, id).Error; err != nil {
		return nil, nil, err
	}
	err := r.db.WithContext(ctx).Where("transaction_id=?", id).Order("created_at, kind").Find(&actions).Error
	return &run, actions, err
}
func (r *TransactionRepository) ListBisqRuns(ctx context.Context) ([]BisqExecutionRun, error) {
	var rows []BisqExecutionRun
	err := r.db.WithContext(ctx).Where("stage NOT IN ?", []string{BisqStageReviewRequired, BisqStageTaken}).Order("transaction_id").Find(&rows).Error
	return rows, err
}

// Park updates only review fields on the CURRENT row. Financial evidence survives.
func (r *TransactionRepository) ParkBisqRun(ctx context.Context, l BisqLease, reason string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockBisqLease(tx, l); err != nil {
			return err
		}
		if err := tx.Model(&BisqExecutionRun{}).Where("transaction_id=?", l.TransactionID).Updates(map[string]any{"stage": BisqStageReviewRequired, "review_reason": reason, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		var row Transaction
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, l.TransactionID).Error; err != nil {
			return err
		}
		meta, err := ParseDexOfferCreation(row.DexOfferCreation)
		if err != nil {
			return err
		}
		if meta.BisqExecution == nil {
			return errors.New("missing Bisq execution metadata")
		}
		row.SetStatus(StatusTransactionNeedsReview, reason)
		// Select named columns, never Save a stale snapshot.
		if err := tx.Model(&row).Updates(map[string]any{"status": row.Status, "status_reason": row.StatusReason,
			"dex_offer_creation": gorm.Expr("jsonb_set(dex_offer_creation, '{bisq_execution,stage}', to_jsonb(?::text))", BisqStageReviewRequired),
		}).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&BisqAction{}).Where("transaction_id=?", row.ID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.Exec("DELETE FROM bisq_execution_reservations WHERE transaction_id=?", row.ID).Error; err != nil {
				return err
			}
			if row.TradeMode == "hedgeless_double_collateral" {
				return tx.Model(&row).Update("btc_pool_refill_status", "restored").Error
			}
		}
		return nil
	})
}

func BisqManaged(tx *Transaction) bool {
	if tx == nil {
		return false
	}
	meta, err := ParseDexOfferCreation(tx.DexOfferCreation)
	return err == nil && meta.BisqExecution != nil
}

func (r *TransactionRepository) FinishBisqRun(ctx context.Context, l BisqLease) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockBisqLease(tx, l); err != nil {
			return err
		}
		var action BisqAction
		if err := tx.Where("transaction_id=? AND kind='take' AND state='confirmed'", l.TransactionID).First(&action).Error; err != nil {
			return err
		}
		var execution map[string]any
		if err := json.Unmarshal(action.Receipt, &execution); err != nil {
			return err
		}
		tradeID, _ := execution["trade_id"].(string)
		fiat, _ := execution["fiat_amount"].(float64)
		if tradeID == "" || fiat <= 0 {
			return errors.New("take receipt lacks trade identity or actual fiat amount")
		}
		var row Transaction
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, l.TransactionID).Error; err != nil {
			return err
		}
		meta, err := ParseDexOfferCreation(row.DexOfferCreation)
		if err != nil {
			return err
		}
		if meta.BisqExecution == nil {
			return errors.New("missing Bisq plan")
		}
		if err := tx.Model(&row).Updates(map[string]any{"status": StatusDexPositionTaken,
			"dex_offer_creation": gorm.Expr("jsonb_set(jsonb_set(dex_offer_creation, '{bisq_execution,stage}', to_jsonb(?::text)), '{execution}', ?::jsonb)", BisqStageTaken, string(action.Receipt)),
			"fiat_amount":        fiat, "dex_offer_taken": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if err := tx.Model(&BisqExecutionRun{}).Where("transaction_id=?", row.ID).Updates(map[string]any{"stage": BisqStageTaken, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		// The pool remains reserved until its replacement and collateral are accounted
		// for. Releasing a worker lease never releases this financial reservation.
		return tx.Exec("DELETE FROM bisq_execution_reservations WHERE transaction_id=? AND resource='bisq-v1'", row.ID).Error
	})
}
