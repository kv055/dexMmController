package dexaccount

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"dexTakerController/internal/cexaccount"
	"dexTakerController/internal/db"
	"dexTakerController/internal/exchangeconnectors/dex/hodlhodl"
	"dexTakerController/internal/exchangeconnectors/dex/robosats"
)

// CheckDEXKey validates a DEX API key by probing the provider's API.
func CheckDEXKey(ctx context.Context, cfg cexaccount.Config, provider string, key db.APIKey) (map[string]string, error) {
	apiKey := strings.TrimSpace(key.APIKey)
	if apiKey == "" {
		return nil, errors.New("api_key is required")
	}

	switch provider {
	case "Robosats":
		return checkRobosatsKey(ctx, cfg, apiKey)
	case "HodlHodl":
		return checkHodlHodlKey(ctx, cfg, key)
	default:
		return nil, cexaccount.ErrUnsupportedProvider
	}
}

func checkRobosatsKey(ctx context.Context, cfg cexaccount.Config, apiKey string) (map[string]string, error) {
	baseURLs := []string{cfg.RobosatsBaseURL}
	if strings.TrimSpace(cfg.RobosatsBaseURL) == "" {
		baseURLs = robosats.DefaultCoordinatorURLs
	}
	var errs []string
	for _, baseURL := range baseURLs {
		client := robosats.NewClient(robosats.Config{
			BaseURL:   baseURL,
			AuthToken: apiKey,
			ProxyURL:  cfg.TorProxyURL,
		})
		if _, err := client.GetRobot(ctx); err == nil {
			return map[string]string{"robot": cexaccount.PermGranted}, nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", shortHost(baseURL), err))
		}
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

func checkHodlHodlKey(ctx context.Context, cfg cexaccount.Config, key db.APIKey) (map[string]string, error) {
	apiKey := strings.TrimSpace(key.APIKey)
	apiSecret := strings.TrimSpace(key.APISecret)
	if apiSecret == "" {
		return nil, errors.New("api_secret is required")
	}
	client := hodlhodl.NewClient(cfg.HodlHodlBaseURL, apiKey)
	if _, err := client.GetMe(ctx); err != nil {
		return nil, err
	}
	if err := client.VerifySignatureKey(ctx, apiSecret); err != nil {
		return nil, err
	}
	return map[string]string{
		"api_key":       cexaccount.PermGranted,
		"signature_key": cexaccount.PermGranted,
	}, nil
}

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
