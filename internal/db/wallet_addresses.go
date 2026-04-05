package db

import "time"

// WalletAddress stores reusable wallet addresses in cleartext for operational use.
type WalletAddress struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Label     string    `gorm:"uniqueIndex;not null" json:"label"`
	Address   string    `gorm:"uniqueIndex;not null" json:"address"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (WalletAddress) TableName() string {
	return "wallet_addresses"
}
