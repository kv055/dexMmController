package db

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Account is a shared provider/account row owned by dexUnifiedApi migrations.
type Account struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Provider        string    `json:"provider"`
	Email           string    `json:"email"`
	Password        string    `json:"password"`
	Phone           string    `json:"phone"`
	LastHealthCheck time.Time `json:"last_health_check"`
	IsHealthy       bool      `json:"is_healthy"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (Account) TableName() string {
	return "accounts"
}

// APIKey is a shared account API-key row owned by dexUnifiedApi migrations.
type APIKey struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	AccountID  uint      `json:"account_id"`
	Name       string    `json:"name"`
	APIKey     string    `gorm:"column:api_key" json:"api_key"`
	APISecret  string    `gorm:"column:api_secret" json:"api_secret"`
	Passphrase string    `json:"passphrase"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (APIKey) TableName() string {
	return "api_keys"
}

// CexExchange represents a supported centralized exchange
type CexExchange string

const (
	CexBinance  CexExchange = "Binance"
	CexKraken   CexExchange = "Kraken"
	CexCoinbase CexExchange = "Coinbase"
	CexBybit    CexExchange = "Bybit"
	CexOKX      CexExchange = "OKX"
	CexBitfinex CexExchange = "Bitfinex"
	CexGemini   CexExchange = "Gemini"
	CexBitStamp CexExchange = "Bitstamp"
)

// // CexHedgingPositionType represents the type of hedging position on a CEX
// type CexHedgingPositionType string

// const (
// 	HedgingLong  CexHedgingPositionType = "Long"
// 	HedgingShort CexHedgingPositionType = "Short"
// 	HedgingNone  CexHedgingPositionType = "None"
// )

// DexPlatform represents a supported decentralized exchange
type DexPlatform string

const (
	DexBisq     DexPlatform = "Bisq"
	DexHodlHodl DexPlatform = "HodlHodl"
	DexRoboSats DexPlatform = "RoboSats"
)

// TransactionStatus represents the lifecycle state of a transaction
type TransactionStatus string

const (
	StatusFiatToCexInitiated           TransactionStatus = "FiatToCexInitiated"
	StatusFiatToCexArrived             TransactionStatus = "FiatToCexArrived"
	StatusCexTradeSuccessful           TransactionStatus = "CexTradeSucessfull"
	StatusCexCryptoWithdrawalInitiated TransactionStatus = "CexCryptoWithdrawalInitiated"
	StatusCexCryptoWithdrawalArrived   TransactionStatus = "CexCryptoWithdrawalArrived"
	StatusDexPositionOpen              TransactionStatus = "DexPositionOpen"
	StatusDexPositionTaken             TransactionStatus = "DexPositionTaken"
	StatusDexFiatHasArrived            TransactionStatus = "DexFiatHasArrived"
	StatusDexPositionReleased          TransactionStatus = "DexPositionReleased"
)

// Transaction represents a crypto trading transaction record
type Transaction struct {
	ID uint `gorm:"primaryKey" json:"id"`

	// Basic transaction info
	Direction          string            `gorm:"index" json:"direction"` // "BUY" or "SELL"
	FiatAmount         float64           `json:"fiat_amount"`
	FiatCurrency       string            `json:"fiat_currency"`
	Cex                CexExchange       `gorm:"index" json:"cex"`
	CexHedgingPosition CexExchange       `json:"cex_hedging_position"`
	Dex                DexPlatform       `gorm:"index" json:"dex"`
	Status             TransactionStatus `gorm:"index" json:"status"`

	// CEX transaction details
	CexTransactionDetails   json.RawMessage `json:"cex_transaction_details" gorm:"type:jsonb"`
	CexBitcoinAmount        float64         `json:"cex_bitcoin_amount"`
	CexTransactionTimestamp time.Time       `json:"cex_transaction_timestamp"`
	CexTransactionFee       float64         `json:"cex_transaction_fee"`

	// CEX withdrawal details
	CexToDexWithdrawalTxDetails json.RawMessage `json:"cex_to_dex_withdrawal_tx_details" gorm:"type:jsonb"`
	CexWithdrawalFee            float64         `json:"cex_withdrawal_fee"`
	DexBtcAddress               string          `json:"dex_btc_address"` // on-chain address the CEX withdrawal is sent to

	// CEX counter position details
	CexCounterPositionOpenDetails  json.RawMessage `json:"cex_counter_position_open_details" gorm:"type:jsonb"`
	CexCounterPositionCloseDetails json.RawMessage `json:"cex_counter_position_close_details" gorm:"type:jsonb"`
	CexCounterPositionFee          float64         `json:"cex_counter_position_fee"`
	// CexHedgingPosition             CexHedgingPositionType `json:"cex_hedging_position"`

	// DEX details
	DexOfferCreation json.RawMessage `json:"dex_offer_creation" gorm:"type:jsonb"`
	DexOfferTaken    time.Time       `json:"dex_offer_taken"`
	DexFiatArrival   json.RawMessage `json:"dex_fiat_arrival" gorm:"type:jsonb"`
	DexBtcRelease    time.Time       `json:"dex_btc_release"`
	DexFees          float64         `json:"dex_fees"`

	// Fiat to CEX transfer
	TransferFiatToCexStartTime   time.Time `json:"transfer_time_fiat_to_cex"`
	TransferFiatToCexArrivalTime time.Time `json:"fiat_at_cex_arrival"`

	// True if everything completed successfully (
	// trade executed, crypto withdrawn, DEX position opened and taken, fiat arrived from DEX, position released and fiat arrived back at CEX)
	TransactionComplete bool `json:"transaction_complete"`

	// Operational costs
	OpenClawLlmInferenceCost float64 `json:"open_claw_llm_inference_cost"`
	VpsHostingCost           float64 `json:"vps_hosting_cost"`

	// Transaction analytics
	TotalTransactionPnl                      float64 `json:"total_transaction_pnl"`
	TotalTransactionTimeElapsed              int64   `json:"total_transaction_time_elapsed"` // seconds
	TotalTransactionVolatilityInBtcPrice     float64 `json:"total_transaction_volatility_in_btc_price"`
	PremiumOverCexPriceToBreakEvenPrediction float64 `json:"premium_over_cex_price_to_break_even_prediction"`

	// Timestamps
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName specifies the table name for this model
func (Transaction) TableName() string {
	return "transactions"
}
