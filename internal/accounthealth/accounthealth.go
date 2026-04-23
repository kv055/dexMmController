package accounthealth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/cex/binance"
	"dexMmController/internal/exchanges/cex/coinbase"
	"dexMmController/internal/exchanges/cex/kraken"
	"dexMmController/internal/exchanges/dex/hodlhodl"
	"dexMmController/internal/exchanges/dex/robosats"
)

var ErrUnsupportedProvider = errors.New("unsupported account health provider")

type Repository interface {
	GetAccountByProvider(ctx context.Context, provider string) (*db.Account, error)
	GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error)
	UpdateAccountHealthCheckedAt(ctx context.Context, provider string, healthy bool) (time.Time, error)
}

type Config struct {
	HodlHodlBaseURL string
	BinanceBaseURL  string
	KrakenBaseURL   string
	CoinbaseBaseURL string
	RobosatsBaseURL string
}

type Checker struct {
	repo Repository
	cfg  Config
}

type Result struct {
	Provider  string    `json:"provider"`
	Status    string    `json:"status"`
	Healthy   bool      `json:"healthy"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
}

func NewChecker(repo Repository, cfg Config) *Checker {
	return &Checker{repo: repo, cfg: cfg}
}

func SupportedProviders() []string {
	return []string{"HodlHodl", "Robosats", "Binance", "Kraken", "Coinbase"}
}

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

	checkErr := c.checkActiveKeys(ctx, provider, keys)
	healthy := checkErr == nil
	checkedAt, updateErr := c.repo.UpdateAccountHealthCheckedAt(ctx, provider, healthy)
	if updateErr != nil {
		return Result{}, updateErr
	}

	message := fmt.Sprintf("%d active %s API key(s) validated.", len(keys), provider)
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
		CheckedAt: checkedAt,
	}, nil
}

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

func (c *Checker) checkActiveKeys(ctx context.Context, provider string, keys []db.APIKey) error {
	if len(keys) == 0 {
		return fmt.Errorf("%s: no active API keys configured", provider)
	}
	for _, key := range keys {
		if err := c.checkKey(ctx, provider, key); err != nil {
			return fmt.Errorf("%s API key #%d failed: %w", provider, key.ID, err)
		}
	}
	return nil
}

func (c *Checker) checkKey(ctx context.Context, provider string, key db.APIKey) error {
	apiKey := strings.TrimSpace(key.APIKey)
	apiSecret := strings.TrimSpace(key.APISecret)
	if apiKey == "" {
		return errors.New("api_key is required")
	}
	if provider == "Robosats" {
		client := robosats.NewClient(robosats.Config{
			BaseURL:   c.cfg.RobosatsBaseURL,
			AuthToken: apiKey,
		})
		_, err := client.GetRobot(ctx)
		return err
	}
	if apiSecret == "" {
		return errors.New("api_secret is required")
	}

	switch provider {
	case "HodlHodl":
		client := hodlhodl.NewClient(c.cfg.HodlHodlBaseURL, apiKey)
		if _, err := client.GetMe(ctx); err != nil {
			return err
		}
		return client.VerifySignatureKey(ctx, apiSecret)
	case "Binance":
		client := binance.NewClient(c.cfg.BinanceBaseURL, apiKey, apiSecret)
		_, err := client.AccountInfo(ctx)
		return err
	case "Kraken":
		client := kraken.NewClient(c.cfg.KrakenBaseURL, apiKey, apiSecret)
		_, err := client.Balance(ctx)
		return err
	case "Coinbase":
		client := coinbase.NewClient(c.cfg.CoinbaseBaseURL, apiKey, apiSecret)
		return client.KeyPermissions(ctx)
	default:
		return ErrUnsupportedProvider
	}
}

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
