package db

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"dexTakerController/internal/exchangeconnectors/dex/mostro"
	"github.com/nbd-wtf/go-nostr"
	"gorm.io/gorm"
)

func (r *TransactionRepository) LockMostro(ctx context.Context, id uint64) (func(), error) {
	if id == 0 || id > math.MaxInt64 {
		return nil, fmt.Errorf("mostro: invalid transaction id")
	}
	db, err := r.db.DB()
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	// Namespace the lock by a stable string so other workers' advisory locks do
	// not accidentally share the transaction-id namespace.
	key := fmt.Sprintf("mostro:transaction:%d", id)
	var ok bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&ok); err != nil || !ok {
		_ = conn.Close()
		if err == nil {
			err = fmt.Errorf("mostro: transaction is being reconciled")
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
func (r *TransactionRepository) LoadMostroSession(ctx context.Context, id uint64) (*mostro.Session, error) {
	var row struct {
		ID                int64
		IdentityID        int64
		SessionData       []byte
		EncryptedMnemonic []byte
	}
	result := r.db.WithContext(ctx).Raw(`SELECT s.id,s.identity_id,s.session_data,i.encrypted_mnemonic FROM mostro_trade_sessions s JOIN mostro_identities i ON i.id=s.identity_id WHERE s.transaction_id=?`, id).Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	var s mostro.Session
	if err := json.Unmarshal(row.SessionData, &s); err != nil {
		return nil, fmt.Errorf("mostro: legacy/incomplete session needs manual recovery: %w", err)
	}
	if s.Request.RequestID != id || s.TradeIndex < 1 || s.Payments == nil || s.Seen == nil {
		return nil, fmt.Errorf("mostro: incomplete session requires manual recovery")
	}
	s.ID = row.ID
	s.IdentityID = row.IdentityID
	s.EncryptedMnemonic = row.EncryptedMnemonic
	return &s, nil
}
func (r *TransactionRepository) CreateMostroSession(ctx context.Context, label string, ciphertext []byte, build func([]byte, int64) (*mostro.Session, error)) (*mostro.Session, error) {
	if strings.TrimSpace(label) == "" {
		return nil, fmt.Errorf("mostro: identity label is required")
	}
	var s *mostro.Session
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(ciphertext) > 0 {
			if !strings.HasPrefix(string(ciphertext), "enc:v1:") {
				return fmt.Errorf("mostro: mnemonic must be encrypted")
			}
			if err := tx.Exec(`INSERT INTO mostro_identities(label,encrypted_mnemonic,next_trade_index) VALUES(?,?,1) ON CONFLICT(label) DO NOTHING`, label, ciphertext).Error; err != nil {
				return err
			}
		}
		var identity struct {
			ID                int64
			EncryptedMnemonic []byte
			NextTradeIndex    int64
		}
		q := tx.Raw(`SELECT id,encrypted_mnemonic,next_trade_index FROM mostro_identities WHERE label=? AND is_active=TRUE FOR UPDATE`, label).Scan(&identity)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return fmt.Errorf("mostro: active identity is not configured")
		}
		var pending int64
		if err := tx.Raw(`SELECT COUNT(*) FROM mostro_trade_sessions WHERE identity_id=? AND state NOT IN ('funded','released')`, identity.ID).Scan(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return fmt.Errorf("mostro: reconcile this identity's pending trade before allocating another key")
		}
		// Existing counters allocated by the incomplete implementation cannot be
		// reset: zero is reserved and lost old keys must not be reused.
		index := identity.NextTradeIndex
		if index < 1 {
			index = 1
		}
		if index >= 1<<31 {
			return fmt.Errorf("mostro: trade index exhausted")
		}
		var err error
		s, err = build(identity.EncryptedMnemonic, index)
		if err != nil {
			return err
		}
		if s.IdentityPubkey == "" {
			return fmt.Errorf("mostro: identity pubkey is required")
		}
		q = tx.Exec(`UPDATE mostro_identities SET identity_pubkey=? WHERE id=? AND (identity_pubkey IS NULL OR identity_pubkey=?)`, s.IdentityPubkey, identity.ID, s.IdentityPubkey)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return fmt.Errorf("mostro: identity key changed")
		}
		s.IdentityID = identity.ID
		s.TradeIndex = index
		s.EncryptedMnemonic = identity.EncryptedMnemonic
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		q = tx.Raw(`INSERT INTO mostro_trade_sessions(transaction_id,identity_id,trade_index,trade_pubkey,node_pubkey,order_id,request_id,protocol_version,state,session_data) VALUES(?,?,?,?,?,?,?,?,?,?::jsonb) RETURNING id`, s.Request.RequestID, s.IdentityID, index, s.TradePubkey, s.Request.Node, s.Request.OrderID, s.Request.RequestID, s.Request.Protocol, s.State, string(data)).Scan(&s.ID)
		if q.Error != nil {
			return q.Error
		}
		return tx.Exec(`UPDATE mostro_identities SET next_trade_index=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, index+1, identity.ID).Error
	})
	return s, err
}
func (r *TransactionRepository) SaveMostroSession(ctx context.Context, s *mostro.Session, event *nostr.Event) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Exec(`UPDATE mostro_trade_sessions SET state=?,peer_pubkey=NULLIF(?,''),last_error=NULLIF(?,''),session_data=?::jsonb,updated_at=CURRENT_TIMESTAMP WHERE id=?`, s.State, s.PeerPubkey, s.LastError, string(data), s.ID)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return fmt.Errorf("mostro: session disappeared")
		}
		for _, p := range s.Payments {
			if err := tx.Exec(`INSERT INTO mostro_payments(session_id,purpose,invoice,payment_hash,amount_sat,status) VALUES(?,?,?,?,?,?) ON CONFLICT(session_id,purpose) DO UPDATE SET payment_hash=EXCLUDED.payment_hash,status=EXCLUDED.status,updated_at=CURRENT_TIMESTAMP`, s.ID, p.Purpose, p.Invoice, p.Hash, p.AmountSat, p.Status).Error; err != nil {
				return err
			}
		}
		if event != nil {
			raw, err := json.Marshal(event)
			if err != nil {
				return err
			}
			return tx.Exec(`INSERT INTO mostro_inbox_events(event_id,session_id,node_pubkey,event_created_at,event_json,processed_at) VALUES(?,?,?,?,?::jsonb,CURRENT_TIMESTAMP) ON CONFLICT(event_id) DO NOTHING`, event.ID, s.ID, s.Request.Node, time.Unix(int64(event.CreatedAt), 0), string(raw)).Error
		}
		return nil
	})
}
func (r *TransactionRepository) MostroOutbox(ctx context.Context, sessionID int64, action string, build func() (nostr.Event, error)) (nostr.Event, error) {
	var event nostr.Event
	var row struct{ EventJSON []byte }
	q := r.db.WithContext(ctx).Raw(`SELECT event_json FROM mostro_outbox WHERE session_id=? AND action=?`, sessionID, action).Scan(&row)
	if q.Error != nil {
		return event, q.Error
	}
	if q.RowsAffected > 0 {
		err := json.Unmarshal(row.EventJSON, &event)
		return event, err
	}
	event, err := build()
	if err != nil {
		return event, err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return event, err
	}
	q = r.db.WithContext(ctx).Exec(`INSERT INTO mostro_outbox(session_id,request_id,event_id,event_json,action) SELECT id,request_id,?,?::jsonb,? FROM mostro_trade_sessions WHERE id=?`, event.ID, string(raw), action, sessionID)
	if q.Error != nil {
		return event, q.Error
	}
	if q.RowsAffected != 1 {
		return event, errors.New("mostro: missing session for outbox")
	}
	return event, nil
}
func (r *TransactionRepository) AckMostroOutbox(ctx context.Context, eventID string) error {
	return r.db.WithContext(ctx).Exec(`UPDATE mostro_outbox SET status='sent',attempts=attempts+1,updated_at=CURRENT_TIMESTAMP WHERE event_id=?`, eventID).Error
}

// AuthorizeMostroRelease enforces fiat confirmation at the database boundary,
// including direct coordinator callers and delayed outbox retries.
func (r *TransactionRepository) AuthorizeMostroRelease(ctx context.Context, id uint64) error {
	var count int64
	if err := r.db.WithContext(ctx).Model(&Transaction{}).Where("id=? AND status IN ?", id, []TransactionStatus{StatusDexFiatHasArrived, StatusDexPositionReleased}).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("mostro: confirmed fiat receipt is required to release")
	}
	return nil
}
func (r *TransactionRepository) LoadMostroChatEvents(ctx context.Context, id int64) ([]nostr.Event, error) {
	var rows []struct{ EventJSON []byte }
	if err := r.db.WithContext(ctx).Raw(`SELECT event_json FROM mostro_chat_events WHERE session_id=? ORDER BY created_at DESC,event_id LIMIT 1000`, id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]nostr.Event, 0, len(rows))
	for _, row := range rows {
		var e nostr.Event
		if err := json.Unmarshal(row.EventJSON, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}
func (r *TransactionRepository) SaveMostroChatEvent(ctx context.Context, id int64, event nostr.Event, innerID string) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(`INSERT INTO mostro_chat_events(session_id,event_id,inner_event_id,event_json,created_at) VALUES(?,?,?,?::jsonb,?) ON CONFLICT DO NOTHING`, id, event.ID, innerID, string(raw), time.Unix(int64(event.CreatedAt), 0)).Error
}
