package cexaccount

import "context"

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
