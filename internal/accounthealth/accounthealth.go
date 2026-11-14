package accounthealth

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"dexTakerController/internal/cexaccount"
	"dexTakerController/internal/db"
	"dexTakerController/internal/dexaccount"
)

// Repository defines the behavior required by this package.
type Repository interface {
	GetAccountByProvider(ctx context.Context, provider string) (*db.Account, error)
	GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error)
	UpdateAPIKeyPermissions(ctx context.Context, keyID uint, permissions map[string]string, checkError string, checkedAt time.Time) error
	UpdateAccountHealthCheckedAt(ctx context.Context, provider string, healthy bool) (time.Time, error)
}

// Checker coordinates health checks across all providers.
type Checker struct {
	repo Repository
	cfg  cexaccount.Config
}

// NewChecker creates a checker.
func NewChecker(repo Repository, cfg cexaccount.Config) *Checker {
	return &Checker{repo: repo, cfg: cfg}
}

// CheckProvider checks a provider.
func (c *Checker) CheckProvider(ctx context.Context, rawProvider string) (cexaccount.HealthResult, error) {
	provider, err := cexaccount.CanonicalProvider(rawProvider)
	if err != nil {
		return cexaccount.HealthResult{}, err
	}
	if _, err := c.repo.GetAccountByProvider(ctx, provider); err != nil {
		return cexaccount.HealthResult{}, err
	}

	keys, err := c.repo.GetActiveAPIKeysForProvider(ctx, provider)
	if err != nil {
		return cexaccount.HealthResult{}, err
	}

	keyResults, checkErr := c.checkActiveKeys(ctx, provider, keys)
	if cexaccount.IsCEXProvider(provider) {
		_, checkErr = cexaccount.AssessCEXPermissions(provider, keyResults)
	}
	healthy := checkErr == nil
	checkedAt, updateErr := c.repo.UpdateAccountHealthCheckedAt(ctx, provider, healthy)
	if updateErr != nil {
		return cexaccount.HealthResult{}, updateErr
	}
	for _, keyResult := range keyResults {
		if err := c.repo.UpdateAPIKeyPermissions(ctx, keyResult.KeyID, keyResult.Permissions, keyResult.Error, checkedAt); err != nil {
			return cexaccount.HealthResult{}, err
		}
	}

	message := fmt.Sprintf("%d active %s API key(s) validated.", len(keys), provider)
	status := "healthy"
	if checkErr != nil {
		status = "unhealthy"
		message = checkErr.Error()
	}

	return cexaccount.HealthResult{
		Provider:  provider,
		Status:    status,
		Healthy:   healthy,
		Message:   message,
		Keys:      keyResults,
		CheckedAt: checkedAt,
	}, nil
}

// CheckAll checks all supported providers.
func (c *Checker) CheckAll(ctx context.Context) []cexaccount.HealthResult {
	results := make([]cexaccount.HealthResult, 0, len(cexaccount.SupportedProviders()))
	for _, provider := range cexaccount.SupportedProviders() {
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

// RunPeriodic runs health checks on a recurring interval.
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

func (c *Checker) checkActiveKeys(ctx context.Context, provider string, keys []db.APIKey) ([]cexaccount.KeyPermissionResult, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s: no active API keys configured", provider)
	}
	keyResults := make([]cexaccount.KeyPermissionResult, 0, len(keys))
	for _, key := range keys {
		permissions, err := c.checkKey(ctx, provider, key)
		result := cexaccount.KeyPermissionResult{KeyID: key.ID, Name: keyDisplayName(key), Permissions: permissions}
		if err != nil {
			result.Error = err.Error()
		}
		keyResults = append(keyResults, result)
	}
	for _, result := range keyResults {
		if result.Error != "" {
			return keyResults, fmt.Errorf("%s API key checks failed", provider)
		}
	}
	return keyResults, nil
}

func (c *Checker) checkKey(ctx context.Context, provider string, key db.APIKey) (map[string]string, error) {
	if cexaccount.IsCEXProvider(provider) {
		return cexaccount.CheckCEXKey(ctx, c.cfg, provider, key)
	}
	return dexaccount.CheckDEXKey(ctx, c.cfg, provider, key)
}

func keyDisplayName(key db.APIKey) string {
	name := strings.TrimSpace(key.Name)
	if name == "" {
		return fmt.Sprintf("key %d", key.ID)
	}
	return name
}
