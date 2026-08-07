package cexaccount

import (
	"context"
	"fmt"
)

// FiatControllerChecker verifies every notification channel selected on the
// account linked to the resolved canonical payment method.
type FiatControllerChecker interface {
	Check(ctx context.Context, paymentMethod string) error
}

// FiatControllerHealthChecker exposes the Fiat Controller's two independent
// pre-trade safety checks. Implementations that only satisfy FiatControllerChecker
// remain supported as a single, combined notification check.
type FiatControllerHealthChecker interface {
	CheckService(ctx context.Context) error
	CheckNotificationHealth(ctx context.Context, paymentMethod string) error
}

// BalanceFetcher returns the available fiat balance for a CEX provider and currency.
type BalanceFetcher interface {
	FiatAvailable(ctx context.Context, provider, currency string) (float64, error)
}

// WhitelistFetcher returns whitelisted BTC withdrawal addresses for a CEX provider.
type WhitelistFetcher interface {
	WhitelistedBTCAddresses(ctx context.Context, provider string) ([]string, error)
}

// WalletSource returns the current on-chain addresses of the local wallet.
type WalletSource interface {
	OnChainAddresses(ctx context.Context) ([]string, error)
}

// BTCBalanceSource returns the confirmed on-chain BTC balance of the local
// wallet (LND hub). Used by the hedgeless liquidity-pool pre-trade check.
type BTCBalanceSource interface {
	ConfirmedOnChainBTC(ctx context.Context) (float64, error)
}

// BTCBalanceSourceFunc implements BTCBalanceSource as a plain function.
type BTCBalanceSourceFunc func(ctx context.Context) (float64, error)

func (f BTCBalanceSourceFunc) ConfirmedOnChainBTC(ctx context.Context) (float64, error) {
	return f(ctx)
}

// LightningBalanceSource returns aggregate channel balances.
// Outbound is local (spendable); inbound is remote (receivable).
type LightningBalanceSource interface {
	OutboundLightningBTC(ctx context.Context) (float64, error)
	InboundLightningBTC(ctx context.Context) (float64, error)
}

// LightningBalanceSourceFuncs implements LightningBalanceSource as plain functions.
type LightningBalanceSourceFuncs struct {
	OutboundFn func(ctx context.Context) (float64, error)
	InboundFn  func(ctx context.Context) (float64, error)
}

func (f LightningBalanceSourceFuncs) OutboundLightningBTC(ctx context.Context) (float64, error) {
	if f.OutboundFn == nil {
		return 0, fmt.Errorf("LND outbound Lightning balance is not available")
	}
	return f.OutboundFn(ctx)
}

func (f LightningBalanceSourceFuncs) InboundLightningBTC(ctx context.Context) (float64, error) {
	if f.InboundFn == nil {
		return 0, fmt.Errorf("LND inbound Lightning balance is not available")
	}
	return f.InboundFn(ctx)
}

// LightningChannelSource reports whether at least one active open channel exists.
type LightningChannelSource interface {
	HasActiveChannel(ctx context.Context) (bool, error)
}

// LightningChannelSourceFunc implements LightningChannelSource as a plain function.
type LightningChannelSourceFunc func(ctx context.Context) (bool, error)

func (f LightningChannelSourceFunc) HasActiveChannel(ctx context.Context) (bool, error) {
	return f(ctx)
}

// LNDOnlineSource verifies the LND wallet RPC is reachable and the wallet is
// unlocked — the minimum bar for CEX→LND withdrawals and hedgeless pool checks.
type LNDOnlineSource interface {
	CheckOnline(ctx context.Context) error
}

// LNDOnlineSourceFunc implements LNDOnlineSource as a plain function.
type LNDOnlineSourceFunc func(ctx context.Context) error

func (f LNDOnlineSourceFunc) CheckOnline(ctx context.Context) error {
	return f(ctx)
}

// CommunicationGateway reports whether the operator can be reached before an
// automated trade starts.
type CommunicationGateway interface {
	Enabled() bool
}

// BalanceFetcherFunc implements BalanceFetcher as a plain function.
type BalanceFetcherFunc func(ctx context.Context, provider, currency string) (float64, error)

func (f BalanceFetcherFunc) FiatAvailable(ctx context.Context, provider, currency string) (float64, error) {
	return f(ctx, provider, currency)
}

// WhitelistFetcherFunc implements WhitelistFetcher as a plain function.
type WhitelistFetcherFunc func(ctx context.Context, provider string) ([]string, error)

func (f WhitelistFetcherFunc) WhitelistedBTCAddresses(ctx context.Context, provider string) ([]string, error) {
	return f(ctx, provider)
}

// WalletSourceFunc implements WalletSource as a plain function.
type WalletSourceFunc func(ctx context.Context) ([]string, error)

func (f WalletSourceFunc) OnChainAddresses(ctx context.Context) ([]string, error) {
	return f(ctx)
}

// PermissionChecker returns cached CEX API key permissions for a provider.
type PermissionChecker interface {
	CEXPermissions(ctx context.Context, provider string) (map[string]string, error)
}

// PermissionCheckerFunc implements PermissionChecker as a plain function.
type PermissionCheckerFunc func(ctx context.Context, provider string) (map[string]string, error)

func (f PermissionCheckerFunc) CEXPermissions(ctx context.Context, provider string) (map[string]string, error) {
	return f(ctx, provider)
}

// PaymentMethodSnapshot is the subset of a payment_methods catalog row used by
// pre-trade checks. Category holds the DB display value (e.g. "Fiat
// Transmitter"); AccountID is the row's accounts foreign key.
type PaymentMethodSnapshot struct {
	Name      string
	Category  string
	AccountID *int64
	IsActive  bool
	IsLinked  bool
	Priority  int
}

// FiatAccountSnapshot is the subset of a fiat transmitter account used by pre-trade checks.
type FiatAccountSnapshot struct {
	Provider   string
	Email      string
	Phone      string
	PaymentTag string
}

// BankAccountSnapshot is the subset of a bank account used by pre-trade checks.
type BankAccountSnapshot struct {
	Scheme   string
	Details  map[string]string
	IsActive bool
}

// PaymentSetupFetcher loads payment_methods catalog rows and the payment
// solutions they link to. Lookups return (nil, nil) when the row does not
// exist; errors are reserved for real failures.
type PaymentSetupFetcher interface {
	PaymentMethodByName(ctx context.Context, name string) (*PaymentMethodSnapshot, error)
	// PickPaymentMethod returns the linked+active method with the highest
	// priority among names, or (nil, nil) when none is eligible.
	PickPaymentMethod(ctx context.Context, names []string) (*PaymentMethodSnapshot, error)
	FiatAccountByID(ctx context.Context, id int64) (*FiatAccountSnapshot, error)
	// BankAccountForAccount returns one bank_accounts row for the linked
	// accounts.id (preferring active rows, lowest id).
	BankAccountForAccount(ctx context.Context, accountID int64) (*BankAccountSnapshot, error)
}

// ChatTemplateFetcher loads the configured chat-request template for a payment
// method on a chat-style DEX venue (RoboSats, Bisq2). Satisfied directly by
// db.TransactionRepository.
type ChatTemplateFetcher interface {
	GetChatTemplate(ctx context.Context, exchange, paymentMethodName string) (string, error)
}

// PaymentSetupFetcherFuncs implements PaymentSetupFetcher as plain functions.
type PaymentSetupFetcherFuncs struct {
	MethodFn func(ctx context.Context, name string) (*PaymentMethodSnapshot, error)
	PickFn   func(ctx context.Context, names []string) (*PaymentMethodSnapshot, error)
	FiatFn   func(ctx context.Context, id int64) (*FiatAccountSnapshot, error)
	BankFn   func(ctx context.Context, accountID int64) (*BankAccountSnapshot, error)
}

func (f PaymentSetupFetcherFuncs) PaymentMethodByName(ctx context.Context, name string) (*PaymentMethodSnapshot, error) {
	if f.MethodFn == nil {
		return nil, fmt.Errorf("payment method fetcher not configured")
	}
	return f.MethodFn(ctx, name)
}

func (f PaymentSetupFetcherFuncs) PickPaymentMethod(ctx context.Context, names []string) (*PaymentMethodSnapshot, error) {
	if f.PickFn == nil {
		return nil, fmt.Errorf("payment method picker not configured")
	}
	return f.PickFn(ctx, names)
}

func (f PaymentSetupFetcherFuncs) FiatAccountByID(ctx context.Context, id int64) (*FiatAccountSnapshot, error) {
	if f.FiatFn == nil {
		return nil, fmt.Errorf("fiat transmitter account fetcher not configured")
	}
	return f.FiatFn(ctx, id)
}

func (f PaymentSetupFetcherFuncs) BankAccountForAccount(ctx context.Context, accountID int64) (*BankAccountSnapshot, error) {
	if f.BankFn == nil {
		return nil, fmt.Errorf("bank account fetcher not configured")
	}
	return f.BankFn(ctx, accountID)
}
