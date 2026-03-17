package db

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// TransactionRepository provides database operations for transactions
type TransactionRepository struct {
	db *gorm.DB
}

// NewTransactionRepository creates a new repository instance
func NewTransactionRepository(db *gorm.DB) *TransactionRepository {
	return &TransactionRepository{db: db}
}

// CreateTransaction creates a new transaction record
func (r *TransactionRepository) CreateTransaction(tx *Transaction) error {
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

// GetTransactionsByDirection retrieves transactions filtered by direction (BUY/SELL)
func (r *TransactionRepository) GetTransactionsByDirection(direction string) ([]Transaction, error) {
	var transactions []Transaction
	if err := r.db.Where("direction = ?", direction).Order("created_at DESC").Find(&transactions).Error; err != nil {
		return nil, fmt.Errorf("failed to get transactions by direction: %w", err)
	}
	return transactions, nil
}

// GetTransactionsByCEX retrieves transactions filtered by CEX name
func (r *TransactionRepository) GetTransactionsByCEX(cexName string) ([]Transaction, error) {
	var transactions []Transaction
	if err := r.db.Where("transfer_destination_cex = ?", cexName).Order("created_at DESC").Find(&transactions).Error; err != nil {
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
		TotalPnl    float64
		AvgPnl      float64
		MaxPnl      float64
		MinPnl      float64
		TxCount     int64
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
