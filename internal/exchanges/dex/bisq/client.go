package bisq

import (
	"context"
	"errors"
	"fmt"
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

type Client struct {
	host     string
	password string

	conn    *grpc.ClientConn
	connErr error
	once    sync.Once
}

type Offer struct {
	ID                     string
	Direction              string
	Price                  string
	AmountSats             uint64
	MinAmountSats          uint64
	Volume                 string
	MinVolume              string
	PaymentAccountID       string
	PaymentMethodID        string
	PaymentMethodShortName string
	State                  string
	IsActivated            bool
	IsMyOffer              bool
}

type PaymentAccount struct {
	ID              string
	AccountName     string
	PaymentMethodID string
	TradeCurrency   string
}

type Trade struct {
	TradeID         string
	OfferID         string
	Role            string
	State           string
	Phase           string
	TradePrice      string
	TradeVolume     string
	TradeAmountSats uint64
	IsCompleted     bool
	HasFailed       bool
	ErrorMessage    string
	DepositTxID     string
	PayoutTxID      string
	CreatedAt       time.Time
}

type TakeOfferParams struct {
	PaymentAccountID     string
	TakerFeeCurrencyCode string
	AmountSats           uint64
}

type CreatePositionParams struct {
	CurrencyCode            string
	Direction               string
	Price                   string
	UseMarketBasedPrice     bool
	MarketPriceMarginPct    float64
	AmountSats              uint64
	MinAmountSats           uint64
	BuyerSecurityDepositPct float64
	TriggerPrice            string
	PaymentAccountID        string
	MakerFeeCurrencyCode    string
}

func NewClient(host, password string) *Client {
	if host == "" {
		host = defaultHost
	}
	return &Client{host: host, password: password}
}

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

func (c bisqCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"password": c.password}, nil
}

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
				},
			},
			{
				Name: strptr("Trades"),
				Method: []*descriptorpb.MethodDescriptorProto{
					method("TakeOffer", ".io.bisq.protobuffer.TakeOfferRequest", ".io.bisq.protobuffer.TakeOfferReply"),
					method("GetTrade", ".io.bisq.protobuffer.GetTradeRequest", ".io.bisq.protobuffer.GetTradeReply"),
					method("GetTrades", ".io.bisq.protobuffer.GetTradesRequest", ".io.bisq.protobuffer.GetTradesReply"),
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
