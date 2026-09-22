package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrBisqQuoteNotFound = errors.New("bisq execution quote not found")

func (r *TransactionRepository) GetBisqPaymentAccountMapping(ctx context.Context, method, currency string) (*BisqPaymentAccountMapping, error) {
	method = strings.TrimSpace(method)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if method == "" || currency == "" {
		return nil, nil
	}
	var rows []BisqPaymentAccountMapping
	err := r.db.WithContext(ctx).
		Table("bisq_payment_account_mappings AS m").Select("m.*").
		Joins("JOIN payment_methods AS p ON lower(p.name) = lower(m.payment_method_name) AND p.account_id = m.local_account_id AND p.is_active").
		Where("lower(m.payment_method_name) = lower(?) AND upper(m.currency) = ?", method, currency).
		Limit(2).Find(&rows).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load bisq payment account mapping: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("ambiguous Bisq payment account mapping for %s/%s", method, currency)
	}
	return &rows[0], nil
}

func (r *TransactionRepository) ListBisqPaymentAccountMappings(ctx context.Context) ([]BisqPaymentAccountMapping, error) {
	var rows []BisqPaymentAccountMapping
	if err := r.db.WithContext(ctx).Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list bisq payment account mappings: %w", err)
	}
	return rows, nil
}

func (r *TransactionRepository) UpsertBisqPaymentAccountMapping(ctx context.Context, mapping BisqPaymentAccountMapping) (*BisqPaymentAccountMapping, error) {
	now := time.Now().UTC()
	mapping.PaymentMethodName = strings.TrimSpace(mapping.PaymentMethodName)
	mapping.Currency = strings.ToUpper(strings.TrimSpace(mapping.Currency))
	mapping.BisqPaymentAccountID = strings.TrimSpace(mapping.BisqPaymentAccountID)
	if mapping.LocalAccountID <= 0 || mapping.PaymentMethodName == "" || mapping.Currency == "" || mapping.BisqPaymentAccountID == "" {
		return nil, fmt.Errorf("bisq payment account mapping is incomplete")
	}
	mapping.UpdatedAt = now
	if mapping.CreatedAt.IsZero() {
		mapping.CreatedAt = now
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "local_account_id"}, {Name: "payment_method_name"}, {Name: "currency"}},
		DoUpdates: clause.AssignmentColumns([]string{"bisq_payment_account_id", "updated_at"}),
	}).Create(&mapping).Error; err != nil {
		return nil, fmt.Errorf("upsert bisq payment account mapping: %w", err)
	}
	return r.GetBisqPaymentAccountMapping(ctx, mapping.PaymentMethodName, mapping.Currency)
}

func (r *TransactionRepository) ActiveBisqFundingDestination(ctx context.Context) (*BisqFundingDestination, error) {
	var row BisqFundingDestination
	err := r.db.WithContext(ctx).Where("is_active").Order("id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load bisq funding destination: %w", err)
	}
	return &row, nil
}

func (r *TransactionRepository) SetActiveBisqFundingDestination(ctx context.Context, address string) (*BisqFundingDestination, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, fmt.Errorf("bisq funding address is required")
	}
	now := time.Now().UTC()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&BisqFundingDestination{}).Where("is_active").Update("is_active", false).Error; err != nil {
			return err
		}
		row := BisqFundingDestination{Address: address, IsActive: true, CreatedAt: now, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "address"}},
			DoUpdates: clause.Assignments(map[string]any{"is_active": true, "updated_at": now}),
		}).Create(&row).Error
	})
	if err != nil {
		return nil, fmt.Errorf("set bisq funding destination: %w", err)
	}
	return r.ActiveBisqFundingDestination(ctx)
}

func (r *TransactionRepository) CreateBisqExecutionQuote(ctx context.Context, quote *BisqExecutionQuote) error {
	if quote == nil {
		return fmt.Errorf("bisq execution quote is required")
	}
	if err := r.db.WithContext(ctx).Create(quote).Error; err != nil {
		return fmt.Errorf("create bisq execution quote: %w", err)
	}
	return nil
}

func (r *TransactionRepository) GetBisqExecutionQuote(ctx context.Context, id string) (*BisqExecutionQuote, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrBisqQuoteNotFound
	}
	var row BisqExecutionQuote
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrBisqQuoteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load bisq execution quote: %w", err)
	}
	return &row, nil
}

func (r *TransactionRepository) GetBisqExecutionQuoteByIdempotency(ctx context.Context, key string) (*BisqExecutionQuote, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	var row BisqExecutionQuote
	err := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load bisq execution quote by idempotency: %w", err)
	}
	return &row, nil
}
