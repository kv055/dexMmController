package db

import (
	"time"

	"commons/paymentmethods"
)

// PaymentMethod is one row of the payment_methods catalog (owned by
// dexUnifiedApi schema file 20_payment_methods.sql, seeded from
// commons/paymentmethods). It maps a canonical offer payment method to the
// operator's saved payment account via account_id.
type PaymentMethod struct {
	// ID identifies the payment method row.
	ID int64 `gorm:"primaryKey" json:"id"`
	// Name is the canonical method name stored on offers (unique).
	Name string `json:"name"`
	// MergeRegex merges raw offer labels into this method (nullable).
	MergeRegex *string `json:"merge_regex"`
	// AliasList holds operator-managed exact text aliases for canonicalization.
	AliasList paymentmethods.TextArray `gorm:"type:text[]" json:"alias_list"`
	// Sources lists the DEX offer slugs this method is associated with.
	Sources paymentmethods.TextArray `gorm:"type:text[]" json:"sources"`
	// Currencies lists the ISO fiat codes this method supports.
	Currencies paymentmethods.TextArray `gorm:"type:text[]" json:"currencies"`
	// Category is the display category (Cash, Bank, Fiat Transmitter, Gift Cards, Crypto, Others).
	Category string `json:"category"`
	// AccountID links a Bank or Fiat Transmitter method to accounts(id).
	AccountID *int64 `json:"account_id"`
	// IsActive reports whether this method may be used to take offers.
	IsActive bool `json:"is_active"`
	// IsLinked is a generated column (account_id set); read-only.
	IsLinked bool `gorm:"->" json:"is_linked"`
	// Priority ranks methods when an offer accepts several (higher wins).
	Priority int `json:"priority"`
	// MatchOrder is the canonicalization scan order (lower first).
	MatchOrder int `json:"match_order"`
	// Popularity counts newly inserted offers that list this method.
	Popularity int `json:"popularity"`
	// CreatedAt is the row creation time.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last modification time.
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the database table name for the model.
func (PaymentMethod) TableName() string {
	return "payment_methods"
}
