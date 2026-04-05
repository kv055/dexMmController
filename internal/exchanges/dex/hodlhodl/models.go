package hodlhodl

import "encoding/json"

// ContractStatus represents the lifecycle state of a HodlHodl contract.
type ContractStatus string

const (
	ContractPending    ContractStatus = "pending"
	ContractDepositing ContractStatus = "depositing"
	ContractInProgress ContractStatus = "in_progress"
	ContractPaid       ContractStatus = "paid"
	ContractCompleted  ContractStatus = "completed"
	ContractCanceled   ContractStatus = "canceled"
	ContractDisputed   ContractStatus = "disputed"
	ContractResolved   ContractStatus = "resolved"
)

// Offer is the JSON shape returned by GET /api/v1/offers/:id.
type Offer struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	Status        string `json:"status"` // "active", "paused", "removed", …
	Side          string `json:"side"`   // "buy" or "sell"
	Title         string `json:"title"`
	Description   string `json:"description"`
	CurrencyCode  string `json:"currency_code"`
	CountryCode   string `json:"country_code"`
	AssetCode     string `json:"asset_code"`
	MaxAmount     string `json:"max_amount"`
	MinAmount     string `json:"min_amount"`
	Price         string `json:"price"`
	FeePercentage string `json:"fee_percentage"`
	Trader        Trader `json:"trader"`

	// PaymentMethodInstructions is populated for "sell" offers (seller's payment details).
	PaymentMethodInstructions []PaymentMethodInstruction `json:"payment_method_instructions"`
	// PaymentMethods is populated for "buy" offers (accepted payment methods).
	PaymentMethods []json.RawMessage `json:"payment_methods"`
}

// Trader is the nested trader info inside an Offer.
type Trader struct {
	Login              string  `json:"login"`
	TradesCount        int     `json:"trades_count"`
	Rating             float64 `json:"rating"`
	VolumeTradedBTC    string  `json:"volume_traded_btc"`
	OnlineStatus       string  `json:"online_status"`
	LastOnlineAt       string  `json:"last_online_at"`
	StrongHodler       bool    `json:"strong_hodler"`
	CountryCode        string  `json:"country_code"`
	AveragePaymentTime int     `json:"average_payment_time_minutes"`
	AverageReleaseTime int     `json:"average_release_time_minutes"`
}

// Contract is the JSON shape returned by GET /api/v1/contracts/:id
// and GET /api/v1/contracts/my.
type Contract struct {
	ID       string         `json:"id"`
	Status   ContractStatus `json:"status"`
	YourRole string         `json:"your_role"` // "buyer" or "seller"

	OfferID      string `json:"offer_id"`
	Price        string `json:"price"`
	Value        string `json:"value"`         // fiat amount
	Volume       string `json:"volume"`        // BTC amount
	CurrencyCode string `json:"currency_code"` // e.g. "AUD", "EUR"
	AssetCode    string `json:"asset_code"`    // e.g. "BTC"

	Confirmations  int    `json:"confirmations"`
	ReleaseAddress string `json:"release_address"`

	Escrow struct {
		Address              string `json:"address"`
		Confirmations        int    `json:"confirmations"`
		AmountDeposited      string `json:"amount_deposited"`
		AmountReleased       string `json:"amount_released"`
		DepositTransactionID string `json:"deposit_transaction_id"`
		ReleaseTransactionID string `json:"release_transaction_id"`
	} `json:"escrow"`

	Counterparty struct {
		Login        string `json:"login"`
		OnlineStatus string `json:"online_status"`
		Rating       string `json:"rating"`
		TradesCount  int    `json:"trades_count"`
	} `json:"counterparty"`

	CreatedAt string `json:"created_at"`
}

// OfferSearchFilters configures the GET /api/v1/offers query.
type OfferSearchFilters struct {
	Side         string // "buy" or "sell"
	CurrencyCode string // e.g. "EUR", "USD"
	AssetCode    string // e.g. "BTC" (default)
	Country      string // ISO country code
	Amount       string // fiat amount to match against offer min/max
	Limit        int    // pagination limit (max 100)
	Offset       int    // pagination offset
}

// CreateContractParams are the required fields for POST /api/v1/contracts.
type CreateContractParams struct {
	OfferID                       string `json:"offer_id"`
	OfferVersion                  string `json:"offer_version"`
	PaymentMethodInstructionID    string `json:"payment_method_instruction_id"`
	PaymentMethodInstructionVer   string `json:"payment_method_instruction_version"`
	Value                         string `json:"value,omitempty"`  // fiat amount (mutually exclusive with Volume)
	Volume                        string `json:"volume,omitempty"` // BTC amount (mutually exclusive with Value)
	Comment                       string `json:"comment,omitempty"`
}

// PaymentMethodInstruction represents a saved payment method on the user's
// account (GET /api/v1/payment_method_instructions) and also appears nested
// inside offers.
type PaymentMethodInstruction struct {
	ID              string `json:"id"`
	Version         string `json:"version"`
	PaymentMethodID string `json:"payment_method_id"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Details         string `json:"details"`
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
