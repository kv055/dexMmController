# New Features - Schema & Query Management

## Overview

The project now includes three powerful new components for managing database schemas and queries:

1. **SchemaManager** - Manage database table schemas
2. **QueryBuilder** - Table-agnostic query interface
3. **DummyDataGenerator** - Generate realistic sample data

## SchemaManager Class

Located in: `internal/db/schema.go`

### Purpose
Provides a high-level interface for managing database schemas and table structures.

### Key Methods

```go
sm := db.NewSchemaManager(database)

// Create or update table schema
sm.CreateSchema(&db.Transaction{})
sm.CreateSchemas(&db.Transaction{}, &db.User{})

// Check if table exists
exists := sm.TableExists("transactions")

// Get table information
columns, err := sm.GetTableColumns("transactions")
schema, err := sm.GetTableSchema("transactions")
desc, err := sm.DescribeTable("transactions")

// Drop table
sm.DropTable("transactions")

// Validate schema exists
err := sm.ValidateSchema("transactions")

// Print all schemas
sm.PrintAllSchemas()
```

### Example Usage

```go
// Initialize
database, _ := db.InitDB("./sqlite.db")
sm := db.NewSchemaManager(database)

// Create table from model
sm.CreateSchema(&db.Transaction{})

// Check what columns exist
columns, _ := sm.GetTableColumns("transactions")
fmt.Println("Columns:", columns)

// Get detailed schema info
schema, _ := sm.GetTableSchema("transactions")
fmt.Printf("Columns: %v\n", schema["columns"])
fmt.Printf("Indexes: %v\n", schema["indexes"])

// Print human-readable description
desc, _ := sm.DescribeTable("transactions")
fmt.Println(desc)
```

---

## QueryBuilder Class

Located in: `internal/db/query.go`

### Purpose
Provides a table-agnostic, generic interface for database queries. Works with any model type.

### Key Methods

#### INSERT Operations
```go
qb := db.NewQueryBuilder(database)

// Insert single record
qb.InsertOne(&transaction)

// Insert multiple records (batch)
qb.InsertMany(transactions, 100)  // batchSize=100
```

#### SELECT Operations
```go
// Get all records
var transactions []db.Transaction
qb.SelectAll(&transactions)

// Get by ID
var tx db.Transaction
qb.SelectByID(&tx, 1)

// Get with WHERE clause
var buyTxs []db.Transaction
qb.SelectWhere(&buyTxs, "direction = ?", "BUY")

// Get with pagination
var page []db.Transaction
qb.SelectPaginated(&page, 0, 10)  // offset=0, limit=10

// Get with ordering
var ordered []db.Transaction
qb.SelectOrdered(&ordered, "created_at DESC", 0, 20)
```

#### UPDATE Operations
```go
// Update single record
tx.TotalTransactionPnl = 100.0
qb.UpdateOne(&tx)

// Update by ID
qb.UpdateByID(1, map[string]interface{}{"status": "completed"})

// Update many records
qb.UpdateMany(
    map[string]interface{}{"status": "processed"},
    "direction = ?",
    "BUY",
)
```

#### DELETE Operations
```go
// Soft delete single record
qb.DeleteOne(&transaction)

// Soft delete by ID
qb.DeleteByID(&db.Transaction{}, 1)

// Soft delete where condition
qb.DeleteWhere(&db.Transaction{}, "direction = ?", "SELL")

// Hard delete (permanent)
qb.HardDeleteByID(&db.Transaction{}, 1)
```

#### AGGREGATE Operations
```go
// Count records
total, _ := qb.CountAll(&db.Transaction{})
buyCount, _ := qb.Count(&db.Transaction{}, "direction = ?", "BUY")

// Sum values
totalPnL, _ := qb.Sum(&db.Transaction{}, "total_transaction_pnl")

// Average
avgPnL, _ := qb.Average(&db.Transaction{}, "total_transaction_pnl")

// Min/Max
maxPnL, _ := qb.Max(&db.Transaction{}, "total_transaction_pnl")
minPnL, _ := qb.Min(&db.Transaction{}, "total_transaction_pnl")

// Distinct values
values, _ := qb.Distinct(&db.Transaction{}, "transfer_destination_cex")

// Check existence
exists, _ := qb.Exists(&db.Transaction{}, "id = ?", 1)

// Group by
var results []interface{}
qb.GroupBy(&results, &db.Transaction{}, "direction", "COUNT(*) as count")
```

#### TRANSACTION Support
```go
// Execute within a transaction
err := qb.Transaction(func(tx *gorm.DB) error {
    // Multiple operations within transaction
    // Automatic rollback on error
    return nil
})
```

### Example Usage

```go
qb := db.NewQueryBuilder(database)

// Insert
transactions := []db.Transaction{...}
qb.InsertMany(transactions, 100)

// Query
var results []db.Transaction
qb.SelectWhere(&results, "direction = ? AND transfer_destination_cex = ?", "BUY", "Kraken")

// Aggregate
count, _ := qb.CountAll(&db.Transaction{})
totalPnL, _ := qb.Sum(&db.Transaction{}, "total_transaction_pnl")

// Update
qb.UpdateByID(1, map[string]interface{}{"status": "verified"})

// Delete
qb.DeleteByID(&db.Transaction{}, 1)
```

---

## DummyDataGenerator Class

Located in: `internal/db/dummy_data.go`

### Purpose
Generates realistic sample transaction data for testing and development.

### Key Methods

```go
gen := db.NewDummyDataGenerator()

// Generate single transaction
tx := gen.GenerateSingleTransaction("BUY", 1000.0, "Kraken")

// Generate multiple random transactions
txs := gen.GenerateMultipleTransactions(50)

// Generate specific types
buyTxs := gen.GenerateBuyTransactions(10)
sellTxs := gen.GenerateSellTransactions(10)
profitTxs := gen.GenerateHighProfitTransactions(5)
lossTxs := gen.GenerateLossingTransactions(5)

// Generate by exchange
krakenTxs := gen.GenerateKrakenTransactions(20)
binanceTxs := gen.GenerateBinanceTransactions(20)

// Pre-configured scenarios
scenario1 := gen.SampleScenario1()  // Realistic trading scenario
scenario2 := gen.SampleScenario2()  // Volatile scenario
all := gen.All(100)                  // Comprehensive mix
```

### Example Usage

```go
gen := db.NewDummyDataGenerator()
qb := db.NewQueryBuilder(database)

// Generate sample data
transactions := gen.GenerateMultipleTransactions(50)

// Insert into database
qb.InsertMany(transactions, 100)

// Query the data
var results []db.Transaction
qb.SelectAll(&results)
fmt.Printf("Inserted %d transactions\n", len(results))
```

---

## Typical Workflow

### Step 1: Initialize Components
```go
// Database
database, _ := db.InitDB("./sqlite.db")

// Schema manager
sm := db.NewSchemaManager(database)
sm.CreateSchemas(&db.Transaction{})

// Query builder
qb := db.NewQueryBuilder(database)

// Dummy data
gen := db.NewDummyDataGenerator()
```

### Step 2: Generate Data
```go
// Create sample transactions
transactions := gen.GenerateMultipleTransactions(100)
```

### Step 3: Insert Data
```go
// Bulk insert
qb.InsertMany(transactions, 50)
```

### Step 4: Query Data
```go
// Various queries
var all []db.Transaction
qb.SelectAll(&all)

var filtered []db.Transaction
qb.SelectWhere(&filtered, "direction = ?", "BUY")

count, _ := qb.CountAll(&db.Transaction{})
total, _ := qb.Sum(&db.Transaction{}, "total_transaction_pnl")
```

### Step 5: Update/Delete
```go
// Update
qb.UpdateByID(1, map[string]interface{}{"status": "completed"})

// Delete
qb.DeleteByID(&db.Transaction{}, 1)
```

---

## Architecture Comparison

### Old Approach (Repository Pattern)
```
main.go
  └─→ TransactionRepository
       └─→ GORM (hardcoded Transaction model)
            └─→ SQLite
```

**Limitations:**
- Only works with Transaction model
- Need separate repository for each table
- Less flexible for dynamic queries

### New Approach (Schema + Query Builder)
```
main.go
  ├─→ SchemaManager (any model)
  ├─→ QueryBuilder (generic interface)
  ├─→ DummyDataGenerator (test data)
  └─→ GORM (any model type)
       └─→ SQLite
```

**Benefits:**
- Works with any model type
- Single QueryBuilder for all tables
- Flexible, reusable components
- Easy to test with dummy data
- Type-safe queries

---

## Complete Example

```go
package main

import (
    "log"
    "dexMmController/internal/db"
)

func main() {
    // Initialize
    database, _ := db.InitDB("./sqlite.db")
    defer db.Close(database)

    sm := db.NewSchemaManager(database)
    qb := db.NewQueryBuilder(database)
    gen := db.NewDummyDataGenerator()

    // Create schema
    sm.CreateSchemas(&db.Transaction{})

    // Generate dummy data
    transactions := gen.GenerateMultipleTransactions(50)

    // Insert data
    qb.InsertMany(transactions, 100)

    // Query data
    var results []db.Transaction
    qb.SelectWhere(&results, "direction = ?", "BUY")
    log.Printf("Found %d BUY transactions\n", len(results))

    // Aggregate
    count, _ := qb.CountAll(&db.Transaction{})
    total, _ := qb.Sum(&db.Transaction{}, "total_transaction_pnl")
    log.Printf("Total: %d transactions | PnL: %.2f\n", count, total)

    // Update
    qb.UpdateByID(1, map[string]interface{}{"status": "verified"})

    // Delete
    qb.DeleteByID(&db.Transaction{}, 1)
}
```

---

## Migration from Old Approach

If using the old `TransactionRepository` approach:

### Old Way
```go
repo := db.NewTransactionRepository(database)
repo.CreateTransaction(&tx)
tx, _ := repo.GetTransaction(1)
repo.UpdateTransaction(tx)
repo.DeleteTransaction(1)
```

### New Way
```go
qb := db.NewQueryBuilder(database)
qb.InsertOne(&tx)
qb.SelectByID(&tx, 1)
qb.UpdateOne(&tx)
qb.DeleteOne(&tx)
```

**Both approaches work!** The repository pattern is still available for backward compatibility.

---

## Best Practices

### 1. Use QueryBuilder for Generic Operations
```go
qb := db.NewQueryBuilder(database)
// Works for any model type
```

### 2. Use SchemaManager for Schema Operations
```go
sm := db.NewSchemaManager(database)
// Check, validate, describe schemas
```

### 3. Use DummyDataGenerator for Testing
```go
gen := db.NewDummyDataGenerator()
// Create sample data without hardcoding
```

### 4. Combine for Powerful Workflows
```go
sm.CreateSchemas(models...)
qb.InsertMany(gen.GenerateMultipleTransactions(1000), 100)
qb.SelectAll(&results)
```

### 5. Use Batch Operations for Large Data
```go
// Good: Batch insert
qb.InsertMany(1000records, 100)

// Bad: Individual inserts in loop
for _, tx := range records {
    qb.InsertOne(&tx)
}
```

---

## Files Modified/Added

- ✅ **NEW** `internal/db/schema.go` - SchemaManager class
- ✅ **NEW** `internal/db/query.go` - QueryBuilder class
- ✅ **NEW** `internal/db/dummy_data.go` - DummyDataGenerator class
- ✅ **UPDATED** `main.go` - 11 comprehensive scenarios using new classes

---

## Next Steps

1. **Run the examples:** `go run main.go`
2. **Review the code:** Check the new files for implementation details
3. **Integrate:** Use these components in your application
4. **Extend:** Add more dummy data scenarios as needed
5. **Test:** Create your own test cases using the QueryBuilder

---

## Troubleshooting

| Issue | Solution |
|-------|----------|
| Can't insert data | Ensure schema exists: `sm.CreateSchemas(&db.Transaction{})` |
| Query returns empty | Check WHERE clause is correct, verify data exists |
| Update didn't work | Verify ID exists before updating |
| Memory issues with large data | Use pagination: `qb.SelectPaginated(&results, offset, limit)` |

---

## Performance Tips

1. **Use InsertMany for bulk operations** - Much faster than individual inserts
2. **Use pagination for large result sets** - Reduces memory usage
3. **Create indexes** - Already included in schema for common columns
4. **Use batch size of 100-1000** - Balance between speed and memory
5. **Use COUNT() for large tables** - Faster than loading all records

---

**Happy querying!** 🚀
