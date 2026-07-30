package cexaccount

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/cex/binance"
	"dexMmController/internal/exchangeconnectors/cex/coinbase"
	"dexMmController/internal/exchangeconnectors/cex/kraken"
)

var ErrUnsupportedProvider = errors.New("unsupported account health provider")

const (
	PermFetchDepositAddress = "fetch_deposit_address"
	PermSpotTrading         = "spot_trading"
	PermReadBalances        = "read_balances"
	PermWithdraw            = "withdraw"
	PermHedgePositions      = "hedge_positions"
)

const (
	PermGranted     = "granted"
	PermDenied      = "denied"
	PermUnsupported = "unsupported"
)

// Config holds base URLs for all exchange connectors.
type Config struct {
	HodlHodlBaseURL         string
	BinanceBaseURL          string
	KrakenBaseURL           string
	CoinbaseBaseURL         string
	CoinbaseExchangeBaseURL string
	RobosatsBaseURL         string
	TorProxyURL             string
}

// CanonicalProvider maps provider aliases to their canonical names.
func CanonicalProvider(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "hodlhodl", "hodl hodl":
		return "HodlHodl", nil
	case "robosats", "robo sats":
		return "Robosats", nil
	case "binance":
		return "Binance", nil
	case "kraken":
		return "Kraken", nil
	case "coinbase":
		return "Coinbase", nil
	default:
		return "", ErrUnsupportedProvider
	}
}

// SupportedProviders returns all provider names (DEX + CEX).
func SupportedProviders() []string {
	return []string{"HodlHodl", "Robosats", "Binance", "Kraken", "Coinbase"}
}

// SupportedCEXProviders returns the centralized exchange providers.
func SupportedCEXProviders() []string {
	return []string{"Binance", "Kraken", "Coinbase"}
}

// IsCEXProvider reports whether the canonical provider is a supported CEX.
func IsCEXProvider(provider string) bool {
	switch provider {
	case "Binance", "Kraken", "Coinbase":
		return true
	default:
		return false
	}
}

// CanonicalCEXPermissions lists the canonical permission keys in display order.
func CanonicalCEXPermissions() []string {
	return []string{PermFetchDepositAddress, PermSpotTrading, PermReadBalances, PermWithdraw, PermHedgePositions}
}

// KeyPermissionResult reports normalized permissions for one API key.
type KeyPermissionResult struct {
	KeyID       uint              `json:"key_id"`
	Name        string            `json:"name"`
	Permissions map[string]string `json:"permissions"`
	Error       string            `json:"error,omitempty"`
}

// HealthResult is the outcome of a single provider health check.
type HealthResult struct {
	Provider  string                `json:"provider"`
	Status    string                `json:"status"`
	Healthy   bool                  `json:"healthy"`
	Message   string                `json:"message"`
	Keys      []KeyPermissionResult `json:"keys,omitempty"`
	CheckedAt time.Time             `json:"checked_at"`
}

// AssessCEXPermissions combines every active key's cached permissions. All
// canonical permissions must be granted by at least one key; an unreadable key
// also blocks trading so operators never proceed with incomplete validation.
func AssessCEXPermissions(provider string, keys []KeyPermissionResult) (map[string]string, error) {
	combined := make(map[string]string, len(CanonicalCEXPermissions()))
	keyErrors := make([]string, 0)
	for _, key := range keys {
		if key.Error != "" {
			keyErrors = append(keyErrors, fmt.Sprintf("key %q could not be checked: %s", key.Name, key.Error))
		}
		for _, permission := range CanonicalCEXPermissions() {
			if key.Permissions[permission] == PermGranted {
				combined[permission] = PermGranted
			}
		}
	}

	missing := make([]string, 0)
	for _, permission := range CanonicalCEXPermissions() {
		if combined[permission] == PermGranted {
			continue
		}
		status := PermDenied
		for _, key := range keys {
			if candidate := key.Permissions[permission]; candidate == PermUnsupported {
				status = PermUnsupported
			} else if candidate == "" && status != PermUnsupported {
				status = "unavailable"
			}
		}
		combined[permission] = status
		missing = append(missing, fmt.Sprintf("%s (%s)", permission, status))
	}
	if len(keyErrors) == 0 && len(missing) == 0 {
		return combined, nil
	}
	parts := make([]string, 0, 2)
	if len(missing) > 0 {
		parts = append(parts, "missing permissions: "+strings.Join(missing, ", "))
	}
	parts = append(parts, keyErrors...)
	return combined, fmt.Errorf("%s API key permissions failed: %s", provider, strings.Join(parts, "; "))
}

// ParseInterval parses a duration string, defaulting to 10 minutes.
func ParseInterval(raw string) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return 10 * time.Minute
	}
	interval, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || interval <= 0 {
		return 10 * time.Minute
	}
	return interval
}

func grantedIf(ok bool) string {
	if ok {
		return PermGranted
	}
	return PermDenied
}

func keyDisplayName(key db.APIKey) string {
	name := strings.TrimSpace(key.Name)
	if name == "" {
		return fmt.Sprintf("key %d", key.ID)
	}
	return name
}

func formatKeyPermissionsMessage(keys []KeyPermissionResult) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`key %q: %s`, key.Name, formatPermissions(key.Permissions)))
	}
	return strings.Join(parts, "; ")
}

func formatPermissions(permissions map[string]string) string {
	names := make([]string, 0, len(permissions))
	for name := range permissions {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		var mark string
		switch permissions[name] {
		case PermGranted:
			mark = "✓"
		case PermUnsupported:
			mark = "—"
		default:
			mark = "✗"
		}
		parts = append(parts, fmt.Sprintf("%s %s", name, mark))
	}
	return strings.Join(parts, " ")
}

// CheckCEXKey validates a single CEX API key and returns its permissions.
func CheckCEXKey(ctx context.Context, cfg Config, provider string, key db.APIKey) (map[string]string, error) {
	apiKey := strings.TrimSpace(key.APIKey)
	apiSecret := strings.TrimSpace(key.APISecret)
	if apiKey == "" {
		return nil, errors.New("api_key is required")
	}
	if apiSecret == "" {
		return nil, errors.New("api_secret is required")
	}

	switch provider {
	case "Binance":
		client := binance.NewClient(cfg.BinanceBaseURL, apiKey, apiSecret)
		restrictions, err := client.KeyRestrictions(ctx)
		if err != nil {
			return nil, err
		}
		depositStatus, err := httpPermissionProbeStatus(client.GetDepositAddress(ctx, "BTC", "BTC"))
		if err != nil {
			return nil, err
		}
		return map[string]string{
			PermFetchDepositAddress: depositStatus,
			PermSpotTrading:         grantedIf(restrictions.EnableSpotAndMarginTrading),
			PermReadBalances:        grantedIf(restrictions.EnableReading),
			PermWithdraw:            grantedIf(restrictions.EnableWithdrawals),
			PermHedgePositions:      grantedIf(restrictions.EnableFutures),
		}, nil
	case "Kraken":
		return checkKrakenKey(ctx, cfg, apiKey, apiSecret)
	case "Coinbase":
		client := coinbase.NewClient(cfg.CoinbaseBaseURL, apiKey, apiSecret)
		permissions, err := client.KeyPermissions(ctx)
		if err != nil {
			return nil, err
		}
		depositStatus, err := httpPermissionProbeStatus(client.CheckBTCDepositAddressAccess(ctx))
		if err != nil {
			return nil, err
		}
		return map[string]string{
			PermFetchDepositAddress: depositStatus,
			PermSpotTrading:         grantedIf(permissions.CanTrade),
			PermReadBalances:        grantedIf(permissions.CanView),
			PermWithdraw:            grantedIf(permissions.CanTransfer),
			PermHedgePositions:      PermUnsupported,
		}, nil
	default:
		return nil, ErrUnsupportedProvider
	}
}

func checkKrakenKey(ctx context.Context, cfg Config, apiKey, apiSecret string) (map[string]string, error) {
	client := kraken.NewClient(cfg.KrakenBaseURL, apiKey, apiSecret)

	readStatus, err := krakenProbeStatus(probeBalanceErr(ctx, client))
	if err != nil {
		return nil, err
	}
	spotStatus, err := krakenProbeStatus(client.ValidateOrderPermission(ctx, "XBTEUR"))
	if err != nil {
		return nil, err
	}
	withdrawStatus, err := krakenProbeStatus(probeWithdrawErr(ctx, client))
	if err != nil {
		return nil, err
	}

	depositStatus, err := krakenProbeStatus(probeDepositAddressErr(ctx, client))
	if err != nil {
		return nil, err
	}

	return map[string]string{
		PermFetchDepositAddress: depositStatus,
		PermSpotTrading:         spotStatus,
		PermReadBalances:        readStatus,
		PermWithdraw:            withdrawStatus,
		PermHedgePositions:      PermUnsupported,
	}, nil
}

func probeBalanceErr(ctx context.Context, client *kraken.Client) error {
	_, err := client.Balance(ctx)
	return err
}

func probeWithdrawErr(ctx context.Context, client *kraken.Client) error {
	_, err := client.GetWithdrawMethods(ctx, "XBT")
	return err
}

func probeDepositAddressErr(ctx context.Context, client *kraken.Client) error {
	methods, err := client.GetDepositMethods(ctx, "XBT")
	if err != nil {
		return err
	}
	if len(methods) == 0 {
		return nil
	}
	_, err = client.GetDepositAddresses(ctx, "XBT", methods[0].Method, false)
	return err
}

func httpPermissionProbeStatus(_ any, err error) (string, error) {
	if err == nil {
		return PermGranted, nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "http 401") || strings.Contains(message, "http 403") ||
		strings.Contains(message, "permission") || strings.Contains(message, "unauthorized") ||
		strings.Contains(message, "forbidden") {
		return PermDenied, nil
	}
	return "", err
}

func krakenProbeStatus(err error) (string, error) {
	if err == nil {
		return PermGranted, nil
	}
	if strings.Contains(err.Error(), "Permission denied") {
		return PermDenied, nil
	}
	return "", err
}
