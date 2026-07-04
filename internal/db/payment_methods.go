package db

import "time"

// PaymentMethod is one row of the payment_methods catalog (owned by
// dexUnifiedApi schema file 20_payment_methods.sql, seeded from
// commons/paymentmethods). It maps a canonical offer payment method to the
// operator's saved payment solution via exactly one of the three link IDs.
type PaymentMethod struct {
	// ID identifies the payment method row.
	ID int64 `gorm:"primaryKey" json:"id"`
	// Name is the canonical method name stored on offers (unique).
	Name string `json:"name"`
	// MergeRegex merges raw offer labels into this method (nullable).
	MergeRegex *string `json:"merge_regex"`
	// Category is the display category (Cash, Bank Transfer, Fiat Transmitter, Gift Cards, Crypto, Others).
	Category string `json:"category"`
	// BankAccountID links a Bank Transfer method to bank_accounts(id).
	BankAccountID *int64 `json:"bank_account_id"`
	// AccountID links a Fiat Transmitter method to accounts(id).
	AccountID *int64 `json:"account_id"`
	// WalletAddressID links a Crypto method to wallet_addresses(id).
	WalletAddressID *int64 `json:"wallet_address_id"`
	// IsActive reports whether this method may be used to take offers.
	IsActive bool `json:"is_active"`
	// IsLinked is a generated column (any link ID set); read-only.
	IsLinked bool `gorm:"->" json:"is_linked"`
	// Priority ranks methods when an offer accepts several (higher wins).
	Priority int `json:"priority"`
	// MatchOrder is the canonicalization scan order (lower first).
	MatchOrder int `json:"match_order"`
	// CreatedAt is the row creation time.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last modification time.
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the database table name for the model.
func (PaymentMethod) TableName() string {
	return "payment_methods"
}
