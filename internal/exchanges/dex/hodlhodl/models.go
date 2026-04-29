package hodlhodl

import "encoding/json"

// ContractStatus represents the lifecycle state of a HodlHodl contract.
type ContractStatus string

const (
	// ContractPending is the contract pending value.
	ContractPending ContractStatus = "pending"
	// ContractDepositing is the contract depositing value.
	ContractDepositing ContractStatus = "depositing"
	// ContractInProgress is the contract in progress value.
	ContractInProgress ContractStatus = "in_progress"
	// ContractPaid is the contract paid value.
	ContractPaid ContractStatus = "paid"
	// ContractCompleted is the contract completed value.
	ContractCompleted ContractStatus = "completed"
	// ContractCanceled is the contract canceled value.
	ContractCanceled ContractStatus = "canceled"
	// ContractDisputed is the contract disputed value.
	ContractDisputed ContractStatus = "disputed"
	// ContractResolved is the contract resolved value.
	ContractResolved ContractStatus = "resolved"
)

// Offer is the JSON shape returned by GET /api/v1/offers/:id.
type Offer struct {
	// ID identifies the offer.
	ID string `json:"id"`
	// Version is the version value for the offer.
	Version string `json:"version"`
	// Status is the current status of the offer.
	Status string `json:"status"` // "active", "paused", "removed", …
	// Side is the side value for the offer.
	Side string `json:"side"` // "buy" or "sell"
	// Title is the title value for the offer.
	Title string `json:"title"`
	// Description is the description value for the offer.
	Description string `json:"description"`
	// CurrencyCode names the asset or network for the offer.
	CurrencyCode string `json:"currency_code"`
	// CountryCode is the count associated with the offer.
	CountryCode string `json:"country_code"`
	// AssetCode is the asset code value for the offer.
	AssetCode string `json:"asset_code"`
	// MaxAmount is the max amount value for the offer.
	MaxAmount string `json:"max_amount"`
	// MinAmount is the min amount value for the offer.
	MinAmount string `json:"min_amount"`
	// Price is the price value used by the offer.
	Price string `json:"price"`
	// FeePercentage is a percentage value.
	FeePercentage string `json:"fee_percentage"`
	// Trader is the trader value for the offer.
	Trader Trader `json:"trader"`

	// PaymentMethodInstructions is populated for "sell" offers (seller's payment details).
	PaymentMethodInstructions []PaymentMethodInstruction `json:"payment_method_instructions"`
	// PaymentMethods is populated for "buy" offers (accepted payment methods).
	PaymentMethods []json.RawMessage `json:"payment_methods"`
}

// Trader is the nested trader info inside an Offer.
type Trader struct {
	// Login is the login value for the trader.
	Login string `json:"login"`
	// TradesCount is the count associated with the trader.
	TradesCount int `json:"trades_count"`
	// Rating is the rating value for the trader.
	Rating float64 `json:"rating"`
	// VolumeTradedBTC is a BTC-denominated volume traded btc value.
	VolumeTradedBTC string `json:"volume_traded_btc"`
	// OnlineStatus is the current status of the trader.
	OnlineStatus string `json:"online_status"`
	// LastOnlineAt records the relevant time for the trader.
	LastOnlineAt string `json:"last_online_at"`
	// StrongHodler is the strong hodler value for the trader.
	StrongHodler bool `json:"strong_hodler"`
	// CountryCode is the count associated with the trader.
	CountryCode string `json:"country_code"`
	// AveragePaymentTime records the relevant time for the trader.
	AveragePaymentTime int `json:"average_payment_time_minutes"`
	// AverageReleaseTime records the relevant time for the trader.
	AverageReleaseTime int `json:"average_release_time_minutes"`
}

// Contract is the JSON shape returned by GET /api/v1/contracts/:id
// and GET /api/v1/contracts/my.
type Contract struct {
	// ID identifies the contract.
	ID string `json:"id"`
	// Status is the current status of the contract.
	Status ContractStatus `json:"status"`
	// YourRole is the your role value for the contract.
	YourRole string `json:"your_role"` // "buyer" or "seller"

	// OfferID is the offer id value for the contract.
	OfferID string `json:"offer_id"`
	// Price is the price value used by the contract.
	Price string `json:"price"`
	// Value is the value value for the contract.
	Value string `json:"value"` // fiat amount
	// Volume is the volume value for the contract.
	Volume string `json:"volume"` // BTC amount
	// CurrencyCode names the asset or network for the contract.
	CurrencyCode string `json:"currency_code"` // e.g. "AUD", "EUR"
	// AssetCode is the asset code value for the contract.
	AssetCode string `json:"asset_code"` // e.g. "BTC"

	// Confirmations is the confirmations value for the contract.
	Confirmations int `json:"confirmations"`
	// ReleaseAddress is the wallet or peer address used by the contract.
	ReleaseAddress string `json:"release_address"`

	// Escrow is the escrow value for the contract.
	Escrow struct {
		Address              string `json:"address"`
		Confirmations        int    `json:"confirmations"`
		AmountDeposited      string `json:"amount_deposited"`
		AmountReleased       string `json:"amount_released"`
		DepositTransactionID string `json:"deposit_transaction_id"`
		ReleaseTransactionID string `json:"release_transaction_id"`
	} `json:"escrow"`

	// Counterparty is the count associated with the contract.
	Counterparty struct {
		Login        string `json:"login"`
		OnlineStatus string `json:"online_status"`
		Rating       string `json:"rating"`
		TradesCount  int    `json:"trades_count"`
	} `json:"counterparty"`

	// CreatedAt records the relevant time for the contract.
	CreatedAt string `json:"created_at"`
}

// OfferSearchFilters configures the GET /api/v1/offers query.
type OfferSearchFilters struct {
	// Side is the side value for the offer search filters.
	Side string // "buy" or "sell"
	// CurrencyCode names the asset or network for the offer search filters.
	CurrencyCode string // e.g. "EUR", "USD"
	// AssetCode is the asset code value for the offer search filters.
	AssetCode string // e.g. "BTC" (default)
	// Country is the count associated with the offer search filters.
	Country string // ISO country code
	// Amount is the amount value for the offer search filters.
	Amount string // fiat amount to match against offer min/max
	// Limit is the limit value for the offer search filters.
	Limit int // pagination limit (max 100)
	// Offset is the offset value for the offer search filters.
	Offset int // pagination offset
}

// CreateContractParams are the required fields for POST /api/v1/contracts.
type CreateContractParams struct {
	// OfferID is the offer id value for the create contract params.
	OfferID string `json:"offer_id"`
	// OfferVersion is the offer version value for the create contract params.
	OfferVersion string `json:"offer_version"`
	// PaymentMethodInstructionID is the payment method instruction id value for the create contract params.
	PaymentMethodInstructionID string `json:"payment_method_instruction_id"`
	// PaymentMethodInstructionVer is the payment method instruction ver value for the create contract params.
	PaymentMethodInstructionVer string `json:"payment_method_instruction_version"`
	// Value is the value value for the create contract params.
	Value string `json:"value,omitempty"` // fiat amount (mutually exclusive with Volume)
	// Volume is the volume value for the create contract params.
	Volume string `json:"volume,omitempty"` // BTC amount (mutually exclusive with Value)
	// Comment is the comment value for the create contract params.
	Comment string `json:"comment,omitempty"`
}

// PaymentMethodInstruction represents a saved payment method on the user's
// account (GET /api/v1/payment_method_instructions) and also appears nested
// inside offers.
type PaymentMethodInstruction struct {
	// ID identifies the payment method instruction.
	ID string `json:"id"`
	// Version is the version value for the payment method instruction.
	Version string `json:"version"`
	// PaymentMethodID is the payment method id value for the payment method instruction.
	PaymentMethodID string `json:"payment_method_id"`
	// Type is the type value for the payment method instruction.
	Type string `json:"type"`
	// Name is the name value for the payment method instruction.
	Name string `json:"name"`
	// Details is the details value for the payment method instruction.
	Details string `json:"details"`
}

// paymentMethodInstructionsResponse wraps the list returned by
// GET /api/v1/payment_method_instructions.
type paymentMethodInstructionsResponse struct {
	PaymentMethodInstructions []PaymentMethodInstruction `json:"payment_method_instructions"`
}

// offerResponse wraps a single offer.
type offerResponse struct {
	Offer Offer `json:"offer"`
}

// offersResponse wraps a list of offers.
type offersResponse struct {
	Offers []Offer `json:"offers"`
}

// contractResponse wraps a single contract.
type contractResponse struct {
	Contract Contract `json:"contract"`
}

// contractsResponse wraps a list of contracts.
type contractsResponse struct {
	Contracts []Contract `json:"contracts"`
}
