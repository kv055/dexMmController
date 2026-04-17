package db

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrWalletAddressNotFound = errors.New("wallet address not found")

// TransactionRepository provides database operations for transactions
type TransactionRepository struct {
	db *gorm.DB
}

type TransactionFilters struct {
	Limit          int
	Offset         int
	Status         string
	Direction      string
	CEX            string
	Dex            string
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
	if err := r.db.Create(tx).Error; err != nil {
		return fmt.Errorf("failed to create transaction: %w", err)
	}
	return nil
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

func (r *TransactionRepository) GetPrimaryWalletAddress() (*WalletAddress, error) {
	return r.GetWalletAddressByLabel("Primary")
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
