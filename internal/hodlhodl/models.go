package hodlhodl

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
