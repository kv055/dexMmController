package main

import (
	"encoding/json"
	"log"

	"dexMmController/internal/db"
	"gorm.io/gorm"
)

type QueryScenarioLogger struct {
	database *gorm.DB
}

func NewQueryScenarioLogger(database *gorm.DB) *QueryScenarioLogger {
	return &QueryScenarioLogger{database: database}
}

func (l *QueryScenarioLogger) Run() {
	// ==================== QUERY BUILDER INITIALIZATION ====================
	log.Println("--- Query Builder & Dummy Data ---")
	queryBuilder := db.NewQueryBuilder(l.database)
	dummyGen := db.NewDummyDataGenerator()
	log.Println("✓ Query builder and dummy data generator initialized\n")

	// ==================== SCENARIO 1: BULK INSERT DUMMY DATA ====================
	log.Println("--- Scenario 1: Bulk Insert Dummy Data ---")
	dummyTransactions := dummyGen.GenerateMultipleTransactions(10)
	log.Printf("Generated %d dummy transactions\n", len(dummyTransactions))

	if err := queryBuilder.InsertMany(dummyTransactions, 100); err != nil {
		log.Printf("Error inserting dummy data: %v\n", err)
	} else {
		log.Printf("✓ Successfully inserted %d transactions\n", len(dummyTransactions))
	}

	// ==================== SCENARIO 2: QUERY ALL RECORDS ====================
	log.Println("\n--- Scenario 2: Query All Records ---")
	var allTxs []db.Transaction
	if err := queryBuilder.SelectAll(&allTxs); err != nil {
		log.Printf("Error: %v\n", err)
	} else {
		log.Printf("✓ Retrieved %d transactions\n", len(allTxs))
		for i, tx := range allTxs {
			log.Printf("  [%d] ID=%d | Dir=%s | Amount=%.2f | PnL=%.2f | CEX=%s\n",
				i+1, tx.ID, tx.Direction, tx.FiatAmount, tx.TotalTransactionPnl, tx.TransferDestinationCEX)
		}
	}

	// ==================== SCENARIO 3: FILTERED QUERY ====================
	log.Println("\n--- Scenario 3: Filtered Query (Direction = BUY) ---")
	var buyTxs []db.Transaction
	if err := queryBuilder.SelectWhere(&buyTxs, "direction = ?", "BUY"); err != nil {
		log.Printf("Error: %v\n", err)
	} else {
		log.Printf("✓ Found %d BUY transactions\n", len(buyTxs))
		for i, tx := range buyTxs {
			log.Printf("  [%d] ID=%d | Amount=%.2f | CEX=%s\n", i+1, tx.ID, tx.FiatAmount, tx.TransferDestinationCEX)
		}
	}

	// ==================== SCENARIO 4: PAGINATED QUERY ====================
	log.Println("\n--- Scenario 4: Paginated Query (First 5 records) ---")
	var paginatedTxs []db.Transaction
	if err := queryBuilder.SelectPaginated(&paginatedTxs, 0, 5); err != nil {
		log.Printf("Error: %v\n", err)
	} else {
		log.Printf("✓ Retrieved page: %d records\n", len(paginatedTxs))
	}

	// ==================== SCENARIO 5: ORDERED QUERY ====================
	log.Println("\n--- Scenario 5: Ordered Query (By PnL DESC) ---")
	var orderedTxs []db.Transaction
	if err := queryBuilder.SelectOrdered(&orderedTxs, "total_transaction_pnl DESC", 0, 5); err != nil {
		log.Printf("Error: %v\n", err)
	} else {
		log.Printf("✓ Top 5 by PnL:\n")
		for i, tx := range orderedTxs {
			log.Printf("  [%d] ID=%d | PnL=%.2f | Direction=%s\n", i+1, tx.ID, tx.TotalTransactionPnl, tx.Direction)
		}
	}

	// ==================== SCENARIO 6: AGGREGATE QUERIES ====================
	log.Println("\n--- Scenario 6: Aggregate Queries ---")

	// Count all transactions
	totalCount, _ := queryBuilder.CountAll(&db.Transaction{})
	log.Printf("✓ Total transactions: %d\n", totalCount)

	// Count by direction
	buyCount, _ := queryBuilder.Count(&db.Transaction{}, "direction = ?", "BUY")
	sellCount, _ := queryBuilder.Count(&db.Transaction{}, "direction = ?", "SELL")
	log.Printf("✓ BUY transactions: %d | SELL transactions: %d\n", buyCount, sellCount)

	// Sum PnL
	totalPnL, _ := queryBuilder.Sum(&db.Transaction{}, "total_transaction_pnl")
	log.Printf("✓ Total PnL: %.2f\n", totalPnL)

	// Average PnL
	avgPnL, _ := queryBuilder.Average(&db.Transaction{}, "total_transaction_pnl")
	log.Printf("✓ Average PnL: %.2f\n", avgPnL)

	// Max/Min PnL
	maxPnL, _ := queryBuilder.Max(&db.Transaction{}, "total_transaction_pnl")
	minPnL, _ := queryBuilder.Min(&db.Transaction{}, "total_transaction_pnl")
	log.Printf("✓ Max PnL: %v | Min PnL: %v\n", maxPnL, minPnL)

	// ==================== SCENARIO 7: DISTINCT VALUES ====================
	log.Println("\n--- Scenario 7: Distinct CEX Values ---")
	cexes, _ := queryBuilder.Distinct(&db.Transaction{}, "transfer_destination_cex")
	log.Printf("✓ Unique CEXes: %v\n", cexes)

	// ==================== SCENARIO 8: CEX-SPECIFIC TRANSACTIONS ====================
	log.Println("\n--- Scenario 8: Kraken Transactions ---")
	var krakenTxs []db.Transaction
	if err := queryBuilder.SelectWhere(&krakenTxs, "transfer_destination_cex = ?", "Kraken"); err != nil {
		log.Printf("Error: %v\n", err)
	} else {
		krakenCount, _ := queryBuilder.Count(&db.Transaction{}, "transfer_destination_cex = ?", "Kraken")
		log.Printf("✓ Kraken transactions: %d\n", krakenCount)
	}

	// ==================== SCENARIO 9: UPDATE OPERATIONS ====================
	log.Println("\n--- Scenario 9: Update Operations ---")
	if len(allTxs) > 0 {
		// Update single record
		allTxs[0].TotalTransactionPnl = 999.99
		if err := queryBuilder.UpdateOne(&allTxs[0]); err != nil {
			log.Printf("Error updating: %v\n", err)
		} else {
			log.Printf("✓ Updated transaction ID=%d with new PnL=%.2f\n", allTxs[0].ID, allTxs[0].TotalTransactionPnl)
		}

		// Update by ID
		if err := queryBuilder.UpdateByID(allTxs[0].ID, map[string]interface{}{"vps_hosting_cost": 10.0}); err != nil {
			log.Printf("Error: %v\n", err)
		} else {
			log.Printf("✓ Updated VPS cost for transaction ID=%d\n", allTxs[0].ID)
		}
	}

	// ==================== SCENARIO 10: SPECIALIZED QUERIES ====================
	log.Println("\n--- Scenario 10: Specialized Queries ---")

	// Check if a record exists
	exists, _ := queryBuilder.Exists(&db.Transaction{}, "id = ?", 1)
	log.Printf("✓ Transaction with ID=1 exists: %v\n", exists)

	// Get transaction by ID
	var singleTx db.Transaction
	if err := queryBuilder.SelectByID(&singleTx, 1); err != nil {
		log.Printf("Note: %v (expected if ID doesn't exist)\n", err)
	} else {
		log.Printf("✓ Retrieved transaction ID=%d | Direction=%s | Amount=%.2f\n",
			singleTx.ID, singleTx.Direction, singleTx.FiatAmount)
	}

	// ==================== SCENARIO 11: STATISTICS SUMMARY ====================
	log.Println("\n--- Scenario 11: Statistics Summary ---")
	stats := map[string]interface{}{
		"total_transactions":    totalCount,
		"total_pnl":             totalPnL,
		"average_pnl":           avgPnL,
		"buy_transactions":      buyCount,
		"sell_transactions":     sellCount,
		"buy_percentage":        float64(buyCount) / float64(totalCount) * 100,
		"sell_percentage":       float64(sellCount) / float64(totalCount) * 100,
		"total_operational_cost": totalCount * 5, // Estimate: 5 per transaction
	}
	statsJSON, _ := json.MarshalIndent(stats, "", "  ")
	log.Printf("✓ Summary Statistics:\n%s\n", string(statsJSON))
}
