package bisq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	defaultHost                   = "localhost:9998"
	defaultCurrencyCode           = "EUR"
	defaultTakerFeeCurrency       = "BTC"
	tradesCategoryOpen      int32 = 0
)

// Client represents a client.
type Client struct {
	host     string
	password string

	conn    *grpc.ClientConn
	connErr error
	once    sync.Once
}

// Offer is a normalized DEX offer used by this package.
type Offer struct {
	// ID identifies the offer.
	ID string
	// Direction is the direction value for the offer.
	Direction string
	// Price is the price value used by the offer.
	Price string
	// AmountSats is the amount in satoshis.
	AmountSats uint64
	// MinAmountSats is the amount in satoshis.
	MinAmountSats uint64
	// Volume is the volume value for the offer.
	Volume string
	// MinVolume is the min volume value for the offer.
	MinVolume string
	// PaymentAccountID is the count associated with the offer.
	PaymentAccountID string
	// PaymentMethodID is the payment method id value for the offer.
	PaymentMethodID string
	// PaymentMethodShortName is the payment method short name value for the offer.
	PaymentMethodShortName string
	// State is the state value for the offer.
	State string
	// IsActivated reports whether the offer condition is true.
	IsActivated bool
	// IsMyOffer reports whether the offer condition is true.
	IsMyOffer bool
}

// PaymentAccount represents a payment account.
type PaymentAccount struct {
	// ID identifies the payment account.
	ID string
	// AccountName is the count associated with the payment account.
	AccountName string
	// PaymentMethodID is the payment method id value for the payment account.
	PaymentMethodID string
	// TradeCurrency names the asset or network for the payment account.
	TradeCurrency string
}

// Trade represents a trade.
type Trade struct {
	// TradeID is the trade id value for the trade.
	TradeID string
	// OfferID is the offer id value for the trade.
	OfferID string
	// Role is the role value for the trade.
	Role string
	// State is the state value for the trade.
	State string
	// Phase is the phase value for the trade.
	Phase string
	// TradePrice is the price value used by the trade.
	TradePrice string
	// TradeVolume is the trade volume value for the trade.
	TradeVolume string
	// TradeAmountSats is the amount in satoshis.
	TradeAmountSats uint64
	// IsCompleted reports whether the trade condition is true.
	IsCompleted bool
	// HasFailed reports whether the trade condition is true.
	HasFailed bool
	// ErrorMessage carries a human-readable message for the trade.
	ErrorMessage string
	// DepositTxID is the deposit tx id value for the trade.
	DepositTxID string
	// PayoutTxID is the payout tx id value for the trade.
	PayoutTxID string
	// ContractAsJSON is the full bisq trade contract serialized as JSON. It carries both
	// parties' payment-account payloads (makerPaymentAccountPayload / takerPaymentAccountPayload)
	// once the trade is taken, and is the source for GetCounterpartyPayment. Empty before the
	// contract is formed.
	ContractAsJSON string
	// CreatedAt records the relevant time for the trade.
	CreatedAt time.Time
}

// AddressBalance is a normalized funding address from the bisq wallet.
type AddressBalance struct {
	// Address is the on-chain BTC address.
	Address string
	// BalanceSats is the confirmed balance in satoshis.
	BalanceSats int64
	// NumConfirmations is the confirmation count of the most recent deposit.
	NumConfirmations int64
	// IsAddressUnused reports whether the address has never received funds.
	IsAddressUnused bool
}

// TakeOfferParams represents a take offer params.
type TakeOfferParams struct {
	// PaymentAccountID is the count associated with the take offer params.
	PaymentAccountID string
	// TakerFeeCurrencyCode names the asset or network for the take offer params.
	TakerFeeCurrencyCode string
	// AmountSats is the amount in satoshis.
	AmountSats uint64
}

// CreatePositionParams represents a create position params.
type CreatePositionParams struct {
	// CurrencyCode names the asset or network for the create position params.
	CurrencyCode string
	// Direction is the direction value for the create position params.
	Direction string
	// Price is the price value used by the create position params.
	Price string
	// UseMarketBasedPrice is the price value used by the create position params.
	UseMarketBasedPrice bool
	// MarketPriceMarginPct is a percentage value.
	MarketPriceMarginPct float64
	// AmountSats is the amount in satoshis.
	AmountSats uint64
	// MinAmountSats is the amount in satoshis.
	MinAmountSats uint64
	// BuyerSecurityDepositPct is a percentage value.
	BuyerSecurityDepositPct float64
	// TriggerPrice is the price value used by the create position params.
	TriggerPrice string
	// PaymentAccountID is the count associated with the create position params.
	PaymentAccountID string
	// MakerFeeCurrencyCode names the asset or network for the create position params.
	MakerFeeCurrencyCode string
}

// NewClient creates a client.
func NewClient(host, password string) *Client {
	if host == "" {
		host = defaultHost
	}
	return &Client{host: host, password: password}
}

// GetMyOffers retrieves a my offers.
func (c *Client) GetMyOffers(ctx context.Context, direction, currencyCode string) ([]Offer, error) {
	if currencyCode == "" {
		currencyCode = defaultCurrencyCode
	}

	req := newMessage("GetMyOffersRequest")
	setString(req, "direction", direction)
	setString(req, "currency_code", currencyCode)

	resp := newMessage("GetMyOffersReply")
	if err := c.invoke(ctx, "/io.bisq.protobuffer.Offers/GetMyOffers", req, resp); err != nil {
		return nil, err
	}

	list := resp.Get(resp.Descriptor().Fields().ByName("offers")).List()
	out := make([]Offer, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		out = append(out, offerFromMessage(list.Get(i).Message()))
	}
	return out, nil
}

// GetPaymentAccounts retrieves a payment accounts.
func (c *Client) GetPaymentAccounts(ctx context.Context) ([]PaymentAccount, error) {
	req := newMessage("GetPaymentAccountsRequest")
	resp := newMessage("GetPaymentAccountsReply")
	if err := c.invoke(ctx, "/io.bisq.protobuffer.PaymentAccounts/GetPaymentAccounts", req, resp); err != nil {
		return nil, err
	}

	list := resp.Get(resp.Descriptor().Fields().ByName("payment_accounts")).List()
	out := make([]PaymentAccount, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		out = append(out, paymentAccountFromMessage(list.Get(i).Message()))
	}
	return out, nil
}

// SepaAccountParams carries the discrete fields needed to create a SEPA payment account on
// the bisq daemon. Bisq derives the trade currency (EUR) and accepted SEPA countries itself.
type SepaAccountParams struct {
	// AccountName is the human-readable label for the payment account.
	AccountName string
	// HolderName is the bank account holder's full name.
	HolderName string
	// IBAN is the account IBAN.
	IBAN string
	// BIC is the bank BIC/SWIFT code.
	BIC string
	// CountryCode is the ISO 3166-1 alpha-2 country of the bank (e.g. "DE").
	CountryCode string
}

// CreateSepaPaymentAccount registers a SEPA payment account in the bisq daemon's profile and
// returns the created account. It relies on the dexArbTrading bisq source patch: the stock
// CreatePaymentAccount(json) path cannot round-trip SEPA (its acceptedCountryCodes collection
// is mishandled by the form parser), so the daemon builds the SepaAccount from these discrete
// fields instead. Bisq rate-limits this to one call per minute.
func (c *Client) CreateSepaPaymentAccount(ctx context.Context, params SepaAccountParams) (*PaymentAccount, error) {
	req := newMessage("CreateSepaPaymentAccountRequest")
	setString(req, "account_name", params.AccountName)
	setString(req, "holder_name", params.HolderName)
	setString(req, "iban", params.IBAN)
	setString(req, "bic", params.BIC)
	setString(req, "country_code", params.CountryCode)

	resp := newMessage("CreateSepaPaymentAccountReply")
	if err := c.invoke(ctx, "/io.bisq.protobuffer.PaymentAccounts/CreateSepaPaymentAccount", req, resp); err != nil {
		return nil, err
	}

	paField := resp.Descriptor().Fields().ByName("payment_account")
	if !resp.Has(paField) {
		return nil, errors.New("bisq: create SEPA payment account returned no account")
	}
	account := paymentAccountFromMessage(resp.Get(paField).Message())
	return &account, nil
}

// DeletePaymentAccount removes a saved payment account from the bisq daemon's profile by id.
// It relies on the dexArbTrading bisq source patch (the stock gRPC API has no delete).
func (c *Client) DeletePaymentAccount(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("bisq: payment account ID is required")
	}
	req := newMessage("DeletePaymentAccountRequest")
	setString(req, "payment_account_id", id)
	resp := newMessage("DeletePaymentAccountReply")
	return c.invoke(ctx, "/io.bisq.protobuffer.PaymentAccounts/DeletePaymentAccount", req, resp)
}

// TakeOffer accepts an existing DEX offer.
func (c *Client) TakeOffer(ctx context.Context, offerID string, params TakeOfferParams) (*Trade, error) {
	if offerID == "" {
		return nil, errors.New("bisq: offer ID is required")
	}

	if params.PaymentAccountID == "" {
		accounts, err := c.GetPaymentAccounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("bisq: get payment accounts: %w", err)
		}
		if len(accounts) == 0 {
			return nil, errors.New("bisq: no payment accounts configured")
		}
		params.PaymentAccountID = accounts[0].ID
	}
	if params.TakerFeeCurrencyCode == "" {
		params.TakerFeeCurrencyCode = defaultTakerFeeCurrency
	}

	req := newMessage("TakeOfferRequest")
	setString(req, "offer_id", offerID)
	setString(req, "payment_account_id", params.PaymentAccountID)
	setString(req, "taker_fee_currency_code", params.TakerFeeCurrencyCode)
	setUint64(req, "amount", params.AmountSats)

	resp := newMessage("TakeOfferReply")
	if err := c.invoke(ctx, "/io.bisq.protobuffer.Trades/TakeOffer", req, resp); err != nil {
		return nil, err
	}

	tradeField := resp.Descriptor().Fields().ByName("trade")
	if !resp.Has(tradeField) {
		return nil, errors.New("bisq: take offer returned no trade")
	}
	trade := tradeFromMessage(resp.Get(tradeField).Message())
	return &trade, nil
}

// GetOpenPositions retrieves an open positions.
func (c *Client) GetOpenPositions(ctx context.Context) ([]Trade, error) {
	req := newMessage("GetTradesRequest")
	setEnum(req, "category", tradesCategoryOpen)

	resp := newMessage("GetTradesReply")
	if err := c.invoke(ctx, "/io.bisq.protobuffer.Trades/GetTrades", req, resp); err != nil {
		return nil, err
	}

	list := resp.Get(resp.Descriptor().Fields().ByName("trades")).List()
	out := make([]Trade, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		out = append(out, tradeFromMessage(list.Get(i).Message()))
	}
	return out, nil
}

// GetPosition retrieves a position.
func (c *Client) GetPosition(ctx context.Context, tradeID string) (*Trade, error) {
	if tradeID == "" {
		return nil, errors.New("bisq: trade ID is required")
	}

	req := newMessage("GetTradeRequest")
	setString(req, "trade_id", tradeID)

	resp := newMessage("GetTradeReply")
	if err := c.invoke(ctx, "/io.bisq.protobuffer.Trades/GetTrade", req, resp); err != nil {
		return nil, err
	}

	tradeField := resp.Descriptor().Fields().ByName("trade")
	if !resp.Has(tradeField) {
		return nil, fmt.Errorf("bisq: trade %s not found", tradeID)
	}
	trade := tradeFromMessage(resp.Get(tradeField).Message())
	return &trade, nil
}

// ConfirmPaymentReceived tells the bisq daemon that the buyer's fiat payment has arrived,
// which triggers it to sign and broadcast the multisig payout — releasing the BTC to the
// buyer. This is the bisq Stage-6 release for the direct route: the daemon holds the keys,
// so no local signing is required. The reply is empty; callers read the resulting
// payout_tx_id via GetPosition.
func (c *Client) ConfirmPaymentReceived(ctx context.Context, tradeID string) error {
	if tradeID == "" {
		return errors.New("bisq: trade ID is required")
	}
	req := newMessage("ConfirmPaymentReceivedRequest")
	setString(req, "trade_id", tradeID)
	resp := newMessage("ConfirmPaymentReceivedReply")
	return c.invoke(ctx, "/io.bisq.protobuffer.Trades/ConfirmPaymentReceived", req, resp)
}

// GetCounterpartyPayment returns the maker's (our counterparty's) payment-account payload
// for a taken trade, read from the trade contract. Bisq is the "structured" style: the daemon
// exposes both parties' payment accounts inside contract_as_json once the trade is taken — no
// chat needed. We are always the taker, so the counterparty is the maker
// (makerPaymentAccountPayload); when that key is absent we return the whole contract so nothing
// is lost. Returns an error while the contract has not been formed yet.
func (c *Client) GetCounterpartyPayment(ctx context.Context, tradeID string) (json.RawMessage, error) {
	trade, err := c.GetPosition(ctx, tradeID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(trade.ContractAsJSON) == "" {
		return nil, fmt.Errorf("bisq: trade %s has no contract yet", tradeID)
	}
	return makerPaymentFromContract(trade.ContractAsJSON)
}

// makerPaymentFromContract extracts the maker's payment-account payload from a bisq trade
// contract JSON. We are always the taker, so the counterparty is the maker. When the field is
// absent the whole contract is returned so nothing is lost.
func makerPaymentFromContract(contractJSON string) (json.RawMessage, error) {
	contractJSON = strings.TrimSpace(contractJSON)
	if contractJSON == "" {
		return nil, fmt.Errorf("bisq: empty contract json")
	}
	var contract map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contractJSON), &contract); err != nil {
		return nil, fmt.Errorf("bisq: parse contract json: %w", err)
	}
	if payload, ok := contract["makerPaymentAccountPayload"]; ok && len(payload) > 0 {
		return json.RawMessage(payload), nil
	}
	return json.RawMessage(contractJSON), nil
}

// CreatePosition creates a position.
func (c *Client) CreatePosition(ctx context.Context, params CreatePositionParams) (*Offer, error) {
	if params.CurrencyCode == "" {
		params.CurrencyCode = defaultCurrencyCode
	}
	if params.MakerFeeCurrencyCode == "" {
		params.MakerFeeCurrencyCode = defaultTakerFeeCurrency
	}
	if params.PaymentAccountID == "" {
		accounts, err := c.GetPaymentAccounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("bisq: get payment accounts: %w", err)
		}
		if len(accounts) == 0 {
			return nil, errors.New("bisq: no payment accounts configured")
		}
		params.PaymentAccountID = accounts[0].ID
	}

	req := newMessage("CreateOfferRequest")
	setString(req, "currency_code", params.CurrencyCode)
	setString(req, "direction", params.Direction)
	setString(req, "price", params.Price)
	setBool(req, "use_market_based_price", params.UseMarketBasedPrice)
	setFloat64(req, "market_price_margin_pct", params.MarketPriceMarginPct)
	setUint64(req, "amount", params.AmountSats)
	setUint64(req, "min_amount", params.MinAmountSats)
	setFloat64(req, "buyer_security_deposit_pct", params.BuyerSecurityDepositPct)
	setString(req, "trigger_price", params.TriggerPrice)
	setString(req, "payment_account_id", params.PaymentAccountID)
	setString(req, "maker_fee_currency_code", params.MakerFeeCurrencyCode)

	resp := newMessage("CreateOfferReply")
	if err := c.invoke(ctx, "/io.bisq.protobuffer.Offers/CreateOffer", req, resp); err != nil {
		return nil, err
	}

	offerField := resp.Descriptor().Fields().ByName("offer")
	if !resp.Has(offerField) {
		return nil, errors.New("bisq: create offer returned no offer")
	}
	offer := offerFromMessage(resp.Get(offerField).Message())
	return &offer, nil
}

// GetFundingAddresses returns the bisq wallet's BTC funding addresses with balances.
// Bisq keeps at least one unused funding address available at all times.
func (c *Client) GetFundingAddresses(ctx context.Context) ([]AddressBalance, error) {
	req := newMessage("GetFundingAddressesRequest")
	resp := newMessage("GetFundingAddressesReply")
	if err := c.invoke(ctx, "/io.bisq.protobuffer.Wallets/GetFundingAddresses", req, resp); err != nil {
		return nil, err
	}

	list := resp.Get(resp.Descriptor().Fields().ByName("address_balance_info")).List()
	out := make([]AddressBalance, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		out = append(out, addressBalanceFromMessage(list.Get(i).Message()))
	}
	return out, nil
}

// FundingAddress returns a BTC address to fund the bisq wallet, preferring an unused
// one (so a fresh CEX withdrawal lands on a clean address).
func (c *Client) FundingAddress(ctx context.Context) (string, error) {
	addrs, err := c.GetFundingAddresses(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if a.IsAddressUnused && a.Address != "" {
			return a.Address, nil
		}
	}
	for _, a := range addrs {
		if a.Address != "" {
			return a.Address, nil
		}
	}
	return "", errors.New("bisq: no funding address available")
}

func (c *Client) invoke(ctx context.Context, method string, req, resp *dynamicpb.Message) error {
	conn, err := c.connection(ctx)
	if err != nil {
		return err
	}
	if err := conn.Invoke(ctx, method, req, resp); err != nil {
		return fmt.Errorf("bisq: %s failed: %w", method, err)
	}
	return nil
}

func (c *Client) connection(ctx context.Context) (*grpc.ClientConn, error) {
	c.once.Do(func() {
		c.conn, c.connErr = grpc.DialContext(
			ctx,
			c.host,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithPerRPCCredentials(bisqCreds{password: c.password}),
		)
	})
	return c.conn, c.connErr
}

type bisqCreds struct{ password string }

// GetRequestMetadata retrieves a request metadata.
func (c bisqCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"password": c.password}, nil
}

// RequireTransportSecurity reports whether the credentials require transport security.
func (c bisqCreds) RequireTransportSecurity() bool { return false }

var (
	bisqFileDesc     protoreflect.FileDescriptor
	bisqFileDescOnce sync.Once
	bisqFileDescErr  error
)

func fileDescriptor() protoreflect.FileDescriptor {
	bisqFileDescOnce.Do(func() {
		fd := buildFileDescriptor()
		bisqFileDesc, bisqFileDescErr = protodesc.NewFile(fd, nil)
	})
	if bisqFileDescErr != nil {
		panic(bisqFileDescErr)
	}
	return bisqFileDesc
}

func newMessage(name string) *dynamicpb.Message {
	md := fileDescriptor().Messages().ByName(protoreflect.Name(name))
	if md == nil {
		panic("bisq descriptor missing message: " + name)
	}
	return dynamicpb.NewMessage(md)
}

func setString(msg *dynamicpb.Message, fieldName string, value string) {
	if value == "" {
		return
	}
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(fieldName))
	msg.Set(field, protoreflect.ValueOfString(value))
}

func setBool(msg *dynamicpb.Message, fieldName string, value bool) {
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(fieldName))
	msg.Set(field, protoreflect.ValueOfBool(value))
}

func setFloat64(msg *dynamicpb.Message, fieldName string, value float64) {
	if value == 0 {
		return
	}
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(fieldName))
	msg.Set(field, protoreflect.ValueOfFloat64(value))
}

func setUint64(msg *dynamicpb.Message, fieldName string, value uint64) {
	if value == 0 {
		return
	}
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(fieldName))
	msg.Set(field, protoreflect.ValueOfUint64(value))
}

func setEnum(msg *dynamicpb.Message, fieldName string, value int32) {
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(fieldName))
	msg.Set(field, protoreflect.ValueOfEnum(protoreflect.EnumNumber(value)))
}

func offerFromMessage(msg protoreflect.Message) Offer {
	return Offer{
		ID:                     stringField(msg, "id"),
		Direction:              stringField(msg, "direction"),
		Price:                  stringField(msg, "price"),
		AmountSats:             uint64Field(msg, "amount"),
		MinAmountSats:          uint64Field(msg, "min_amount"),
		Volume:                 stringField(msg, "volume"),
		MinVolume:              stringField(msg, "min_volume"),
		PaymentAccountID:       stringField(msg, "payment_account_id"),
		PaymentMethodID:        stringField(msg, "payment_method_id"),
		PaymentMethodShortName: stringField(msg, "payment_method_short_name"),
		State:                  stringField(msg, "state"),
		IsActivated:            boolField(msg, "is_activated"),
		IsMyOffer:              boolField(msg, "is_my_offer"),
	}
}

func paymentAccountFromMessage(msg protoreflect.Message) PaymentAccount {
	paymentMethodID := ""
	tradeCurrency := ""

	pmField := msg.Descriptor().Fields().ByName("payment_method")
	if pmField != nil && msg.Has(pmField) {
		paymentMethodID = stringField(msg.Get(pmField).Message(), "id")
	}
	tcField := msg.Descriptor().Fields().ByName("selected_trade_currency")
	if tcField != nil && msg.Has(tcField) {
		tradeCurrency = stringField(msg.Get(tcField).Message(), "code")
	}

	return PaymentAccount{
		ID:              stringField(msg, "id"),
		AccountName:     stringField(msg, "account_name"),
		PaymentMethodID: paymentMethodID,
		TradeCurrency:   tradeCurrency,
	}
}

func tradeFromMessage(msg protoreflect.Message) Trade {
	out := Trade{
		TradeID:         stringField(msg, "trade_id"),
		Role:            stringField(msg, "role"),
		State:           stringField(msg, "state"),
		Phase:           stringField(msg, "phase"),
		TradePrice:      stringField(msg, "trade_price"),
		TradeVolume:     stringField(msg, "trade_volume"),
		TradeAmountSats: uint64Field(msg, "trade_amount_as_long"),
		IsCompleted:     boolField(msg, "is_completed"),
		HasFailed:       boolField(msg, "has_failed"),
		ErrorMessage:    stringField(msg, "error_message"),
		DepositTxID:     stringField(msg, "deposit_tx_id"),
		PayoutTxID:      stringField(msg, "payout_tx_id"),
		ContractAsJSON:  stringField(msg, "contract_as_json"),
	}
	if ms := uint64Field(msg, "date"); ms > 0 {
		out.CreatedAt = time.UnixMilli(int64(ms)).UTC()
	}

	offerField := msg.Descriptor().Fields().ByName("offer")
	if offerField != nil && msg.Has(offerField) {
		out.OfferID = stringField(msg.Get(offerField).Message(), "id")
	}
	return out
}

func addressBalanceFromMessage(msg protoreflect.Message) AddressBalance {
	return AddressBalance{
		Address:          stringField(msg, "address"),
		BalanceSats:      int64Field(msg, "balance"),
		NumConfirmations: int64Field(msg, "num_confirmations"),
		IsAddressUnused:  boolField(msg, "is_address_unused"),
	}
}

func stringField(msg protoreflect.Message, name string) string {
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || !msg.Has(field) {
		return ""
	}
	return msg.Get(field).String()
}

func boolField(msg protoreflect.Message, name string) bool {
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || !msg.Has(field) {
		return false
	}
	return msg.Get(field).Bool()
}

func uint64Field(msg protoreflect.Message, name string) uint64 {
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || !msg.Has(field) {
		return 0
	}
	return msg.Get(field).Uint()
}

func int64Field(msg protoreflect.Message, name string) int64 {
	field := msg.Descriptor().Fields().ByName(protoreflect.Name(name))
	if field == nil || !msg.Has(field) {
		return 0
	}
	return msg.Get(field).Int()
}

func buildFileDescriptor() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Syntax:  strptr("proto3"),
		Name:    strptr("bisq_minimal.proto"),
		Package: strptr("io.bisq.protobuffer"),
		MessageType: []*descriptorpb.DescriptorProto{
			message("OfferInfo", []*descriptorpb.FieldDescriptorProto{
				field("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("direction", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("price", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("amount", 6, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
				field("min_amount", 7, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
				field("volume", 8, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("min_volume", 9, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("payment_account_id", 13, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("payment_method_id", 14, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("payment_method_short_name", 15, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("state", 19, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("is_activated", 24, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				field("is_my_offer", 25, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
			}),
			message("PaymentMethod", []*descriptorpb.FieldDescriptorProto{
				field("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("TradeCurrency", []*descriptorpb.FieldDescriptorProto{
				field("code", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("PaymentAccount", []*descriptorpb.FieldDescriptorProto{
				field("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				messageField("payment_method", 3, ".io.bisq.protobuffer.PaymentMethod"),
				field("account_name", 4, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				messageField("selected_trade_currency", 6, ".io.bisq.protobuffer.TradeCurrency"),
			}),
			message("GetMyOffersRequest", []*descriptorpb.FieldDescriptorProto{
				field("direction", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("currency_code", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("GetMyOffersReply", []*descriptorpb.FieldDescriptorProto{
				repeatedMessageField("offers", 1, ".io.bisq.protobuffer.OfferInfo"),
			}),
			message("CreateOfferRequest", []*descriptorpb.FieldDescriptorProto{
				field("currency_code", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("direction", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("price", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("use_market_based_price", 4, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				field("market_price_margin_pct", 5, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
				field("amount", 6, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
				field("min_amount", 7, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
				field("buyer_security_deposit_pct", 8, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
				field("trigger_price", 9, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("payment_account_id", 10, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("maker_fee_currency_code", 11, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("CreateOfferReply", []*descriptorpb.FieldDescriptorProto{
				messageField("offer", 1, ".io.bisq.protobuffer.OfferInfo"),
			}),
			message("GetPaymentAccountsRequest", nil),
			message("GetPaymentAccountsReply", []*descriptorpb.FieldDescriptorProto{
				repeatedMessageField("payment_accounts", 1, ".io.bisq.protobuffer.PaymentAccount"),
			}),
			// CreateSepaPaymentAccount / DeletePaymentAccount are provided by the dexArbTrading
			// bisq source patch (bisq-patches/payment-accounts-sepa-delete.patch); the stock
			// daemon exposes neither.
			message("CreateSepaPaymentAccountRequest", []*descriptorpb.FieldDescriptorProto{
				field("account_name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("holder_name", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("iban", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("bic", 4, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("country_code", 5, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("CreateSepaPaymentAccountReply", []*descriptorpb.FieldDescriptorProto{
				messageField("payment_account", 1, ".io.bisq.protobuffer.PaymentAccount"),
			}),
			message("DeletePaymentAccountRequest", []*descriptorpb.FieldDescriptorProto{
				field("payment_account_id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("DeletePaymentAccountReply", nil),
			message("TakeOfferRequest", []*descriptorpb.FieldDescriptorProto{
				field("offer_id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("payment_account_id", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("taker_fee_currency_code", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("amount", 4, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
			}),
			message("TradeInfo", []*descriptorpb.FieldDescriptorProto{
				messageField("offer", 1, ".io.bisq.protobuffer.OfferInfo"),
				field("trade_id", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("date", 4, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
				field("role", 5, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("deposit_tx_id", 10, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("payout_tx_id", 11, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("trade_amount_as_long", 12, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
				field("trade_price", 13, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("state", 15, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("phase", 16, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("is_completed", 23, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				// contract_as_json (24) and contract (25) carry the trade contract once formed;
				// field numbers match the modern bisq TradeInfo proto (consistent with the
				// is_completed=23 / trade_volume=26 / has_failed=30 numbering already used here).
				field("contract_as_json", 24, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("trade_volume", 26, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("has_failed", 30, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				field("error_message", 31, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("TakeOfferReply", []*descriptorpb.FieldDescriptorProto{
				messageField("trade", 1, ".io.bisq.protobuffer.TradeInfo"),
			}),
			message("GetTradeRequest", []*descriptorpb.FieldDescriptorProto{
				field("trade_id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("GetTradeReply", []*descriptorpb.FieldDescriptorProto{
				messageField("trade", 1, ".io.bisq.protobuffer.TradeInfo"),
			}),
			message("GetTradesRequest", []*descriptorpb.FieldDescriptorProto{
				enumField("category", 1, ".io.bisq.protobuffer.GetTradesRequest.Category"),
			}, &descriptorpb.EnumDescriptorProto{
				Name: strptr("Category"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					{Name: strptr("OPEN"), Number: int32ptr(0)},
					{Name: strptr("CLOSED"), Number: int32ptr(1)},
					{Name: strptr("FAILED"), Number: int32ptr(2)},
				},
			}),
			message("GetTradesReply", []*descriptorpb.FieldDescriptorProto{
				repeatedMessageField("trades", 1, ".io.bisq.protobuffer.TradeInfo"),
			}),
			message("ConfirmPaymentReceivedRequest", []*descriptorpb.FieldDescriptorProto{
				field("trade_id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}),
			message("ConfirmPaymentReceivedReply", nil),
			message("AddressBalanceInfo", []*descriptorpb.FieldDescriptorProto{
				field("address", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("balance", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64),
				field("num_confirmations", 3, descriptorpb.FieldDescriptorProto_TYPE_INT64),
				field("is_address_unused", 4, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
			}),
			message("GetFundingAddressesRequest", nil),
			message("GetFundingAddressesReply", []*descriptorpb.FieldDescriptorProto{
				repeatedMessageField("address_balance_info", 1, ".io.bisq.protobuffer.AddressBalanceInfo"),
			}),
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: strptr("Offers"),
				Method: []*descriptorpb.MethodDescriptorProto{
					method("GetMyOffers", ".io.bisq.protobuffer.GetMyOffersRequest", ".io.bisq.protobuffer.GetMyOffersReply"),
					method("CreateOffer", ".io.bisq.protobuffer.CreateOfferRequest", ".io.bisq.protobuffer.CreateOfferReply"),
				},
			},
			{
				Name: strptr("PaymentAccounts"),
				Method: []*descriptorpb.MethodDescriptorProto{
					method("GetPaymentAccounts", ".io.bisq.protobuffer.GetPaymentAccountsRequest", ".io.bisq.protobuffer.GetPaymentAccountsReply"),
					method("CreateSepaPaymentAccount", ".io.bisq.protobuffer.CreateSepaPaymentAccountRequest", ".io.bisq.protobuffer.CreateSepaPaymentAccountReply"),
					method("DeletePaymentAccount", ".io.bisq.protobuffer.DeletePaymentAccountRequest", ".io.bisq.protobuffer.DeletePaymentAccountReply"),
				},
			},
			{
				Name: strptr("Trades"),
				Method: []*descriptorpb.MethodDescriptorProto{
					method("TakeOffer", ".io.bisq.protobuffer.TakeOfferRequest", ".io.bisq.protobuffer.TakeOfferReply"),
					method("GetTrade", ".io.bisq.protobuffer.GetTradeRequest", ".io.bisq.protobuffer.GetTradeReply"),
					method("GetTrades", ".io.bisq.protobuffer.GetTradesRequest", ".io.bisq.protobuffer.GetTradesReply"),
					method("ConfirmPaymentReceived", ".io.bisq.protobuffer.ConfirmPaymentReceivedRequest", ".io.bisq.protobuffer.ConfirmPaymentReceivedReply"),
				},
			},
			{
				Name: strptr("Wallets"),
				Method: []*descriptorpb.MethodDescriptorProto{
					method("GetFundingAddresses", ".io.bisq.protobuffer.GetFundingAddressesRequest", ".io.bisq.protobuffer.GetFundingAddressesReply"),
				},
			},
		},
	}
}

func message(name string, fields []*descriptorpb.FieldDescriptorProto, enums ...*descriptorpb.EnumDescriptorProto) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{
		Name:     strptr(name),
		Field:    fields,
		EnumType: enums,
	}
}

func field(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   strptr(name),
		Number: int32ptr(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   kind.Enum(),
	}
}

func messageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     strptr(name),
		Number:   int32ptr(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: strptr(typeName),
	}
}

func repeatedMessageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     strptr(name),
		Number:   int32ptr(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: strptr(typeName),
	}
}

func enumField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     strptr(name),
		Number:   int32ptr(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
		TypeName: strptr(typeName),
	}
}

func method(name, input, output string) *descriptorpb.MethodDescriptorProto {
	return &descriptorpb.MethodDescriptorProto{
		Name:       strptr(name),
		InputType:  strptr(input),
		OutputType: strptr(output),
	}
}

func strptr(v string) *string { return &v }
func int32ptr(v int32) *int32 { return &v }
