package binance

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	sharedbinance "commons/cexclient/binance"
	spot "github.com/binance/binance-connector-go/clients/spot"
	spotmodels "github.com/binance/binance-connector-go/clients/spot/src/restapi/models"
	wallet "github.com/binance/binance-connector-go/clients/wallet"
	walletmodels "github.com/binance/binance-connector-go/clients/wallet/src/restapi/models"
)

const defaultBaseURL = "https://api.binance.com"

// Client represents a client.
type Client struct {
	spot   *spot.BinanceSpotClient
	wallet *wallet.BinanceWalletClient
}

// DepositAddressResponse represents a deposit address response.
type DepositAddressResponse struct {
	// Address is the wallet or peer address used by the deposit address response.
	Address string `json:"address"`
	// Coin names the asset or network for the deposit address response.
	Coin string `json:"coin"`
	// Tag is the tag value for the deposit address response.
	Tag string `json:"tag"`
	// URL is the endpoint URL.
	URL string `json:"url"`
	// IsDefault reports whether the deposit address response condition is true.
	IsDefault int `json:"isDefault"`
}

// WithdrawRequest represents a withdraw request.
type WithdrawRequest struct {
	// Coin names the asset or network for the withdraw request.
	Coin string
	// Address is the wallet or peer address used by the withdraw request.
	Address string
	// Amount is the amount value for the withdraw request.
	Amount string
	// Network names the asset or network for the withdraw request.
	Network string
	// AddressTag is the wallet or peer address used by the withdraw request.
	AddressTag string
	// WithdrawOrderID preserves additional source data for the withdraw request.
	WithdrawOrderID string
	// TransactionFeeFlag is the fee value reported for the withdraw request.
	TransactionFeeFlag *bool
	// Name is the name value for the withdraw request.
	Name string
	// WalletType is the wallet type value for the withdraw request.
	WalletType *int
	// RecvWindow is the recv window value for the withdraw request.
	RecvWindow int64
}

// WithdrawResponse represents a withdraw response.
type WithdrawResponse struct {
	// ID identifies the withdraw response.
	ID string `json:"id"`
}

// DepositHistoryParams represents a deposit history params.
type DepositHistoryParams struct {
	// Coin names the asset or network for the deposit history params.
	Coin string
	// Status is the current status of the deposit history params.
	Status *int
	// StartTime records the relevant time for the deposit history params.
	StartTime *time.Time
	// EndTime records the relevant time for the deposit history params.
	EndTime *time.Time
	// Offset is the offset value for the deposit history params.
	Offset *int
	// Limit is the limit value for the deposit history params.
	Limit *int
	// RecvWindow is the recv window value for the deposit history params.
	RecvWindow int64
}

// DepositRecord represents a deposit record.
type DepositRecord struct {
	// ID identifies the deposit record.
	ID string `json:"id"`
	// Amount is the amount value for the deposit record.
	Amount string `json:"amount"`
	// Coin names the asset or network for the deposit record.
	Coin string `json:"coin"`
	// Network names the asset or network for the deposit record.
	Network string `json:"network"`
	// Status is the current status of the deposit record.
	Status int `json:"status"`
	// Address is the wallet or peer address used by the deposit record.
	Address string `json:"address"`
	// AddressTag is the wallet or peer address used by the deposit record.
	AddressTag string `json:"addressTag"`
	// TxID is the tx id value for the deposit record.
	TxID string `json:"txId"`
	// InsertTime records the relevant time for the deposit record.
	InsertTime int64 `json:"insertTime"`
	// TransferType is the transfer type value for the deposit record.
	TransferType int `json:"transferType"`
	// ConfirmTimes records the relevant time for the deposit record.
	ConfirmTimes string `json:"confirmTimes"`
	// UnlockConfirm is the unlock confirm value for the deposit record.
	UnlockConfirm string `json:"unlockConfirm"`
}

// WithdrawHistoryParams represents a withdraw history params.
type WithdrawHistoryParams struct {
	// Coin names the asset or network for the withdraw history params.
	Coin string
	// WithdrawOrderID preserves additional source data for the withdraw history params.
	WithdrawOrderID string
	// Status is the current status of the withdraw history params.
	Status *int
	// StartTime records the relevant time for the withdraw history params.
	StartTime *time.Time
	// EndTime records the relevant time for the withdraw history params.
	EndTime *time.Time
	// Offset is the offset value for the withdraw history params.
	Offset *int
	// Limit is the limit value for the withdraw history params.
	Limit *int
	// RecvWindow is the recv window value for the withdraw history params.
	RecvWindow int64
}

// WithdrawRecord represents a withdraw record.
type WithdrawRecord struct {
	// ID identifies the withdraw record.
	ID string `json:"id"`
	// Amount is the amount value for the withdraw record.
	Amount string `json:"amount"`
	// TransactionFee is the fee value reported for the withdraw record.
	TransactionFee string `json:"transactionFee"`
	// Coin names the asset or network for the withdraw record.
	Coin string `json:"coin"`
	// Status is the current status of the withdraw record.
	Status int `json:"status"`
	// Address is the wallet or peer address used by the withdraw record.
	Address string `json:"address"`
	// ApplyTime records the relevant time for the withdraw record.
	ApplyTime string `json:"applyTime"`
	// Network names the asset or network for the withdraw record.
	Network string `json:"network"`
	// TransferType is the transfer type value for the withdraw record.
	TransferType int `json:"transferType"`
	// WithdrawOrderID preserves additional source data for the withdraw record.
	WithdrawOrderID string `json:"withdrawOrderId"`
	// TxID is the tx id value for the withdraw record.
	TxID string `json:"txId"`
}

// SpotBalance is a single asset balance from the spot account endpoint.
type SpotBalance struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

// AccountInfo reports Binance account permissions and balances.
type AccountInfo struct {
	// CanTrade is the can trade value for the account info.
	CanTrade bool `json:"canTrade"`
	// CanWithdraw preserves additional source data for the account info.
	CanWithdraw bool `json:"canWithdraw"`
	// CanDeposit is the can deposit value for the account info.
	CanDeposit bool `json:"canDeposit"`
}

// KeyRestrictions reports API-key-level permissions from Binance.
type KeyRestrictions struct {
	// IPRestrict reports whether the key is IP-restricted.
	IPRestrict bool `json:"ipRestrict"`
	// EnableReading reports whether the key can read account data.
	EnableReading bool `json:"enableReading"`
	// EnableSpotAndMarginTrading reports whether the key can trade spot/margin.
	EnableSpotAndMarginTrading bool `json:"enableSpotAndMarginTrading"`
	// EnableWithdrawals reports whether the key can withdraw funds.
	EnableWithdrawals bool `json:"enableWithdrawals"`
	// EnableFutures reports whether the key can trade USDⓈ-M/COIN-M futures (hedging).
	EnableFutures bool `json:"enableFutures"`
}

// WithdrawAddressEntry is a saved withdrawal address from the account address book.
type WithdrawAddressEntry struct {
	// Address is the wallet or peer address used by the withdraw address entry.
	Address string `json:"address"`
	// AddressTag is the optional tag/memo for the withdraw address entry.
	AddressTag string `json:"addressTag"`
	// Coin names the asset for the withdraw address entry.
	Coin string `json:"coin"`
	// Network names the blockchain network for the withdraw address entry.
	Network string `json:"network"`
	// Name is the user-defined label for the withdraw address entry.
	Name string `json:"name"`
	// WhiteStatus reports whether the address is whitelisted.
	WhiteStatus bool `json:"whiteStatus"`
}

// OrderFill is a single fill (partial execution) within a Binance order response.
type OrderFill struct {
	// Price is the fill price, as a decimal string.
	Price string `json:"price"`
	// Qty is the filled base-asset quantity for this fill, as a decimal string.
	Qty string `json:"qty"`
	// Commission is the fee charged for this fill, as a decimal string.
	Commission string `json:"commission"`
	// CommissionAsset is the asset the fee was charged in (may differ from the
	// quote currency, e.g. "BNB" when the fee-discount program is active).
	CommissionAsset string `json:"commissionAsset"`
	// TradeID is the exchange-assigned trade id for this fill.
	TradeID int64 `json:"tradeId"`
}

// OrderResponse is the result of a Binance spot order placement.
type OrderResponse struct {
	// Symbol is the trading symbol the order was placed on.
	Symbol string `json:"symbol"`
	// OrderID is the exchange-assigned order id.
	OrderID int64 `json:"orderId"`
	// Status is the current order status (e.g. FILLED).
	Status string `json:"status"`
	// ExecutedQty is the filled base-asset quantity.
	ExecutedQty string `json:"executedQty"`
	// CummulativeQuoteQty is the filled quote-asset amount (spelling per Binance API).
	CummulativeQuoteQty string `json:"cummulativeQuoteQty"`
	// Fills lists the individual executions that made up this order. Present for
	// MARKET orders, which use Binance's default FULL response type.
	Fills []OrderFill `json:"fills"`
}

// NewClient creates a client.
func NewClient(baseURL, apiKey, secretKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	opts := sharedbinance.Options{BaseURL: strings.TrimRight(baseURL, "/")}
	return &Client{
		spot:   sharedbinance.NewSpot(apiKey, secretKey, opts),
		wallet: sharedbinance.NewWallet(apiKey, secretKey, opts),
	}
}

// Name returns the exchange or bridge name.
func (c *Client) Name() string {
	return "binance"
}

// AccountInfo retrieves account capability and permission details.
func (c *Client) AccountInfo(ctx context.Context) (*AccountInfo, error) {
	resp, err := c.spot.RestApi.AccountAPI.GetAccount(ctx).Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("account info", false, err)
	}
	data := resp.Data
	return &AccountInfo{
		CanTrade:    data.GetCanTrade(),
		CanWithdraw: data.GetCanWithdraw(),
		CanDeposit:  data.GetCanDeposit(),
	}, nil
}

// SpotBalances retrieves non-zero spot wallet balances.
func (c *Client) SpotBalances(ctx context.Context) ([]SpotBalance, error) {
	resp, err := c.spot.RestApi.AccountAPI.GetAccount(ctx).Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("spot balances", false, err)
	}
	balances := make([]SpotBalance, 0, len(resp.Data.Balances))
	for _, row := range resp.Data.Balances {
		balances = append(balances, SpotBalance{
			Asset:  row.GetAsset(),
			Free:   row.GetFree(),
			Locked: row.GetLocked(),
		})
	}
	return balances, nil
}

// KeyRestrictions retrieves API-key permission flags for the configured credentials.
func (c *Client) KeyRestrictions(ctx context.Context) (*KeyRestrictions, error) {
	resp, err := c.wallet.RestApi.AccountAPI.GetApiKeyPermission(ctx).Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("key restrictions", false, err)
	}
	data := resp.Data
	return &KeyRestrictions{
		IPRestrict:                 data.GetIpRestrict(),
		EnableReading:              data.GetEnableReading(),
		EnableSpotAndMarginTrading: data.GetEnableSpotAndMarginTrading(),
		EnableWithdrawals:          data.GetEnableWithdrawals(),
		EnableFutures:              data.GetEnableFutures(),
	}, nil
}

// WithdrawAddressList retrieves the saved (address book) withdrawal addresses.
func (c *Client) WithdrawAddressList(ctx context.Context) ([]WithdrawAddressEntry, error) {
	resp, err := c.wallet.RestApi.CapitalAPI.FetchWithdrawAddressList(ctx).Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("withdraw address list", false, err)
	}
	out := make([]WithdrawAddressEntry, 0, len(resp.Data.Items))
	for _, row := range resp.Data.Items {
		out = append(out, WithdrawAddressEntry{
			Address:     row.GetAddress(),
			AddressTag:  row.GetAddressTag(),
			Coin:        row.GetCoin(),
			Network:     row.GetNetwork(),
			Name:        row.GetName(),
			WhiteStatus: row.GetWhiteStatus(),
		})
	}
	return out, nil
}

// GetDepositAddress retrieves a deposit address.
func (c *Client) GetDepositAddress(ctx context.Context, coin, network string) (*DepositAddressResponse, error) {
	req := c.wallet.RestApi.CapitalAPI.DepositAddress(ctx).Coin(coin)
	if network != "" {
		req = req.Network(network)
	}
	resp, err := req.Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("deposit address", false, err)
	}
	return &DepositAddressResponse{
		Address: resp.Data.GetAddress(),
		Coin:    resp.Data.GetCoin(),
		Tag:     resp.Data.GetTag(),
		URL:     resp.Data.GetUrl(),
	}, nil
}

// ApplyWithdraw submits a withdrawal request to Binance.
func (c *Client) ApplyWithdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error) {
	amount, err := strconv.ParseFloat(strings.TrimSpace(req.Amount), 32)
	if err != nil {
		return nil, fmt.Errorf("binance: withdraw amount: %w", err)
	}
	call := c.wallet.RestApi.CapitalAPI.Withdraw(ctx).
		Coin(req.Coin).
		Address(req.Address).
		Amount(float32(amount))
	if req.Network != "" {
		call = call.Network(req.Network)
	}
	if req.AddressTag != "" {
		call = call.AddressTag(req.AddressTag)
	}
	if req.WithdrawOrderID != "" {
		call = call.WithdrawOrderId(req.WithdrawOrderID)
	}
	if req.TransactionFeeFlag != nil {
		call = call.TransactionFeeFlag(*req.TransactionFeeFlag)
	}
	if req.Name != "" {
		call = call.Name(req.Name)
	}
	if req.WalletType != nil {
		call = call.WalletType(int64(*req.WalletType))
	}
	if req.RecvWindow > 0 {
		call = call.RecvWindow(req.RecvWindow)
	}
	resp, err := call.Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("withdraw", true, err)
	}
	return &WithdrawResponse{ID: resp.Data.GetId()}, nil
}

// MarketBuy places a market buy order for quantityBTC of the base asset on symbol (e.g. "BTCEUR").
func (c *Client) MarketBuy(ctx context.Context, symbol string, quantityBTC float64) (*OrderResponse, error) {
	resp, err := c.spot.RestApi.TradeAPI.NewOrder(ctx).
		Symbol(symbol).
		Side(spotmodels.NewOrderSideParameterBuy).
		Type(spotmodels.NewOrderTypeParameterMarket).
		Quantity(float32(quantityBTC)).
		NewOrderRespType(spotmodels.NewOrderNewOrderRespTypeParameterFull).
		Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("market buy", true, err)
	}
	return mapOrderResponse(resp.Data), nil
}

// GetDepositHistory retrieves a deposit history.
func (c *Client) GetDepositHistory(ctx context.Context, params DepositHistoryParams) ([]DepositRecord, error) {
	call := c.wallet.RestApi.CapitalAPI.DepositHistory(ctx)
	if params.Coin != "" {
		call = call.Coin(params.Coin)
	}
	if params.Status != nil {
		call = call.Status(walletmodels.DepositHistoryStatusParameter(*params.Status))
	}
	if params.StartTime != nil {
		call = call.StartTime(params.StartTime.UnixMilli())
	}
	if params.EndTime != nil {
		call = call.EndTime(params.EndTime.UnixMilli())
	}
	if params.Offset != nil {
		call = call.Offset(int64(*params.Offset))
	}
	if params.Limit != nil {
		call = call.Limit(int64(*params.Limit))
	}
	if params.RecvWindow > 0 {
		call = call.RecvWindow(params.RecvWindow)
	}
	resp, err := call.Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("deposit history", false, err)
	}
	out := make([]DepositRecord, 0, len(resp.Data.Items))
	for _, row := range resp.Data.Items {
		unlock := ""
		if row.UnlockConfirm != nil {
			unlock = strconv.FormatInt(*row.UnlockConfirm, 10)
		}
		out = append(out, DepositRecord{
			ID:            row.GetId(),
			Amount:        row.GetAmount(),
			Coin:          row.GetCoin(),
			Network:       row.GetNetwork(),
			Status:        int(row.GetStatus()),
			Address:       row.GetAddress(),
			AddressTag:    row.GetAddressTag(),
			TxID:          row.GetTxId(),
			InsertTime:    row.GetInsertTime(),
			TransferType:  int(row.GetTransferType()),
			ConfirmTimes:  row.GetConfirmTimes(),
			UnlockConfirm: unlock,
		})
	}
	return out, nil
}

// GetWithdrawHistory retrieves a withdraw history.
func (c *Client) GetWithdrawHistory(ctx context.Context, params WithdrawHistoryParams) ([]WithdrawRecord, error) {
	call := c.wallet.RestApi.CapitalAPI.WithdrawHistory(ctx)
	if params.Coin != "" {
		call = call.Coin(params.Coin)
	}
	if params.WithdrawOrderID != "" {
		call = call.WithdrawOrderId(params.WithdrawOrderID)
	}
	if params.Status != nil {
		call = call.Status(int64(*params.Status))
	}
	if params.StartTime != nil {
		call = call.StartTime(params.StartTime.UnixMilli())
	}
	if params.EndTime != nil {
		call = call.EndTime(params.EndTime.UnixMilli())
	}
	if params.Offset != nil {
		call = call.Offset(int64(*params.Offset))
	}
	if params.Limit != nil {
		call = call.Limit(int64(*params.Limit))
	}
	if params.RecvWindow > 0 {
		call = call.RecvWindow(params.RecvWindow)
	}
	resp, err := call.Execute()
	if err != nil {
		return nil, sharedbinance.WrapError("withdraw history", false, err)
	}
	out := make([]WithdrawRecord, 0, len(resp.Data.Items))
	for _, row := range resp.Data.Items {
		out = append(out, WithdrawRecord{
			ID:              row.GetId(),
			Amount:          row.GetAmount(),
			TransactionFee:  row.GetTransactionFee(),
			Coin:            row.GetCoin(),
			Status:          int(row.GetStatus()),
			Address:         row.GetAddress(),
			ApplyTime:       row.GetApplyTime(),
			Network:         row.GetNetwork(),
			TransferType:    int(row.GetTransferType()),
			WithdrawOrderID: row.GetWithdrawOrderId(),
			TxID:            row.GetTxId(),
		})
	}
	return out, nil
}

func mapOrderResponse(data spotmodels.NewOrderResponse) *OrderResponse {
	fills := make([]OrderFill, 0, len(data.Fills))
	for _, fill := range data.Fills {
		fills = append(fills, OrderFill{
			Price:           fill.GetPrice(),
			Qty:             fill.GetQty(),
			Commission:      fill.GetCommission(),
			CommissionAsset: fill.GetCommissionAsset(),
			TradeID:         fill.GetTradeId(),
		})
	}
	return &OrderResponse{
		Symbol:              data.GetSymbol(),
		OrderID:             data.GetOrderId(),
		Status:              data.GetStatus(),
		ExecutedQty:         data.GetExecutedQty(),
		CummulativeQuoteQty: data.GetCummulativeQuoteQty(),
		Fills:               fills,
	}
}
