package db

import (
	"fmt"
	"gorm.io/gorm"
)

// QueryBuilder provides a generic query interface for any table
type QueryBuilder struct {
	db *gorm.DB
}

// NewQueryBuilder creates a new query builder
func NewQueryBuilder(database *gorm.DB) *QueryBuilder {
	return &QueryBuilder{db: database}
}

// ==================== INSERT OPERATIONS ====================

// InsertOne inserts a single record into a table
// Usage: qb.InsertOne(&transaction)
func (qb *QueryBuilder) InsertOne(data interface{}) error {
	if err := qb.db.Create(data).Error; err != nil {
		return fmt.Errorf("failed to insert record: %w", err)
	}
	return nil
}

// InsertMany inserts multiple records in batch
// Usage: qb.InsertMany([]&transaction1, &transaction2)
func (qb *QueryBuilder) InsertMany(data interface{}, batchSize int) error {
	if batchSize <= 0 {
		batchSize = 100
	}

	if err := qb.db.CreateInBatches(data, batchSize).Error; err != nil {
		return fmt.Errorf("failed to insert batch: %w", err)
	}
	return nil
}

// ==================== SELECT OPERATIONS ====================

// SelectAll retrieves all records from a table
// Usage: qb.SelectAll(&transactions)
func (qb *QueryBuilder) SelectAll(dest interface{}) error {
	if err := qb.db.Find(dest).Error; err != nil {
		return fmt.Errorf("failed to select all records: %w", err)
	}
	return nil
}

// SelectByID retrieves a single record by primary key
// Usage: qb.SelectByID(&transaction, 1)
func (qb *QueryBuilder) SelectByID(dest interface{}, id interface{}) error {
	if err := qb.db.First(dest, id).Error; err != nil {
		if gorm.ErrRecordNotFound == err {
			return fmt.Errorf("record not found with ID: %v", id)
		}
		return fmt.Errorf("failed to select by ID: %w", err)
	}
	return nil
}

// SelectWhere retrieves records matching a condition
// Usage: qb.SelectWhere(&transactions, "direction = ?", "BUY")
func (qb *QueryBuilder) SelectWhere(dest interface{}, query interface{}, args ...interface{}) error {
	if err := qb.db.Where(query, args...).Find(dest).Error; err != nil {
		return fmt.Errorf("failed to select with where clause: %w", err)
	}
	return nil
}

// SelectPaginated retrieves records with pagination
// Usage: qb.SelectPaginated(&transactions, 0, 10) // offset=0, limit=10
func (qb *QueryBuilder) SelectPaginated(dest interface{}, offset int, limit int) error {
	if limit <= 0 {
		limit = 10
	}

	if err := qb.db.Offset(offset).Limit(limit).Find(dest).Error; err != nil {
		return fmt.Errorf("failed to select paginated records: %w", err)
	}
	return nil
}

// SelectOrdered retrieves records with custom ordering
// Usage: qb.SelectOrdered(&transactions, "created_at DESC", 0, 10)
func (qb *QueryBuilder) SelectOrdered(dest interface{}, orderBy string, offset int, limit int) error {
	if limit <= 0 {
		limit = 10
	}

	query := qb.db.Order(orderBy).Offset(offset).Limit(limit)
	if err := query.Find(dest).Error; err != nil {
		return fmt.Errorf("failed to select ordered records: %w", err)
	}
	return nil
}

// ==================== UPDATE OPERATIONS ====================

// UpdateOne updates a single record
// Usage: qb.UpdateOne(&transaction)
func (qb *QueryBuilder) UpdateOne(data interface{}) error {
	if err := qb.db.Save(data).Error; err != nil {
		return fmt.Errorf("failed to update record: %w", err)
	}
	return nil
}

// UpdateMany updates multiple records with a condition
// Usage: qb.UpdateMany(&Transaction{}, map[string]interface{}{"status": "completed"}, "id IN ?", []int{1,2,3})
func (qb *QueryBuilder) UpdateMany(updates map[string]interface{}, query interface{}, args ...interface{}) error {
	if err := qb.db.Model(nil).Where(query, args...).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update many records: %w", err)
	}
	return nil
}

// UpdateByID updates a record by ID
// Usage: qb.UpdateByID(1, map[string]interface{}{"status": "done"})
func (qb *QueryBuilder) UpdateByID(id interface{}, updates map[string]interface{}) error {
	if err := qb.db.Model(nil).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update record by ID: %w", err)
	}
	return nil
}

// ==================== DELETE OPERATIONS ====================

// DeleteOne deletes a single record (soft delete)
// Usage: qb.DeleteOne(&transaction)
func (qb *QueryBuilder) DeleteOne(data interface{}) error {
	if err := qb.db.Delete(data).Error; err != nil {
		return fmt.Errorf("failed to delete record: %w", err)
	}
	return nil
}

// DeleteByID deletes a record by ID (soft delete)
// Usage: qb.DeleteByID(&Transaction{}, 1)
func (qb *QueryBuilder) DeleteByID(model interface{}, id interface{}) error {
	if err := qb.db.Delete(model, id).Error; err != nil {
		return fmt.Errorf("failed to delete record by ID: %w", err)
	}
	return nil
}

// DeleteWhere deletes records matching a condition
// Usage: qb.DeleteWhere(&Transaction{}, "direction = ?", "BUY")
func (qb *QueryBuilder) DeleteWhere(model interface{}, query interface{}, args ...interface{}) error {
	if err := qb.db.Where(query, args...).Delete(model).Error; err != nil {
		return fmt.Errorf("failed to delete records with where clause: %w", err)
	}
	return nil
}

// HardDeleteByID permanently deletes a record (hard delete, ignores soft delete)
// Usage: qb.HardDeleteByID(&Transaction{}, 1)
func (qb *QueryBuilder) HardDeleteByID(model interface{}, id interface{}) error {
	if err := qb.db.Unscoped().Delete(model, id).Error; err != nil {
		return fmt.Errorf("failed to hard delete record: %w", err)
	}
	return nil
}

// ==================== AGGREGATE OPERATIONS ====================

// Count returns the number of records matching a condition
// Usage: count, _ := qb.Count(&Transaction{}, "direction = ?", "BUY")
func (qb *QueryBuilder) Count(model interface{}, query interface{}, args ...interface{}) (int64, error) {
	var count int64
	err := qb.db.Model(model).Where(query, args...).Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("failed to count records: %w", err)
	}
	return count, nil
}

// CountAll returns the total number of records in a table
// Usage: total, _ := qb.CountAll(&Transaction{})
func (qb *QueryBuilder) CountAll(model interface{}) (int64, error) {
	var count int64
	if err := qb.db.Model(model).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count all records: %w", err)
	}
	return count, nil
}

// Sum returns the sum of a numeric column
// Usage: sum, _ := qb.Sum(&Transaction{}, "total_transaction_pnl")
func (qb *QueryBuilder) Sum(model interface{}, column string) (float64, error) {
	var result float64
	if err := qb.db.Model(model).Select("COALESCE(SUM(" + column + "), 0)").Scan(&result).Error; err != nil {
		return 0, fmt.Errorf("failed to calculate sum: %w", err)
	}
	return result, nil
}

// Average returns the average of a numeric column
// Usage: avg, _ := qb.Average(&Transaction{}, "total_transaction_pnl")
func (qb *QueryBuilder) Average(model interface{}, column string) (float64, error) {
	var result float64
	if err := qb.db.Model(model).Select("COALESCE(AVG(" + column + "), 0)").Scan(&result).Error; err != nil {
		return 0, fmt.Errorf("failed to calculate average: %w", err)
	}
	return result, nil
}

// Max returns the maximum value of a column
// Usage: max, _ := qb.Max(&Transaction{}, "total_transaction_pnl")
func (qb *QueryBuilder) Max(model interface{}, column string) (interface{}, error) {
	var result float64
	if err := qb.db.Model(model).Select("MAX(" + column + ")").Scan(&result).Error; err != nil {
		return nil, fmt.Errorf("failed to get max value: %w", err)
	}
	return result, nil
}

// Min returns the minimum value of a column
// Usage: min, _ := qb.Min(&Transaction{}, "total_transaction_pnl")
func (qb *QueryBuilder) Min(model interface{}, column string) (interface{}, error) {
	var result float64
	if err := qb.db.Model(model).Select("MIN(" + column + ")").Scan(&result).Error; err != nil {
		return nil, fmt.Errorf("failed to get min value: %w", err)
	}
	return result, nil
}

// ==================== UTILITY OPERATIONS ====================

// Distinct returns distinct values of a column
// Usage: values, _ := qb.Distinct(&Transaction{}, "transfer_destination_cex")
func (qb *QueryBuilder) Distinct(model interface{}, column string) ([]interface{}, error) {
	var results []interface{}
	if err := qb.db.Model(model).Distinct(column).Find(&results).Error; err != nil {
		return nil, fmt.Errorf("failed to get distinct values: %w", err)
	}
	return results, nil
}

// Exists checks if a record exists matching a condition
// Usage: exists, _ := qb.Exists(&Transaction{}, "id = ?", 1)
func (qb *QueryBuilder) Exists(model interface{}, query interface{}, args ...interface{}) (bool, error) {
	var count int64
	err := qb.db.Model(model).Where(query, args...).Limit(1).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("failed to check existence: %w", err)
	}
	return count > 0, nil
}

// GroupBy groups records and returns aggregated results
// Usage: qb.GroupBy(&results, &Transaction{}, "direction", "COUNT(*) as count")
func (qb *QueryBuilder) GroupBy(dest interface{}, model interface{}, groupColumn string, selectColumns string) error {
	if err := qb.db.Model(model).Select(selectColumns).Group(groupColumn).Scan(dest).Error; err != nil {
		return fmt.Errorf("failed to group records: %w", err)
	}
	return nil
}

// ==================== ADVANCED OPERATIONS ====================

// Transaction executes a function within a database transaction
// Usage: qb.Transaction(func(tx *gorm.DB) error { ... })
func (qb *QueryBuilder) Transaction(fn func(*gorm.DB) error) error {
	return qb.db.Transaction(fn)
}

// GetStats returns a map of statistics for aggregated analysis
// Usage: stats, _ := qb.GetStats(&Transaction{})
func (qb *QueryBuilder) GetStats(model interface{}) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	total, err := qb.CountAll(model)
	if err != nil {
		return nil, err
	}
	stats["total_records"] = total

	return stats, nil
}

// BeginTx begins a new transaction
func (qb *QueryBuilder) BeginTx() *QueryBuilder {
	return &QueryBuilder{db: qb.db.Begin()}
}

// Commit commits the transaction
func (qb *QueryBuilder) Commit() error {
	return qb.db.Commit().Error
}

// Rollback rolls back the transaction
func (qb *QueryBuilder) Rollback() error {
	return qb.db.Rollback().Error
}
