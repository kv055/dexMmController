package lndhealth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"dexMmController/internal/db"
	lnd "dexMmController/internal/wallethub"
)

// Client defines the LND operations required for wallet health checks.
type Client interface {
	HealthCheck(ctx context.Context) error
	NodeInfo(ctx context.Context) (*lnd.NodeInfoResponse, error)
	ListChannels(ctx context.Context) ([]lnd.Channel, error)
	ExportChannelBackup(ctx context.Context) error
	LastExportedChannelBackup() ([]byte, int)
}

// Repository defines the database access required for wallet health checks.
type Repository interface {
	GetAccountByProvider(ctx context.Context, provider string) (*db.Account, error)
	LNDWalletCredentials(ctx context.Context) (password string, mnemonic string, err error)
	GetPrimaryWalletAddress() (*db.WalletAddress, error)
	LatestLNDChannelBackup(ctx context.Context, accountID int64) (*db.LNDChannelBackup, error)
	LatestLNDMasterKeyHash(ctx context.Context, accountID int64) (string, error)
	UpdateAccountHealthCheckedAt(ctx context.Context, provider string, healthy bool) (time.Time, error)
}

// Config carries optional runtime configuration for LND health checks.
type Config struct {
	ExtendedMasterKeyFile string
}

// CheckResult reports the outcome of one health sub-check.
type CheckResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Result is the aggregate LND wallet health outcome.
type Result struct {
	Provider  string        `json:"provider"`
	Status    string        `json:"status"`
	Healthy   bool          `json:"healthy"`
	Message   string        `json:"message"`
	Checks    []CheckResult `json:"checks,omitempty"`
	CheckedAt time.Time     `json:"checked_at"`
}

// Checker runs comprehensive health checks for the LND wallet account.
type Checker struct {
	repo Repository
	cfg  Config

	mu     sync.RWMutex
	client Client
}

// NewChecker creates an LND wallet health checker.
func NewChecker(repo Repository, client Client, cfg Config) *Checker {
	return &Checker{repo: repo, client: client, cfg: cfg}
}

// SetClient swaps the live LND client used by the checker. It is safe to call at
// runtime, e.g. after the wallet is created on demand via the init endpoint.
func (c *Checker) SetClient(client Client) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.client = client
	c.mu.Unlock()
}

func (c *Checker) currentClient() Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

// IsLNDWalletProvider reports whether the provider name refers to the LND wallet account.
func IsLNDWalletProvider(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "lnd wallet", "lnd_wallet", "lnd":
		return true
	default:
		return strings.TrimSpace(raw) == db.LNDAccountProvider
	}
}

// Check runs all LND wallet health checks and persists account health state.
func (c *Checker) Check(ctx context.Context) (Result, error) {
	if c == nil || c.repo == nil {
		return Result{}, errors.New("lnd health checker is not configured")
	}

	client := c.currentClient()

	provider := db.LNDAccountProvider
	account, err := c.repo.GetAccountByProvider(ctx, provider)
	if err != nil {
		return Result{}, err
	}

	password, mnemonic, err := c.repo.LNDWalletCredentials(ctx)
	if err != nil {
		return Result{}, err
	}

	checks := make([]CheckResult, 0, 8)
	requiredOK := true

	addRequired := func(name string, ok bool, detail string) {
		checks = append(checks, CheckResult{Name: name, OK: ok, Detail: detail})
		if !ok {
			requiredOK = false
		}
	}
	addInfo := func(name string, detail string) {
		checks = append(checks, CheckResult{Name: name, OK: true, Detail: detail})
	}

	if strings.TrimSpace(password) == "" {
		addRequired("wallet password", false, "missing in database")
	} else {
		addRequired("wallet password", true, "configured in database")
	}

	if !clientConfigured(client) {
		addRequired("lnd reachable", false, "LND client is not initialized")
	} else if err := client.HealthCheck(ctx); err != nil {
		addRequired("lnd reachable", false, err.Error())
	} else {
		addRequired("lnd reachable", true, "wallet unlocked and RPC healthy")
	}

	var liveChannelCount int
	channelsListed := false
	if clientConfigured(client) {
		if nodeInfo, err := client.NodeInfo(ctx); err != nil {
			addInfo("node sync", fmt.Sprintf("node info unavailable: %v", err))
		} else {
			addInfo(
				"node sync",
				fmt.Sprintf(
					"synced_to_chain=%t synced_to_graph=%t block_height=%d",
					nodeInfo.SyncedToChain,
					nodeInfo.SyncedToGraph,
					nodeInfo.BlockHeight,
				),
			)
		}

		channels, err := client.ListChannels(ctx)
		if err != nil {
			addRequired("channel list", false, err.Error())
		} else {
			channelsListed = true
			liveChannelCount = len(channels)
			addRequired("channel list", true, fmt.Sprintf("%d channel(s) listed", liveChannelCount))
		}
	}

	needsSeed := liveChannelCount > 0
	storedBackup, err := c.repo.LatestLNDChannelBackup(ctx, int64(account.ID))
	if err != nil {
		return Result{}, err
	}
	if storedBackup != nil {
		needsSeed = true
	}

	if needsSeed && strings.TrimSpace(mnemonic) == "" {
		addRequired("aezeed seed", false, "missing in database")
	} else if strings.TrimSpace(mnemonic) == "" {
		addInfo("aezeed seed", "not stored yet (ok for a wallet with no channels)")
	} else {
		addRequired("aezeed seed", true, "configured in database")
	}

	wallet, err := c.repo.GetPrimaryWalletAddress()
	if err != nil {
		addRequired("primary address", false, err.Error())
	} else if wallet == nil || strings.TrimSpace(wallet.Address) == "" {
		addRequired("primary address", false, "missing in database")
	} else {
		addRequired("primary address", true, wallet.Address)
	}

	masterKeyPath := strings.TrimSpace(c.cfg.ExtendedMasterKeyFile)
	if masterKeyPath != "" {
		fileHash, err := hashFileSHA256(masterKeyPath)
		if err != nil {
			addRequired("master key file", false, err.Error())
		} else {
			storedHash, err := c.repo.LatestLNDMasterKeyHash(ctx, int64(account.ID))
			if err != nil {
				return Result{}, err
			}
			if storedHash == "" {
				addRequired("master key file", false, "no hash recorded in database")
			} else if !strings.EqualFold(fileHash, storedHash) {
				addRequired("master key file", false, "hash does not match latest database record")
			} else {
				addRequired("master key file", true, "matches database record")
			}
		}
	}

	if !clientConfigured(client) {
		// Earlier checks already recorded why RPC is unavailable.
	} else if !channelsListed {
		// Channel list failed; backup validation is skipped.
	} else if liveChannelCount == 0 {
		addInfo("channel backup", "no channels to verify")
	} else if err := client.ExportChannelBackup(ctx); err != nil {
		addRequired("channel backup", false, err.Error())
	} else {
		liveRaw, liveBackupCount := client.LastExportedChannelBackup()
		liveHash := hashBackupBytes(liveRaw)
		if storedBackup == nil {
			addRequired("channel backup", false, "no backup stored in database")
		} else if liveHash == "" {
			addRequired("channel backup", false, "live export returned empty backup payload")
		} else if !strings.EqualFold(liveHash, storedBackup.BackupSHA256) {
			addRequired(
				"channel backup",
				false,
				fmt.Sprintf("hash mismatch (live=%s stored=%s)", liveHash, storedBackup.BackupSHA256),
			)
		} else if liveBackupCount != storedBackup.ChannelCount {
			addRequired(
				"channel backup",
				false,
				fmt.Sprintf("channel count mismatch (live=%d stored=%d)", liveBackupCount, storedBackup.ChannelCount),
			)
		} else if liveChannelCount != storedBackup.ChannelCount {
			addRequired(
				"channel backup",
				false,
				fmt.Sprintf("listed channels=%d stored backup channels=%d", liveChannelCount, storedBackup.ChannelCount),
			)
		} else {
			addRequired(
				"channel backup",
				true,
				fmt.Sprintf("%d channel(s), hash matches database", storedBackup.ChannelCount),
			)
		}
	}

	checkedAt, err := c.repo.UpdateAccountHealthCheckedAt(ctx, provider, requiredOK)
	if err != nil {
		return Result{}, err
	}

	status := "healthy"
	if !requiredOK {
		status = "unhealthy"
	}

	return Result{
		Provider:  provider,
		Status:    status,
		Healthy:   requiredOK,
		Message:   formatMessage(checks),
		Checks:    checks,
		CheckedAt: checkedAt,
	}, nil
}

// RunAndLog executes Check and logs the outcome for periodic health jobs.
func (c *Checker) RunAndLog(ctx context.Context) {
	result, err := c.Check(ctx)
	if err != nil {
		log.Printf("[AccountHealth] %s: %v", db.LNDAccountProvider, err)
		return
	}
	if result.Healthy {
		log.Printf("[AccountHealth] %s %s: %s", result.Provider, result.Status, result.Message)
		return
	}
	log.Printf("[AccountHealth] %s %s: %s", result.Provider, result.Status, result.Message)
}

// RunPeriodic executes Check on a fixed interval until ctx is cancelled.
func (c *Checker) RunPeriodic(ctx context.Context, interval time.Duration) {
	if c == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.RunAndLog(ctx)
		}
	}
}

func formatMessage(checks []CheckResult) string {
	parts := make([]string, 0, len(checks))
	for _, check := range checks {
		mark := "✗"
		if check.OK {
			mark = "✓"
		}
		line := fmt.Sprintf("%s %s", check.Name, mark)
		if check.Detail != "" {
			line += ": " + check.Detail
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "; ")
}

func hashBackupBytes(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func clientConfigured(client Client) bool {
	if client == nil {
		return false
	}
	value := reflect.ValueOf(client)
	return value.Kind() != reflect.Pointer || !value.IsNil()
}

func hashFileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read master key file: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
