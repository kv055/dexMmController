package cexaccount

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"commons/mmcapabilities"
	"dexMmController/internal/db"
	"dexMmController/internal/exchangeconnectors/cex/binance"
	"dexMmController/internal/exchangeconnectors/cex/coinbase"
	"dexMmController/internal/exchangeconnectors/cex/kraken"
)

var ErrUnsupportedProvider = errors.New("unsupported account health provider")

const (
	PermFetchDepositAddress = mmcapabilities.PermFetchDepositAddress
	PermSpotTrading         = mmcapabilities.PermSpotTrading
	PermReadBalances        = mmcapabilities.PermReadBalances
	PermWithdraw            = mmcapabilities.PermWithdraw
	PermHedgePositions      = mmcapabilities.PermHedgePositions
)

const (
	PermGranted     = mmcapabilities.StatusGranted
	PermDenied      = mmcapabilities.StatusDenied
	PermUnsupported = mmcapabilities.StatusUnsupported
)

// Config holds base URLs for all exchange connectors.
type Config struct {
	HodlHodlBaseURL            string
	BinanceBaseURL             string
	KrakenBaseURL              string
	KrakenFuturesBaseURL       string
	CoinbaseBaseURL            string
	CoinbaseINTXBaseURL        string
	CoinbaseBeneficiaryName    string
	CoinbaseBeneficiaryCountry string
	CoinbaseLNDWithdrawAddress string
	RobosatsBaseURL            string
	TorProxyURL                string
}

// CanonicalProvider maps provider aliases to their canonical names.
func CanonicalProvider(raw string) (string, error) {
	if provider, ok := mmcapabilities.ProviderByID(raw); ok && provider.ExecutionSupported {
		return provider.Name, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "hodlhodl", "hodl hodl":
		return "HodlHodl", nil
	case "robosats", "robo sats":
		return "Robosats", nil
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
	return mmcapabilities.ExecutionProviders()
}

// IsCEXProvider reports whether the canonical provider is a supported CEX.
func IsCEXProvider(provider string) bool {
	definition, ok := mmcapabilities.ProviderByID(provider)
	return ok && definition.ExecutionSupported
}

// CanonicalCEXPermissions lists the canonical permission keys in display order.
func CanonicalCEXPermissions() []string {
	return mmcapabilities.CanonicalPermissions()
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
// canonical permissions must be granted by at least one key. Keys that failed
// inspection are reported in the error when permissions are still missing, but
// they do not fail the check once complementary keys already cover every
// permission (e.g. a revoked spot key next to a healthy futures key).
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
			if key.Error != "" {
				continue
			}
			if candidate := key.Permissions[permission]; candidate == PermUnsupported {
				status = PermUnsupported
			} else if candidate == "" && status != PermUnsupported {
				status = "unavailable"
			}
		}
		combined[permission] = status
		missing = append(missing, fmt.Sprintf("%s (%s)", permission, status))
	}
	if len(missing) == 0 {
		return combined, nil
	}
	parts := make([]string, 0, 3)
	parts = append(parts, "missing permissions: "+strings.Join(missing, ", "))
	parts = append(parts, keyErrors...)
	if provider == "Kraken" && combined[PermHedgePositions] != PermGranted {
		parts = append(parts, "Kraken futures requires a separate Futures trading API key (not a Spot key); create one in Kraken Pro → Settings → Futures trading API with General=Full Access")
	}
	if provider == "Kraken" && (combined[PermSpotTrading] != PermGranted || combined[PermReadBalances] != PermGranted) {
		parts = append(parts, "Kraken spot requires a Spot trading API key; deactivate invalid keys and keep an active Spot key alongside the Futures key")
	}
	if provider == "Coinbase" && combined[PermHedgePositions] != PermGranted {
		parts = append(parts, "Coinbase futures (INTX) requires a separate International Exchange API key with passphrase; create one in Coinbase International → API, then add it as an active Coinbase key alongside the CDP spot key")
	}
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
		// Binance deposit-address endpoints are USER_DATA (Enable Reading).
		return map[string]string{
			PermFetchDepositAddress: grantedIf(restrictions.EnableReading),
			PermSpotTrading:         grantedIf(restrictions.EnableSpotAndMarginTrading),
			PermReadBalances:        grantedIf(restrictions.EnableReading),
			PermWithdraw:            grantedIf(restrictions.EnableWithdrawals),
			PermHedgePositions:      grantedIf(restrictions.EnableFutures),
		}, nil
	case "Kraken":
		return checkKrakenKey(ctx, cfg, apiKey, apiSecret)
	case "Coinbase":
		return checkCoinbaseKey(ctx, cfg, key)
	default:
		return nil, ErrUnsupportedProvider
	}
}

func checkCoinbaseKey(ctx context.Context, cfg Config, key db.APIKey) (map[string]string, error) {
	// Mirror Kraken: probe every surface for this keypair and always return a full
	// map. CDP JWT keys cover spot; INTX HMAC+passphrase keys cover hedge. Soft
	// failures become denied so complementary keys can be unioned by Assess.
	apiKey := strings.TrimSpace(key.APIKey)
	apiSecret := strings.TrimSpace(key.APISecret)
	passphrase := strings.TrimSpace(key.Passphrase)

	spot := coinbase.NewClient(cfg.CoinbaseBaseURL, apiKey, apiSecret)
	canTrade := false
	canTransfer := false
	if permissions, err := spot.KeyPermissions(ctx); err == nil && permissions != nil {
		canTrade = permissions.CanTrade
		canTransfer = permissions.CanTransfer
	}

	hedgeStatus := PermDenied
	if passphrase != "" {
		futures := coinbase.NewFuturesClientWithBaseURL(cfg.CoinbaseINTXBaseURL, apiKey, apiSecret, passphrase)
		hedgeStatus = coinbaseProbeStatus(futures.CheckAccess(ctx))
	}

	return map[string]string{
		PermFetchDepositAddress: coinbaseProbeStatus(func() error {
			_, err := spot.BTCDepositAddresses(ctx)
			return err
		}()),
		PermReadBalances: coinbaseProbeStatus(func() error {
			_, err := spot.ListAccounts(ctx)
			return err
		}()),
		PermSpotTrading:    grantedIf(canTrade),
		PermWithdraw:       grantedIf(canTransfer),
		PermHedgePositions: hedgeStatus,
	}, nil
}

func coinbaseProbeStatus(err error) string {
	if err == nil {
		return PermGranted
	}
	return PermDenied
}

func checkKrakenKey(ctx context.Context, cfg Config, apiKey, apiSecret string) (map[string]string, error) {
	// Try every permission endpoint for this keypair. Success → granted; any probe
	// failure (wrong venue, permission denied, lockout, decode, HTTP error) → denied.
	// Always return a complete map so complementary spot+futures keys can be unioned.
	spot := kraken.NewClient(cfg.KrakenBaseURL, apiKey, apiSecret)
	futures := kraken.NewFuturesClientWithBaseURL(cfg.KrakenFuturesBaseURL, apiKey, apiSecret)

	return map[string]string{
		PermFetchDepositAddress: krakenProbeStatus(func() error {
			_, err := spot.GetDepositMethods(ctx, "XBT")
			return err
		}()),
		PermReadBalances: krakenProbeStatus(func() error {
			_, err := spot.Balance(ctx)
			return err
		}()),
		PermSpotTrading: krakenProbeStatus(spot.ValidateOrderPermission(ctx, "XBTEUR")),
		PermWithdraw: krakenProbeStatus(func() error {
			_, err := spot.GetWithdrawMethods(ctx, "XBT")
			return err
		}()),
		PermHedgePositions: krakenFuturesProbeStatus(futures.CheckAPIKey(ctx)),
	}, nil
}

func krakenFuturesProbeStatus(info *kraken.FuturesAPIKeyInfo, err error) string {
	if err != nil || info == nil {
		return PermDenied
	}
	return grantedIf(strings.EqualFold(info.Permissions.General, kraken.FuturesAccessFull))
}

func krakenProbeStatus(err error) string {
	if err == nil {
		return PermGranted
	}
	return PermDenied
}
