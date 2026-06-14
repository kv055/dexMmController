package accounthealth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/cex/binance"
	"dexMmController/internal/exchanges/cex/coinbase"
	"dexMmController/internal/exchanges/cex/kraken"
	"dexMmController/internal/exchanges/dex/hodlhodl"
	"dexMmController/internal/exchanges/dex/robosats"
)

// ErrUnsupportedProvider is the err unsupported provider value.
var ErrUnsupportedProvider = errors.New("unsupported account health provider")

// Canonical CEX permission keys — a common schema reported uniformly for every
// centralized exchange so the UI can render a consistent, detailed set.
const (
	PermSpotTrading    = "spot_trading"
	PermReadBalances   = "read_balances"
	PermWithdraw       = "withdraw"
	PermHedgePositions = "hedge_positions"
)

// Permission status values (tri-state). "unsupported" means the exchange cannot
// report that capability (e.g. Kraken has no permissions API for futures), so the
// UI renders it as "n/a" rather than a false negative.
const (
	PermGranted     = "granted"
	PermDenied      = "denied"
	PermUnsupported = "unsupported"
)

// CanonicalCEXPermissions lists the canonical permission keys in display order.
func CanonicalCEXPermissions() []string {
	return []string{PermSpotTrading, PermReadBalances, PermWithdraw, PermHedgePositions}
}

// grantedIf maps a boolean capability flag to a tri-state permission status.
func grantedIf(ok bool) string {
	if ok {
		return PermGranted
	}
	return PermDenied
}

// Repository defines the behavior required by this package.
type Repository interface {
	GetAccountByProvider(ctx context.Context, provider string) (*db.Account, error)
	GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error)
	UpdateAPIKeyPermissions(ctx context.Context, keyID uint, permissions map[string]string, checkedAt time.Time) error
	UpdateAccountHealthCheckedAt(ctx context.Context, provider string, healthy bool) (time.Time, error)
}

// Config represents a config.
type Config struct {
	// HodlHodlBaseURL is the hodl hodl base url value for the config.
	HodlHodlBaseURL string
	// BinanceBaseURL is the binance base url value for the config.
	BinanceBaseURL string
	// KrakenBaseURL is the kraken base url value for the config.
	KrakenBaseURL string
	// CoinbaseBaseURL is the coinbase base url value for the config.
	CoinbaseBaseURL string
	// CoinbaseExchangeBaseURL is the Coinbase Exchange API base url (address book, HMAC auth).
	CoinbaseExchangeBaseURL string
	// RobosatsBaseURL is the robosats base url value for the config.
	RobosatsBaseURL string
	// TorProxyURL is an optional SOCKS proxy used to reach Tor-only
	// providers such as the RoboSats onion coordinators.
	TorProxyURL string
}

// Checker represents a checker.
type Checker struct {
	repo Repository
	cfg  Config
}

// KeyPermissionResult reports normalized permissions for one API key.
type KeyPermissionResult struct {
	// KeyID identifies the API key row.
	KeyID uint `json:"key_id"`
	// Name is the configured key label.
	Name string `json:"name"`
	// Permissions maps canonical permission names to tri-state status
	// ("granted" | "denied" | "unsupported").
	Permissions map[string]string `json:"permissions"`
}

// Result represents a result.
type Result struct {
	// Provider is the provider value for the result.
	Provider string `json:"provider"`
	// Status is the current status of the result.
	Status string `json:"status"`
	// Healthy reports whether the result condition is true.
	Healthy bool `json:"healthy"`
	// Message carries a human-readable message for the result.
	Message string `json:"message"`
	// Keys reports per-key permission details for CEX providers.
	Keys []KeyPermissionResult `json:"keys,omitempty"`
	// CheckedAt records the relevant time for the result.
	CheckedAt time.Time `json:"checked_at"`
}

// NewChecker creates a checker.
func NewChecker(repo Repository, cfg Config) *Checker {
	return &Checker{repo: repo, cfg: cfg}
}

// SupportedProviders returns the provider names checked by account health.
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

// CheckProvider checks a provider.
func (c *Checker) CheckProvider(ctx context.Context, rawProvider string) (Result, error) {
	provider, err := CanonicalProvider(rawProvider)
	if err != nil {
		return Result{}, err
	}
	if _, err := c.repo.GetAccountByProvider(ctx, provider); err != nil {
		return Result{}, err
	}

	keys, err := c.repo.GetActiveAPIKeysForProvider(ctx, provider)
	if err != nil {
		return Result{}, err
	}

	keyResults, checkErr := c.checkActiveKeys(ctx, provider, keys)
	healthy := checkErr == nil
	checkedAt, updateErr := c.repo.UpdateAccountHealthCheckedAt(ctx, provider, healthy)
	if updateErr != nil {
		return Result{}, updateErr
	}
	for _, keyResult := range keyResults {
		if len(keyResult.Permissions) == 0 {
			continue
		}
		if err := c.repo.UpdateAPIKeyPermissions(ctx, keyResult.KeyID, keyResult.Permissions, checkedAt); err != nil {
			return Result{}, err
		}
	}

	message := fmt.Sprintf("%d active %s API key(s) validated.", len(keys), provider)
	if len(keyResults) > 0 {
		message = message + " " + formatKeyPermissionsMessage(keyResults)
	}
	status := "healthy"
	if checkErr != nil {
		status = "unhealthy"
		message = checkErr.Error()
	}

	return Result{
		Provider:  provider,
		Status:    status,
		Healthy:   healthy,
		Message:   message,
		Keys:      keyResults,
		CheckedAt: checkedAt,
	}, nil
}

// CheckAll checks an all.
func (c *Checker) CheckAll(ctx context.Context) []Result {
	results := make([]Result, 0, len(SupportedProviders()))
	for _, provider := range SupportedProviders() {
		result, err := c.CheckProvider(ctx, provider)
		if err != nil {
			log.Printf("[AccountHealth] %s: %v", provider, err)
			continue
		}
		log.Printf("[AccountHealth] %s %s: %s", result.Provider, result.Status, result.Message)
		results = append(results, result)
	}
	return results
}

// RunPeriodic runs a periodic.
func (c *Checker) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	c.CheckAll(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.CheckAll(ctx)
		}
	}
}

func (c *Checker) checkActiveKeys(ctx context.Context, provider string, keys []db.APIKey) ([]KeyPermissionResult, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s: no active API keys configured", provider)
	}
	keyResults := make([]KeyPermissionResult, 0, len(keys))
	for _, key := range keys {
		permissions, err := c.checkKey(ctx, provider, key)
		if err != nil {
			return keyResults, fmt.Errorf("%s API key #%d failed: %w", provider, key.ID, err)
		}
		if permissions != nil {
			keyResults = append(keyResults, KeyPermissionResult{
				KeyID:       key.ID,
				Name:        keyDisplayName(key),
				Permissions: permissions,
			})
		}
	}
	return keyResults, nil
}

func (c *Checker) checkKey(ctx context.Context, provider string, key db.APIKey) (map[string]string, error) {
	apiKey := strings.TrimSpace(key.APIKey)
	apiSecret := strings.TrimSpace(key.APISecret)
	if apiKey == "" {
		return nil, errors.New("api_key is required")
	}
	if provider == "Robosats" {
		// Robot tokens are coordinator-specific: when no base URL is
		// configured, probe the known coordinators until one knows the robot.
		baseURLs := []string{c.cfg.RobosatsBaseURL}
		if strings.TrimSpace(c.cfg.RobosatsBaseURL) == "" {
			baseURLs = robosats.DefaultCoordinatorURLs
		}
		var errs []string
		for _, baseURL := range baseURLs {
			client := robosats.NewClient(robosats.Config{
				BaseURL:   baseURL,
				AuthToken: apiKey,
				ProxyURL:  c.cfg.TorProxyURL,
			})
			if _, err := client.GetRobot(ctx); err == nil {
				return map[string]string{"robot": PermGranted}, nil
			} else {
				errs = append(errs, fmt.Sprintf("%s: %v", shortHost(baseURL), err))
			}
		}
		return nil, errors.New(strings.Join(errs, "; "))
	}
	if apiSecret == "" {
		return nil, errors.New("api_secret is required")
	}

	switch provider {
	case "HodlHodl":
		client := hodlhodl.NewClient(c.cfg.HodlHodlBaseURL, apiKey)
		if _, err := client.GetMe(ctx); err != nil {
			return nil, err
		}
		if err := client.VerifySignatureKey(ctx, apiSecret); err != nil {
			return nil, err
		}
		return map[string]string{
			"api_key":       PermGranted,
			"signature_key": PermGranted,
		}, nil
	case "Binance":
		client := binance.NewClient(c.cfg.BinanceBaseURL, apiKey, apiSecret)
		restrictions, err := client.KeyRestrictions(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			PermSpotTrading:    grantedIf(restrictions.EnableSpotAndMarginTrading),
			PermReadBalances:   grantedIf(restrictions.EnableReading),
			PermWithdraw:       grantedIf(restrictions.EnableWithdrawals),
			PermHedgePositions: grantedIf(restrictions.EnableFutures),
		}, nil
	case "Kraken":
		return c.checkKrakenKey(ctx, apiKey, apiSecret)
	case "Coinbase":
		client := coinbase.NewClient(c.cfg.CoinbaseBaseURL, apiKey, apiSecret)
		permissions, err := client.KeyPermissions(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			PermSpotTrading:  grantedIf(permissions.CanTrade),
			PermReadBalances: grantedIf(permissions.CanView),
			PermWithdraw:     grantedIf(permissions.CanTransfer),
			// Coinbase derivatives ride on the trade scope + account eligibility;
			// there is no separate key scope to introspect.
			PermHedgePositions: PermUnsupported,
		}, nil
	default:
		return nil, ErrUnsupportedProvider
	}
}

// checkKrakenKey infers a Kraken key's permissions via safe capability probes,
// because Kraken exposes no permission-introspection endpoint. The Balance call
// also acts as the auth gate: a non-permission error there (bad signature/key)
// fails the whole check. Each probe is read-only or validate-only (no funds move).
func (c *Checker) checkKrakenKey(ctx context.Context, apiKey, apiSecret string) (map[string]string, error) {
	client := kraken.NewClient(c.cfg.KrakenBaseURL, apiKey, apiSecret)

	// Read balances — also validates the key signature.
	readStatus, err := krakenProbeStatus(probeBalanceErr(ctx, client))
	if err != nil {
		return nil, err
	}
	// Spot trading — validate-only AddOrder (places nothing).
	spotStatus, err := krakenProbeStatus(client.ValidateOrderPermission(ctx, "XBTEUR"))
	if err != nil {
		return nil, err
	}
	// Withdraw — listing withdraw methods requires the Withdraw Funds permission.
	withdrawStatus, err := krakenProbeStatus(probeWithdrawErr(ctx, client))
	if err != nil {
		return nil, err
	}

	return map[string]string{
		PermSpotTrading:  spotStatus,
		PermReadBalances: readStatus,
		PermWithdraw:     withdrawStatus,
		// Kraken Futures is a separate API with its own keys; not introspectable here.
		PermHedgePositions: PermUnsupported,
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

// krakenProbeStatus classifies a probe outcome: nil → granted, a Kraken
// "Permission denied" → denied (key valid, capability not granted), anything else
// (network/auth failure) → propagated so the health check surfaces the real error.
func krakenProbeStatus(err error) (string, error) {
	if err == nil {
		return PermGranted, nil
	}
	if strings.Contains(err.Error(), "Permission denied") {
		return PermDenied, nil
	}
	return "", err
}

// shortHost abbreviates a coordinator URL for error messages, since onion
// hostnames are 56 characters long.
func shortHost(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return baseURL
	}
	host := parsed.Host
	if len(host) > 14 {
		return host[:8] + "…" + host[len(host)-6:]
	}
	return host
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

// ParseInterval parses an interval.
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
