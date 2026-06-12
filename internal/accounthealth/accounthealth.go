package accounthealth

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// Repository defines the behavior required by this package.
type Repository interface {
	GetAccountByProvider(ctx context.Context, provider string) (*db.Account, error)
	GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error)
	UpdateAPIKeyPermissions(ctx context.Context, keyID uint, permissions map[string]bool, checkedAt time.Time) error
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
	// RobosatsBaseURL is the robosats base url value for the config.
	RobosatsBaseURL string
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
	// Permissions maps normalized permission names to granted flags.
	Permissions map[string]bool `json:"permissions"`
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

func (c *Checker) checkKey(ctx context.Context, provider string, key db.APIKey) (map[string]bool, error) {
	apiKey := strings.TrimSpace(key.APIKey)
	apiSecret := strings.TrimSpace(key.APISecret)
	if apiKey == "" {
		return nil, errors.New("api_key is required")
	}
	if provider == "Robosats" {
		client := robosats.NewClient(robosats.Config{
			BaseURL:   c.cfg.RobosatsBaseURL,
			AuthToken: apiKey,
		})
		if _, err := client.GetRobot(ctx); err != nil {
			return nil, err
		}
		return map[string]bool{"robot": true}, nil
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
		return map[string]bool{
			"api_key":        true,
			"signature_key": true,
		}, nil
	case "Binance":
		client := binance.NewClient(c.cfg.BinanceBaseURL, apiKey, apiSecret)
		restrictions, err := client.KeyRestrictions(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]bool{
			"read":          restrictions.EnableReading,
			"trade":         restrictions.EnableSpotAndMarginTrading,
			"withdraw":      restrictions.EnableWithdrawals,
			"ip_restricted": restrictions.IPRestrict,
		}, nil
	case "Kraken":
		client := kraken.NewClient(c.cfg.KrakenBaseURL, apiKey, apiSecret)
		if _, err := client.Balance(ctx); err != nil {
			return nil, err
		}
		return map[string]bool{"read": true}, nil
	case "Coinbase":
		client := coinbase.NewClient(c.cfg.CoinbaseBaseURL, apiKey, apiSecret)
		permissions, err := client.KeyPermissions(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]bool{
			"view":     permissions.CanView,
			"trade":    permissions.CanTrade,
			"transfer": permissions.CanTransfer,
		}, nil
	default:
		return nil, ErrUnsupportedProvider
	}
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

func formatPermissions(permissions map[string]bool) string {
	names := make([]string, 0, len(permissions))
	for name := range permissions {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		mark := "✗"
		if permissions[name] {
			mark = "✓"
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
