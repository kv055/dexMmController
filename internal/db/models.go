package db

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Account is a shared provider/account row owned by dexUnifiedApi schema files.
type Account struct {
	// ID identifies the account.
	ID uint `gorm:"primaryKey" json:"id"`
	// Provider is the provider value for the account.
	Provider string `json:"provider"`
	// Email is the email value for the account.
	Email string `json:"email"`
	// Password is the password value for the account.
	Password string `json:"password"`
	// Phone is the phone value for the account.
	Phone string `json:"phone"`
	// LastHealthCheck is the last health check value for the account.
	LastHealthCheck time.Time `json:"last_health_check"`
	// IsHealthy reports whether the account condition is true.
	IsHealthy bool `json:"is_healthy"`
	// CreatedAt records the relevant time for the account.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt records the relevant time for the account.
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the database table name for the model.
func (Account) TableName() string {
	return "accounts"
}

// APIKey is a shared account API-key row owned by dexUnifiedApi schema files.
type APIKey struct {
	// ID identifies the apikey.
	ID uint `gorm:"primaryKey" json:"id"`
	// AccountID is the count associated with the apikey.
	AccountID uint `json:"account_id"`
	// Name is the name value for the apikey.
	Name string `json:"name"`
	// APIKey is the apikey value for the apikey.
	APIKey string `gorm:"column:api_key" json:"api_key"`
	// APISecret is the apisecret value for the apikey.
	APISecret string `gorm:"column:api_secret" json:"api_secret"`
	// Passphrase is the passphrase value for the apikey.
	Passphrase string `json:"passphrase"`
	// IsActive reports whether the apikey condition is true.
	IsActive bool `json:"is_active"`
	// Permissions stores normalized validation or permission flags from the latest health check.
	Permissions json.RawMessage `gorm:"column:permissions;type:jsonb" json:"permissions,omitempty"`
	// PermissionsCheckedAt records when permissions were last fetched from the exchange.
	PermissionsCheckedAt *time.Time `json:"permissions_checked_at,omitempty"`
	// CreatedAt records the relevant time for the apikey.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt records the relevant time for the apikey.
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the database table name for the model.
func (APIKey) TableName() string {
	return "api_keys"
}

// CexExchange represents a supported centralized exchange
type CexExchange string

const (
	// CexBinance is the cex binance value.
	CexBinance CexExchange = "Binance"
	// CexKraken is the cex kraken value.
	CexKraken CexExchange = "Kraken"
	// CexCoinbase is the cex coinbase value.
	CexCoinbase CexExchange = "Coinbase"
	// CexBybit is the cex bybit value.
	CexBybit CexExchange = "Bybit"
	// CexOKX is the cex okx value.
	CexOKX CexExchange = "OKX"
	// CexBitfinex is the cex bitfinex value.
	CexBitfinex CexExchange = "Bitfinex"
	// CexGemini is the cex gemini value.
	CexGemini CexExchange = "Gemini"
	// CexBitStamp is the cex bit stamp value.
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
	// DexBisq is the dex bisq value.
	DexBisq DexPlatform = "Bisq"
	// DexHodlHodl is the dex hodl hodl value.
	DexHodlHodl DexPlatform = "HodlHodl"
	// DexRoboSats is the dex robo sats value.
	DexRoboSats DexPlatform = "RoboSats"
)

// TransactionStatus represents the lifecycle state of a transaction
type TransactionStatus string

const (
	// StatusFiatToCexInitiated is the status fiat to cex initiated value.
	StatusFiatToCexInitiated TransactionStatus = "FiatToCexInitiated"
	// StatusFiatToCexArrived is the status fiat to cex arrived value.
	StatusFiatToCexArrived TransactionStatus = "FiatToCexArrived"
	// StatusCexTradeflowStarted marks a bid tradeflow row created before the spot
	// buy, so the irreversible CEX steps are recoverable rather than only existing
	// in an in-flight request.
	StatusCexTradeflowStarted TransactionStatus = "CexTradeflowStarted"
	// StatusHedgeFailedNeedsReview marks a tradeflow that bought spot but could not
	// open (or confirm) the hedge after retries — the account holds unhedged BTC and
	// needs manual reconciliation. Terminal until a human intervenes; happy-path
	// workers must skip it.
	StatusHedgeFailedNeedsReview TransactionStatus = "HedgeFailedNeedsReview"
	// StatusCexTradeflowNeedsReview marks a tradeflow that died at CexTradeflowStarted
	// with the spot-buy outcome unrecorded/unconfirmable. Recovery cannot safely
	// re-buy (MarketBuyBTC has no idempotency key), so a human must reconcile.
	// Terminal until a human intervenes; happy-path workers must skip it.
	StatusCexTradeflowNeedsReview TransactionStatus = "CexTradeflowNeedsReview"
	// StatusCexTradeSuccessful is the status cex trade successful value.
	StatusCexTradeSuccessful TransactionStatus = "CexTradeSucessfull"
	// StatusCexCryptoWithdrawalInitiated is the status cex crypto withdrawal initiated value.
	StatusCexCryptoWithdrawalInitiated TransactionStatus = "CexCryptoWithdrawalInitiated"
	// StatusCexCryptoWithdrawalArrived is the status cex crypto withdrawal arrived value.
	StatusCexCryptoWithdrawalArrived TransactionStatus = "CexCryptoWithdrawalArrived"
	// StatusDexPositionOpen is the status dex position open value.
	StatusDexPositionOpen TransactionStatus = "DexPositionOpen"
	// StatusDexPositionTaken is the status dex position taken value.
	StatusDexPositionTaken TransactionStatus = "DexPositionTaken"
	// StatusDexFiatHasArrived is the status dex fiat has arrived value.
	StatusDexFiatHasArrived TransactionStatus = "DexFiatHasArrived"
	// StatusDexPositionReleased is the status dex position released value.
	StatusDexPositionReleased TransactionStatus = "DexPositionReleased"
)

// Transaction represents a crypto trading transaction record
type Transaction struct {
	// ID identifies the transaction.
	ID uint `gorm:"primaryKey" json:"id"`

	// Direction is the direction value for the transaction.
	Direction string `gorm:"index" json:"direction"` // "BUY" or "SELL"
	// FiatAmount is a fiat-denominated fiat amount value.
	FiatAmount float64 `json:"fiat_amount"`
	// FiatCurrency is a fiat-denominated fiat currency value.
	FiatCurrency string `json:"fiat_currency"`
	// Cex is the cex value for the transaction.
	Cex CexExchange `gorm:"index" json:"cex"`
	// CexHedgingPosition is the cex hedging position value for the transaction.
	CexHedgingPosition CexExchange `json:"cex_hedging_position"`
	// Dex is the dex value for the transaction.
	Dex DexPlatform `gorm:"index" json:"dex"`
	// Status is the current status of the transaction.
	Status TransactionStatus `gorm:"index" json:"status"`

	// CexTransactionDetails is the cex transaction details value for the transaction.
	CexTransactionDetails json.RawMessage `json:"cex_transaction_details" gorm:"type:jsonb"`
	// CexBitcoinAmount is the cex bitcoin amount value for the transaction.
	CexBitcoinAmount float64 `json:"cex_bitcoin_amount"`
	// CexTransactionTimestamp records the relevant time for the transaction.
	CexTransactionTimestamp time.Time `json:"cex_transaction_timestamp"`
	// CexTransactionFee is the fee value reported for the transaction.
	CexTransactionFee float64 `json:"cex_transaction_fee"`

	// CexToDexWithdrawalTxDetails preserves additional source data for the transaction.
	CexToDexWithdrawalTxDetails json.RawMessage `json:"cex_to_dex_withdrawal_tx_details" gorm:"type:jsonb"`
	// CexWithdrawalFee is the fee value reported for the transaction.
	CexWithdrawalFee float64 `json:"cex_withdrawal_fee"`
	// DexBtcAddress is a BTC-denominated dex btc address value.
	DexBtcAddress string `json:"dex_btc_address"` // on-chain address the CEX withdrawal is sent to

	// CexCounterPositionOpenDetails is the count associated with the transaction.
	CexCounterPositionOpenDetails json.RawMessage `json:"cex_counter_position_open_details" gorm:"type:jsonb"`
	// CexCounterPositionCloseDetails is the count associated with the transaction.
	CexCounterPositionCloseDetails json.RawMessage `json:"cex_counter_position_close_details" gorm:"type:jsonb"`
	// CexCounterPositionFee is the count associated with the transaction.
	CexCounterPositionFee float64 `json:"cex_counter_position_fee"`
	// CexHedgingPosition             CexHedgingPositionType `json:"cex_hedging_position"`

	// DexOfferCreation is the dex offer creation value for the transaction.
	DexOfferCreation json.RawMessage `json:"dex_offer_creation" gorm:"type:jsonb"`
	// DexOfferTaken is the dex offer taken value for the transaction.
	DexOfferTaken time.Time `json:"dex_offer_taken"`
	// DexFiatArrival is a fiat-denominated dex fiat arrival value.
	DexFiatArrival json.RawMessage `json:"dex_fiat_arrival" gorm:"type:jsonb"`
	// DexBtcRelease is a BTC-denominated dex btc release value.
	DexBtcRelease time.Time `json:"dex_btc_release"`
	// DexFees is the fee value reported for the transaction.
	DexFees float64 `json:"dex_fees"`

	// TransferFiatToCexStartTime is a fiat-denominated transfer fiat to cex start time value.
	TransferFiatToCexStartTime time.Time `json:"transfer_time_fiat_to_cex"`
	// TransferFiatToCexArrivalTime is a fiat-denominated transfer fiat to cex arrival time value.
	TransferFiatToCexArrivalTime time.Time `json:"fiat_at_cex_arrival"`

	// TransactionComplete is the transaction complete value for the transaction.
	TransactionComplete bool `json:"transaction_complete"`

	// OpenClawLlmInferenceCost is the open claw llm inference cost value for the transaction.
	OpenClawLlmInferenceCost float64 `json:"open_claw_llm_inference_cost"`
	// VpsHostingCost is the vps hosting cost value for the transaction.
	VpsHostingCost float64 `json:"vps_hosting_cost"`

	// TotalTransactionPnl is the total transaction pnl value for the transaction.
	TotalTransactionPnl float64 `json:"total_transaction_pnl"`
	// TotalTransactionTimeElapsed records the relevant time for the transaction.
	TotalTransactionTimeElapsed int64 `json:"total_transaction_time_elapsed"` // seconds
	// TotalTransactionVolatilityInBtcPrice is a BTC-denominated total transaction volatility in btc price value.
	TotalTransactionVolatilityInBtcPrice float64 `json:"total_transaction_volatility_in_btc_price"`
	// PremiumOverCexPriceToBreakEvenPrediction is the price value used by the transaction.
	PremiumOverCexPriceToBreakEvenPrediction float64 `json:"premium_over_cex_price_to_break_even_prediction"`

	// CreatedAt records the relevant time for the transaction.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt records the relevant time for the transaction.
	UpdatedAt time.Time `json:"updated_at"`
	// DeletedAt records the relevant time for the transaction.
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName specifies the table name for this model
func (Transaction) TableName() string {
	return "transactions"
}
