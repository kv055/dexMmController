package db

import "time"

// WalletAddress stores reusable wallet addresses in cleartext for operational use.
type WalletAddress struct {
	// ID identifies the wallet address.
	ID uint `gorm:"primaryKey" json:"id"`
	// Label is the label value for the wallet address.
	Label string `gorm:"uniqueIndex;not null" json:"label"`
	// Address is the wallet or peer address used by the wallet address.
	Address string `gorm:"uniqueIndex;not null" json:"address"`
	// Provider records the CEX associated with this address when it was stored
	// as a withdrawal-whitelist destination.
	Provider string `json:"provider,omitempty"`
	// Category identifies the operational purpose of the stored address.
	// Category is nullable in Postgres. A pointer ensures new general-purpose
	// on-chain addresses are stored as NULL rather than an invalid empty enum.
	Category *string `json:"category,omitempty"`
	// CreatedAt records the relevant time for the wallet address.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt records the relevant time for the wallet address.
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the database table name for the model.
func (WalletAddress) TableName() string {
	return "wallet_addresses"
}
