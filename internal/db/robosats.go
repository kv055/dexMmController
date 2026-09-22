package db

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"dexMmController/internal/exchangeconnectors/dex/robosats"
	"gorm.io/gorm"
)

func (r *TransactionRepository) LockRoboSats(ctx context.Context, id uint64) (func(), error) {
	if id == 0 || id > math.MaxInt64 {
		return nil, fmt.Errorf("robosats: invalid transaction id")
	}
	db, err := r.db.DB()
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("robosats:transaction:%d", id)
	var ok bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&ok); err != nil || !ok {
		_ = conn.Close()
		if err == nil {
			err = fmt.Errorf("robosats: transaction is being reconciled")
		}
		return nil, err
	}
	return func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		err := conn.QueryRowContext(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key).Scan(&unlocked)
		if err != nil || !unlocked {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, nil
}

func (r *TransactionRepository) LoadRoboSatsSession(ctx context.Context, id uint64) (*robosats.Session, error) {
	var row struct {
		ID          int64
		SessionData []byte
	}
	result := r.db.WithContext(ctx).Raw(`SELECT id, session_data FROM robosats_trade_sessions WHERE transaction_id=?`, id).Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	var s robosats.Session
	if err := json.Unmarshal(row.SessionData, &s); err != nil {
		return nil, fmt.Errorf("robosats: incomplete session needs manual recovery: %w", err)
	}
	if s.Request.RequestID != id || s.Payments == nil {
		return nil, fmt.Errorf("robosats: incomplete session requires manual recovery")
	}
	s.ID = row.ID
	return &s, nil
}

func (r *TransactionRepository) CreateRoboSatsSession(ctx context.Context, s *robosats.Session) (*robosats.Session, error) {
	if s == nil || s.Request.RequestID == 0 {
		return nil, fmt.Errorf("robosats: session request is required")
	}
	if s.Payments == nil {
		s.Payments = map[string]*robosats.Payment{}
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, fmt.Sprintf("robosats:robot:%d:%s", s.Request.CredentialID, s.Request.CoordinatorID)).Error; err != nil {
			return err
		}
		var pending int64
		if err := tx.Raw(`SELECT COUNT(*) FROM robosats_trade_sessions WHERE credential_id=? AND coordinator_id=? AND reservation_active`,
			s.Request.CredentialID, s.Request.CoordinatorID).Scan(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return fmt.Errorf("robosats: robot already has an active trade on this coordinator")
		}
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		q := tx.Raw(`INSERT INTO robosats_trade_sessions(transaction_id, credential_id, coordinator_id, order_id, quote_id, phase, session_data)
			VALUES(?,?,?,?,?,?,?::jsonb) RETURNING id`,
			s.Request.RequestID, s.Request.CredentialID, s.Request.CoordinatorID, s.Request.OrderID, nullIfEmpty(s.Request.QuoteID), s.Phase, string(data)).Scan(&s.ID)
		return q.Error
	})
	return s, err
}

func (r *TransactionRepository) SaveRoboSatsSession(ctx context.Context, s *robosats.Session) error {
	if s == nil || s.ID == 0 {
		return fmt.Errorf("robosats: session id is required")
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE robosats_trade_sessions SET phase=?, last_error=NULLIF(?,''), session_data=?::jsonb, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
			s.Phase, s.LastError, string(data), s.ID).Error; err != nil {
			return err
		}
		for _, purpose := range []string{"bond", "escrow"} {
			p := s.Payments[purpose]
			if p == nil {
				continue
			}
			if err := tx.Exec(`INSERT INTO robosats_payments(session_id, purpose, invoice, payment_hash, amount_sat, status)
				VALUES(?,?,?,?,?,?)
				ON CONFLICT(session_id, purpose) DO UPDATE SET invoice=EXCLUDED.invoice, payment_hash=EXCLUDED.payment_hash, amount_sat=EXCLUDED.amount_sat, status=EXCLUDED.status, updated_at=CURRENT_TIMESTAMP`,
				s.ID, p.Purpose, p.Invoice, p.Hash, p.AmountSat, p.Status).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
