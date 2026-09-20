package db

import (
	"encoding/json"
	"time"
)

const (
	BisqExecutionVersion = 2

	BisqStageAccepted         = "accepted"
	BisqStageFundingInitiated = "funding_initiated"
	BisqStageFundingObserved  = "funding_observed"
	BisqStageTakeInitiated    = "take_initiated"
	BisqStageTaken            = "taken"
	BisqStageReviewRequired   = "review_required"
)

// BisqExecutionPlan is the versioned metadata persisted on DexOfferCreation for
// an accepted Bisq v1 bid. Detailed stages live here so existing transaction
// statuses stay compatible.
type BisqExecutionPlan struct {
	Version             int       `json:"version"`
	QuoteID             string    `json:"quote_id"`
	Stage               string    `json:"stage"`
	PaymentAccountID    string    `json:"payment_account_id"`
	LocalAccountID      int64     `json:"local_account_id,omitempty"`
	FundingAddress      string    `json:"funding_address"`
	TradeAmountSats     uint64    `json:"trade_amount_sats"`
	SecurityDepositSats uint64    `json:"security_deposit_sats"`
	TakerFeeSats        uint64    `json:"taker_fee_sats"`
	MiningFeeSats       uint64    `json:"mining_fee_sats"`
	TotalToPaySats      uint64    `json:"total_to_pay_sats"`
	Volume              string    `json:"volume"`
	VolumeAsLong        int64     `json:"volume_as_long"`
	FiatAmount          float64   `json:"fiat_amount"`
	MaxWalletDebitSats  uint64    `json:"max_wallet_debit_sats"`
	MaxCexFiatSpend     float64   `json:"max_cex_fiat_spend"`
	MaxFundingFeeSats   uint64    `json:"max_funding_fee_sats"`
	MinFiatProceeds     float64   `json:"min_fiat_proceeds"`
	MinVolumeAsLong     int64     `json:"min_volume_as_long"`
	DeadlineAt          time.Time `json:"deadline_at"`
	IdempotencyKey      string    `json:"idempotency_key,omitempty"`
	Intents             []string  `json:"intents,omitempty"`
}

// BisqPaymentAccountMapping binds a local receiving account to a daemon payment account.
type BisqPaymentAccountMapping struct {
	ID                    int64     `gorm:"primaryKey" json:"id"`
	LocalAccountID        int64     `json:"local_account_id"`
	PaymentMethodName     string    `json:"payment_method_name"`
	Currency              string    `json:"currency"`
	BisqPaymentAccountID  string    `json:"bisq_payment_account_id"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func (BisqPaymentAccountMapping) TableName() string { return "bisq_payment_account_mappings" }

// BisqFundingDestination is the operator-designated Bisq wallet address.
type BisqFundingDestination struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Address   string    `json:"address"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (BisqFundingDestination) TableName() string { return "bisq_funding_destinations" }

// BisqExecutionQuote is a stored pre-accept quote.
type BisqExecutionQuote struct {
	ID                string          `gorm:"primaryKey;type:uuid" json:"id"`
	OfferID           string          `json:"offer_id"`
	TradeMode         string          `json:"trade_mode"`
	PaymentAccountID  string          `json:"payment_account_id"`
	LocalAccountID    *int64          `json:"local_account_id,omitempty"`
	Quote             json.RawMessage `gorm:"type:jsonb" json:"quote"`
	Limits            json.RawMessage `gorm:"type:jsonb" json:"limits"`
	ResolvedRoute     json.RawMessage `gorm:"type:jsonb" json:"resolved_route"`
	SelectedOffer     json.RawMessage `gorm:"type:jsonb" json:"selected_offer,omitempty"`
	PaymentMethod     string          `json:"payment_method"`
	FundingAddress    string          `json:"funding_address"`
	IdempotencyKey    *string         `json:"idempotency_key,omitempty"`
	TransactionID     *uint           `json:"transaction_id,omitempty"`
	ExpiresAt         time.Time       `json:"expires_at"`
	AcceptedAt        *time.Time      `json:"accepted_at,omitempty"`
	DeadlineAt        *time.Time      `json:"deadline_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
}

func (BisqExecutionQuote) TableName() string { return "bisq_execution_quotes" }

// BisqSpendingLimits are the operator-confirmed caps stored with a quote.
type BisqSpendingLimits struct {
	MaxFundingFeeSats  uint64  `json:"max_funding_fee_sats"`
	MaxCexFiatSpend    float64 `json:"max_cex_fiat_spend"`
	MaxWalletDebitSats uint64  `json:"max_wallet_debit_sats"`
	MinFiatProceeds    float64 `json:"min_fiat_proceeds"`
}
