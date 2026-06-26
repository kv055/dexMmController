package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrWalletAddressNotFound is the err wallet address not found value.
var ErrWalletAddressNotFound = errors.New("wallet address not found")

// LNDAccountProvider is the lndaccount provider value.
const LNDAccountProvider = "LND wallet"

// TransactionRepository provides database operations for transactions
type TransactionRepository struct {
	db *gorm.DB
}

// GetAccountByProvider retrieves an account by provider.
func (r *TransactionRepository) GetAccountByProvider(ctx context.Context, provider string) (*Account, error) {
	var account Account
	if err := r.db.WithContext(ctx).Where("provider = ?", strings.TrimSpace(provider)).First(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("load account %s: %w", provider, err)
	}
	return &account, nil
}

// EnsureAccount creates a provider account row if it does not already exist.
func (r *TransactionRepository) EnsureAccount(ctx context.Context, provider string) (*Account, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, errors.New("account provider is required")
	}

	// Guarded insert rather than ON CONFLICT (provider): the accounts table's unique
	// key is (provider, category) since dexUnifiedApi migration 022, and the row's
	// category is left NULL (the only allowed value for e.g. 'LND wallet'), so a
	// provider-only ON CONFLICT has no matching constraint. INSERT ... WHERE NOT EXISTS
	// stays idempotent without depending on a provider-only unique constraint.
	if err := r.db.WithContext(ctx).Exec(`
		INSERT INTO accounts (provider)
		SELECT ?
		WHERE NOT EXISTS (
			SELECT 1 FROM accounts WHERE provider = ?
		)
	`, provider, provider).Error; err != nil {
		return nil, fmt.Errorf("ensure account %s: %w", provider, err)
	}

	return r.GetAccountByProvider(ctx, provider)
}

// GetActiveAPIKeysForProvider retrieves an active apikeys for provider.
func (r *TransactionRepository) GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]APIKey, error) {
	var keys []APIKey
	if err := r.db.WithContext(ctx).
		Table("api_keys").
		Select("api_keys.*").
		Joins("JOIN accounts ON accounts.id = api_keys.account_id").
		Where("accounts.provider = ? AND api_keys.is_active = TRUE", strings.TrimSpace(provider)).
		Order("api_keys.updated_at DESC, api_keys.id DESC").
		Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("load active API keys for %s: %w", provider, err)
	}
	return keys, nil
}

// UpdateAPIKeyPermissions stores the latest normalized permissions for an API key.
func (r *TransactionRepository) UpdateAPIKeyPermissions(ctx context.Context, keyID uint, permissions map[string]string, checkedAt time.Time) error {
	if keyID == 0 {
		return errors.New("api key id is required")
	}
	if len(permissions) == 0 {
		return nil
	}
	raw, err := json.Marshal(permissions)
	if err != nil {
		return fmt.Errorf("marshal api key permissions: %w", err)
	}
	result := r.db.WithContext(ctx).Exec(`
		UPDATE api_keys
		SET permissions = ?::jsonb,
		    permissions_checked_at = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, string(raw), checkedAt, keyID)
	if result.Error != nil {
		return fmt.Errorf("update api key permissions for key %d: %w", keyID, result.Error)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// UpdateAccountHealthCheckedAt updates the timestamp of the latest account health check.
func (r *TransactionRepository) UpdateAccountHealthCheckedAt(ctx context.Context, provider string, healthy bool) (time.Time, error) {
	var checkedAt time.Time
	err := r.db.WithContext(ctx).Raw(`
		UPDATE accounts
		SET last_health_check = CURRENT_TIMESTAMP,
		    is_healthy = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE provider = ?
		RETURNING last_health_check
	`, healthy, strings.TrimSpace(provider)).Scan(&checkedAt).Error
	if err != nil {
		return time.Time{}, fmt.Errorf("update account health for %s: %w", provider, err)
	}
	if checkedAt.IsZero() {
		return time.Time{}, gorm.ErrRecordNotFound
	}
	return checkedAt, nil
}

// TransactionFilters represents a transaction filters.
type TransactionFilters struct {
	// Limit is the limit value for the transaction filters.
	Limit int
	// Offset is the offset value for the transaction filters.
	Offset int
	// Status is the current status of the transaction filters.
	Status string
	// Direction is the direction value for the transaction filters.
	Direction string
	// CEX is the cex value for the transaction filters.
	CEX string
	// Dex is the dex value for the transaction filters.
	Dex string
	// IncompleteOnly is the incomplete only value for the transaction filters.
	IncompleteOnly bool
}

// NewTransactionRepository creates a new repository instance
func NewTransactionRepository(db *gorm.DB) *TransactionRepository {
	return &TransactionRepository{db: db}
}

// CreateTransaction creates a new transaction record
func (r *TransactionRepository) CreateTransaction(tx *Transaction) error {
	if err := r.assignPrimaryWalletAddress(tx); err != nil {
		return err
	}
	r.maybeResolveOfferDBID(tx)
	if err := r.db.Create(tx).Error; err != nil {
		return fmt.Errorf("failed to create transaction: %w", err)
	}
	return nil
}

// maybeResolveOfferDBID best-effort fills DexOfferDBID from the external DexOfferID
// when the FK isn't resolved yet. A missing offers row (or lookup error) leaves the
// FK nil rather than failing the transaction write.
func (r *TransactionRepository) maybeResolveOfferDBID(tx *Transaction) {
	if tx == nil || tx.DexOfferDBID != nil || strings.TrimSpace(tx.DexOfferID) == "" {
		return
	}
	if id, err := r.ResolveOfferDBID(tx.DexOfferID); err == nil {
		tx.DexOfferDBID = id
	}
}

// ResolveOfferDBID returns the offers.id for an external DEX offer_id, or nil when
// no matching offer row exists yet. Best-effort: callers must not fail a trade over
// a missing offers row.
func (r *TransactionRepository) ResolveOfferDBID(offerID string) (*int64, error) {
	offerID = strings.TrimSpace(offerID)
	if offerID == "" {
		return nil, nil
	}
	var ids []int64
	if err := r.db.Table("offers").Where("offer_id = ?", offerID).Limit(1).Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("resolve offer db id for %s: %w", offerID, err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return &ids[0], nil
}

// GetTransaction retrieves a single transaction by ID
func (r *TransactionRepository) GetTransaction(id uint) (*Transaction, error) {
	var tx Transaction
	if err := r.db.First(&tx, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("transaction not found with ID %d", id)
		}
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}
	return &tx, nil
}

// GetAllTransactions retrieves all transactions with optional pagination
func (r *TransactionRepository) GetAllTransactions(limit int, offset int) ([]Transaction, error) {
	var transactions []Transaction
	query := r.db.Order("created_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	if err := query.Find(&transactions).Error; err != nil {
		return nil, fmt.Errorf("failed to get transactions: %w", err)
	}
	return transactions, nil
}

// ListTransactions returns transactions matching the provided filters.
func (r *TransactionRepository) ListTransactions(filters TransactionFilters) ([]Transaction, int64, error) {
	var (
		transactions []Transaction
		total        int64
	)

	query := r.applyTransactionFilters(r.db.Model(&Transaction{}), filters)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count transactions: %w", err)
	}

	query = query.Order("created_at DESC")
	if filters.Limit > 0 {
		query = query.Limit(filters.Limit)
	}
	if filters.Offset > 0 {
		query = query.Offset(filters.Offset)
	}

	if err := query.Find(&transactions).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list transactions: %w", err)
	}

	return transactions, total, nil
}

// GetTransactionsByStatuses returns transactions matching any of the given statuses.
// When direction is non-empty, results are limited to that direction (BUY/SELL).
func (r *TransactionRepository) GetTransactionsByStatuses(statuses []TransactionStatus, direction string) ([]Transaction, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	var transactions []Transaction
	query := r.db.Where("status IN ?", statuses)
	if dir := strings.TrimSpace(direction); dir != "" {
		query = query.Where("direction = ?", strings.ToUpper(dir))
	}
	if err := query.Order("created_at ASC").Find(&transactions).Error; err != nil {
		return nil, fmt.Errorf("failed to get transactions by statuses: %w", err)
	}
	return transactions, nil
}

// GetTransactionsByDirection retrieves transactions filtered by direction (BUY/SELL)
func (r *TransactionRepository) GetTransactionsByDirection(direction string) ([]Transaction, error) {
	var transactions []Transaction
	if err := r.db.Where("direction = ?", direction).Order("created_at DESC").Find(&transactions).Error; err != nil {
		return nil, fmt.Errorf("failed to get transactions by direction: %w", err)
	}
	return transactions, nil
}

func (r *TransactionRepository) applyTransactionFilters(query *gorm.DB, filters TransactionFilters) *gorm.DB {
	if filters.IncompleteOnly {
		query = query.Where("status != ?", StatusDexPositionReleased)
	}
	if status := strings.TrimSpace(filters.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if direction := strings.TrimSpace(filters.Direction); direction != "" {
		query = query.Where("direction = ?", strings.ToUpper(direction))
	}
	if cex := strings.TrimSpace(filters.CEX); cex != "" {
		query = query.Where("cex = ?", cex)
	}
	if dex := strings.TrimSpace(filters.Dex); dex != "" {
		query = query.Where("dex = ?", dex)
	}
	return query
}

// GetTransactionsByCEX retrieves transactions filtered by CEX name
func (r *TransactionRepository) GetTransactionsByCEX(cexName string) ([]Transaction, error) {
	var transactions []Transaction
	if err := r.db.Where("cex = ?", cexName).Order("created_at DESC").Find(&transactions).Error; err != nil {
		return nil, fmt.Errorf("failed to get transactions by CEX: %w", err)
	}
	return transactions, nil
}

// UpdateTransaction updates an existing transaction
func (r *TransactionRepository) UpdateTransaction(tx *Transaction) error {
	if tx.ID == 0 {
		return errors.New("transaction ID is required for update")
	}
	r.maybeResolveOfferDBID(tx)
	if err := r.db.Save(tx).Error; err != nil {
		return fmt.Errorf("failed to update transaction: %w", err)
	}
	return nil
}

// DeleteTransaction deletes a transaction (soft delete)
func (r *TransactionRepository) DeleteTransaction(id uint) error {
	if err := r.db.Delete(&Transaction{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete transaction: %w", err)
	}
	return nil
}

// GetIncompleteTransactions retrieves all transactions that have not yet reached DexPositionReleased
func (r *TransactionRepository) GetIncompleteTransactions() ([]Transaction, error) {
	var transactions []Transaction
	if err := r.db.Where("status != ?", StatusDexPositionReleased).Order("created_at ASC").Find(&transactions).Error; err != nil {
		return nil, fmt.Errorf("failed to get incomplete transactions: %w", err)
	}
	return transactions, nil
}

// GetTransactionCount returns the total number of transactions
func (r *TransactionRepository) GetTransactionCount() (int64, error) {
	var count int64
	if err := r.db.Model(&Transaction{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count transactions: %w", err)
	}
	return count, nil
}

// GetPnlStats returns PnL statistics
func (r *TransactionRepository) GetPnlStats() (map[string]interface{}, error) {
	var stats struct {
		TotalPnl float64
		AvgPnl   float64
		MaxPnl   float64
		MinPnl   float64
		TxCount  int64
	}

	if err := r.db.Model(&Transaction{}).
		Select(
			"COALESCE(SUM(total_transaction_pnl), 0) as total_pnl",
			"COALESCE(AVG(total_transaction_pnl), 0) as avg_pnl",
			"COALESCE(MAX(total_transaction_pnl), 0) as max_pnl",
			"COALESCE(MIN(total_transaction_pnl), 0) as min_pnl",
			"COUNT(*) as tx_count",
		).
		Scan(&stats).Error; err != nil {
		return nil, fmt.Errorf("failed to get PnL stats: %w", err)
	}

	return map[string]interface{}{
		"total_pnl": stats.TotalPnl,
		"avg_pnl":   stats.AvgPnl,
		"max_pnl":   stats.MaxPnl,
		"min_pnl":   stats.MinPnl,
		"tx_count":  stats.TxCount,
	}, nil
}

// GetWalletAddressByLabel retrieves a wallet address by label.
func (r *TransactionRepository) GetWalletAddressByLabel(label string) (*WalletAddress, error) {
	var wallet WalletAddress
	if err := r.db.Where("label = ?", label).First(&wallet).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w for label %q", ErrWalletAddressNotFound, label)
		}
		return nil, fmt.Errorf("failed to get wallet address by label %q: %w", label, err)
	}
	return &wallet, nil
}

// SaveWalletAddress stores or updates a wallet address record.
func (r *TransactionRepository) SaveWalletAddress(wallet *WalletAddress) error {
	if wallet == nil {
		return errors.New("wallet address is required")
	}
	if wallet.Label == "" {
		return errors.New("wallet label is required")
	}
	if wallet.Address == "" {
		return errors.New("wallet address value is required")
	}

	if err := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "label"}},
		DoUpdates: clause.AssignmentColumns([]string{"address", "updated_at"}),
	}).Create(wallet).Error; err != nil {
		return fmt.Errorf("failed to save wallet address %q: %w", wallet.Label, err)
	}
	return nil
}

// GetPrimaryWalletAddress retrieves a primary wallet address.
func (r *TransactionRepository) GetPrimaryWalletAddress() (*WalletAddress, error) {
	return r.GetWalletAddressByLabel("Primary")
}

// ListWalletAddresses returns all stored wallet address records.
func (r *TransactionRepository) ListWalletAddresses() ([]WalletAddress, error) {
	var wallets []WalletAddress
	if err := r.db.Order("created_at ASC, id ASC").Find(&wallets).Error; err != nil {
		return nil, fmt.Errorf("failed to list wallet addresses: %w", err)
	}
	return wallets, nil
}

// RecordLNDMasterKeyHash stores the hash of the configured LND master key.
func (r *TransactionRepository) RecordLNDMasterKeyHash(masterKeyHash, sourcePath string, fileSizeBytes int64) error {
	masterKeyHash = strings.TrimSpace(masterKeyHash)
	if masterKeyHash == "" {
		return errors.New("LND master key hash is required")
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		var accountID int64
		if err := tx.Exec(`
			INSERT INTO accounts (provider)
			VALUES (?)
			ON CONFLICT (provider) DO NOTHING
		`, LNDAccountProvider).Error; err != nil {
			return fmt.Errorf("ensure LND account row: %w", err)
		}
		if err := tx.Raw(`
			SELECT id
			FROM accounts
			WHERE provider = ?
		`, LNDAccountProvider).Scan(&accountID).Error; err != nil {
			return fmt.Errorf("load LND account row: %w", err)
		}
		if accountID == 0 {
			return fmt.Errorf("LND account row %q is missing", LNDAccountProvider)
		}

		if err := tx.Exec(`
			INSERT INTO lnd_master_key_hashes (
				account_id,
				hash_algorithm,
				master_key_hash,
				source_path,
				file_size_bytes
			)
			SELECT ?, 'sha256', ?, NULLIF(?, ''), ?
			WHERE NOT EXISTS (
				SELECT 1
				FROM lnd_master_key_hashes
				WHERE account_id = ?
				  AND master_key_hash = ?
				  AND file_size_bytes = ?
			)
		`, accountID, masterKeyHash, strings.TrimSpace(sourcePath), fileSizeBytes, accountID, masterKeyHash, fileSizeBytes).Error; err != nil {
			return fmt.Errorf("insert LND master key hash: %w", err)
		}

		return nil
	})
}

// UpdateAccountHealth records the latest health state for a provider.
func (r *TransactionRepository) UpdateAccountHealth(ctx context.Context, provider string, healthy bool) error {
	_, err := r.UpdateAccountHealthCheckedAt(ctx, provider, healthy)
	return err
}

func (r *TransactionRepository) assignPrimaryWalletAddress(tx *Transaction) error {
	if tx == nil || tx.DexBtcAddress != "" {
		return nil
	}

	primary, err := r.GetPrimaryWalletAddress()
	if err != nil {
		if errors.Is(err, ErrWalletAddressNotFound) || primary == nil {
			return nil
		}
		return fmt.Errorf("failed to assign primary wallet address: %w", err)
	}

	tx.DexBtcAddress = primary.Address
	return nil
}
