package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExecutionQuote is a persisted Unified API quote bound to one trade.
type ExecutionQuote struct {
	ID            string
	Exchange      string
	CoordinatorID string
	OfferID       string
	PaymentMethod string
	FiatAmount    float64
	BTCAmount     float64
	MaxBondSat    int64
	MaxEscrowSat  int64
	BondFeeSat    int64
	EscrowFeeSat  int64
	NativeTerms   json.RawMessage
	Route         *ResolvedRoute
	ExpiresAt     time.Time
	TransactionID uint
}

func (r *TransactionRepository) LoadExecutionQuote(ctx context.Context, quoteID string) (*ExecutionQuote, error) {
	quoteID = strings.TrimSpace(quoteID)
	if quoteID == "" {
		return nil, fmt.Errorf("execution quote id is required")
	}
	var row struct {
		Exchange      string
		BondFeeSat    int64
		EscrowFeeSat  int64
		NativeTerms   json.RawMessage
		CoordinatorID sql.NullString
		OfferID       string
		PaymentMethod string
		FiatAmount    float64
		BTCAmount     float64
		MaxBondSat    int64
		MaxEscrowSat  int64
		Route         []byte
		ExpiresAt     time.Time
		TransactionID sql.NullInt64
	}
	err := r.db.WithContext(ctx).Raw(`SELECT exchange, bond_fee_sat, escrow_fee_sat, native_terms, COALESCE(coordinator_id, '') AS coordinator_id, offer_id, payment_method, fiat_amount, btc_amount, max_bond_sat, max_escrow_sat, route, expires_at, transaction_id
		FROM execution_quotes WHERE id=?`, quoteID).Scan(&row).Error
	if err != nil {
		return nil, fmt.Errorf("load execution quote: %w", err)
	}
	q := &ExecutionQuote{
		Exchange: row.Exchange, BondFeeSat: row.BondFeeSat, EscrowFeeSat: row.EscrowFeeSat, NativeTerms: row.NativeTerms,
		ID: quoteID, CoordinatorID: strings.TrimSpace(row.CoordinatorID.String), OfferID: row.OfferID,
		PaymentMethod: row.PaymentMethod, FiatAmount: row.FiatAmount, BTCAmount: row.BTCAmount,
		MaxBondSat: row.MaxBondSat, MaxEscrowSat: row.MaxEscrowSat, ExpiresAt: row.ExpiresAt,
	}
	if row.Exchange == "" {
		return nil, fmt.Errorf("execution quote not found")
	}
	if len(row.Route) > 0 {
		var route ResolvedRoute
		if err := json.Unmarshal(row.Route, &route); err != nil {
			return nil, fmt.Errorf("decode execution quote route: %w", err)
		}
		q.Route = &route
	}
	if row.TransactionID.Valid && row.TransactionID.Int64 > 0 {
		q.TransactionID = uint(row.TransactionID.Int64)
	}
	return q, nil
}

func (r *TransactionRepository) BindExecutionQuote(ctx context.Context, quoteID string, transactionID uint) error {
	quoteID = strings.TrimSpace(quoteID)
	if quoteID == "" || transactionID == 0 {
		return fmt.Errorf("execution quote and transaction are required")
	}
	res := r.db.WithContext(ctx).Exec(`UPDATE execution_quotes SET transaction_id=?
		WHERE id=? AND (transaction_id IS NULL OR transaction_id=?)`, transactionID, quoteID, transactionID)
	if res.Error != nil {
		return fmt.Errorf("bind execution quote: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("execution quote is already bound to another trade")
	}
	return nil
}
