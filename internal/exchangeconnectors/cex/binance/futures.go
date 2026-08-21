package binance

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	sharedbinance "commons/cexclient/binance"
	"dexMmController/internal/exchangeconnectors/cex"
	futures "github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures"
	models "github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures/src/restapi/models"
)

const defaultFuturesBaseURL = "https://fapi.binance.com"

// FuturesClient adapts Binance's official USDⓈ-M Futures SDK to the controller's
// hedge interface. Authentication, signing, HTTP transport, and WebSocket
// transport remain owned by the SDK.
type FuturesClient struct {
	client    *futures.BinanceDerivativesTradingUsdsFuturesClient
	apiKey    string
	secretKey string
	options   sharedbinance.Options
}

func NewFuturesClient(apiKey, apiSecret string) *FuturesClient {
	return NewFuturesClientWithBaseURL("", apiKey, apiSecret)
}

// NewFuturesClientWithBaseURL overrides only the REST endpoint. Stream users
// that need testnet or a mock server should use NewFuturesClientWithURLs.
func NewFuturesClientWithBaseURL(baseURL, apiKey, apiSecret string) *FuturesClient {
	return newFuturesClient(sharedbinance.Options{BaseURL: baseURL}, apiKey, apiSecret)
}

// NewFuturesClientWithURLs configures both REST and WebSocket endpoints. It is
// useful for testnet and deterministic stream tests.
func NewFuturesClientWithURLs(baseURL, websocketStreamsURL, apiKey, apiSecret string) *FuturesClient {
	return newFuturesClient(sharedbinance.Options{BaseURL: baseURL, WebsocketStreamsURL: websocketStreamsURL}, apiKey, apiSecret)
}

func newFuturesClient(options sharedbinance.Options, apiKey, apiSecret string) *FuturesClient {
	baseURL := options.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultFuturesBaseURL
	}
	options.BaseURL = strings.TrimRight(baseURL, "/")
	key := strings.TrimSpace(apiKey)
	secret := strings.TrimSpace(apiSecret)
	return &FuturesClient{
		client: sharedbinance.NewFutures(key, secret, options),
		apiKey: key, secretKey: secret, options: options,
	}
}

func (c *FuturesClient) PlaceOrder(ctx context.Context, symbol, side string, btcQty float64, leverage int) (*cex.FuturesOrderResult, error) {
	if leverage > 0 {
		if err := c.setLeverage(ctx, symbol, leverage); err != nil {
			// Leverage may already be set; reconciliation still makes a placement
			// attempt safe to continue.
			fmt.Printf("binance futures: set leverage warning: %v\n", err)
		}
	}

	binanceSide := models.NewAlgoOrderSideParameterSell
	if side == "long" {
		binanceSide = models.NewAlgoOrderSideParameterBuy
	}
	resp, err := c.client.RestApi.TradeAPI.NewOrder(ctx).
		Symbol(symbol).
		Side(binanceSide).
		Type(models.PlaceMultipleOrdersBatchOrdersParameterInnerTypeMarket).
		Quantity(float32(btcQty)).
		Execute()
	if err != nil {
		return nil, fmt.Errorf("binance futures order: %w", err)
	}
	return &cex.FuturesOrderResult{
		Venue: "binance_futures", Instrument: symbol, Side: side,
		OrderID: strconv.FormatInt(resp.Data.GetOrderId(), 10),
		Status:  strings.ToLower(resp.Data.GetStatus()),
	}, nil
}

func (c *FuturesClient) CurrentPosition(ctx context.Context) (cex.HedgePositionUpdate, error) {
	resp, err := c.client.RestApi.TradeAPI.PositionInformationV3(ctx).Symbol(hedgeSymbol).Execute()
	if err != nil {
		return cex.HedgePositionUpdate{}, fmt.Errorf("binance futures position risk: %w", err)
	}
	update := cex.HedgePositionUpdate{Venue: "binance_futures", Instrument: hedgeSymbol, Side: "flat", Currency: "USDT", UpdatedAt: time.Now().UTC().Format(time.RFC3339), Connected: true}
	for _, row := range resp.Data.Items {
		if !strings.EqualFold(row.GetSymbol(), hedgeSymbol) {
			continue
		}
		amount, _ := strconv.ParseFloat(row.GetPositionAmt(), 64)
		update.SizeBTC = absFloat(amount)
		update.Side = sideFromSignedSize(amount)
		update.EntryPrice, _ = strconv.ParseFloat(row.GetEntryPrice(), 64)
		update.UnrealizedPnL, _ = strconv.ParseFloat(row.GetUnRealizedProfit(), 64)
		return update, nil
	}
	return update, nil
}

func (c *FuturesClient) setLeverage(ctx context.Context, symbol string, leverage int) error {
	_, err := c.client.RestApi.TradeAPI.ChangeInitialLeverage(ctx).Symbol(symbol).Leverage(int64(leverage)).Execute()
	return err
}
