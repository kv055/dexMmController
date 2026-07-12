package cexaccount

import (
	"context"
	"fmt"
)

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
// Transmitter"); the link IDs are the row's foreign keys.
type PaymentMethodSnapshot struct {
	Name            string
	Category        string
	BankAccountID   *int64
	AccountID       *int64
	WalletAddressID *int64
	IsActive        bool
	IsLinked        bool
	Priority        int
}

// FiatAccountSnapshot is the subset of a fiat transmitter account used by pre-trade checks.
type FiatAccountSnapshot struct {
	Provider string
	Email    string
	Phone    string
}

// BankAccountSnapshot is the subset of a bank account used by pre-trade checks.
type BankAccountSnapshot struct {
	Scheme   string
	Details  map[string]string
	IsActive bool
}

// WalletAddressSnapshot is the subset of a wallet_addresses row used by pre-trade checks.
type WalletAddressSnapshot struct {
	Label   string
	Address string
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
	BankAccountByID(ctx context.Context, id int64) (*BankAccountSnapshot, error)
	WalletAddressByID(ctx context.Context, id int64) (*WalletAddressSnapshot, error)
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
	BankFn   func(ctx context.Context, id int64) (*BankAccountSnapshot, error)
	WalletFn func(ctx context.Context, id int64) (*WalletAddressSnapshot, error)
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

func (f PaymentSetupFetcherFuncs) BankAccountByID(ctx context.Context, id int64) (*BankAccountSnapshot, error) {
	if f.BankFn == nil {
		return nil, fmt.Errorf("bank account fetcher not configured")
	}
	return f.BankFn(ctx, id)
}

func (f PaymentSetupFetcherFuncs) WalletAddressByID(ctx context.Context, id int64) (*WalletAddressSnapshot, error) {
	if f.WalletFn == nil {
		return nil, fmt.Errorf("wallet address fetcher not configured")
	}
	return f.WalletFn(ctx, id)
}
