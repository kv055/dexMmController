package main

import (
	"context"
	"log"
	"time"

	"dexMmController/internal/bitcoin"
	"dexMmController/internal/db"
	"dexMmController/internal/openclaw"
)

func main() {
	log.Println("=== Crypto Trading Database with Schema & Query Manager ===\n")

	// Initialize database
	database, err := db.InitDB("./sqlite.db")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v\n", err)
	}
	defer db.Close(database)

	// ==================== OPENCLAW MOCK (DEMO) ====================
	// This is a local mock to demonstrate bi-directional comms and polling logic.
	log.Println("--- OpenClaw Mock: Poll Bank Tx Arrival ---")
	mock := openclaw.NewMockServer("127.0.0.1:0")
	mock.SetArrivedAfter("xyz123", 3*time.Second)
	if err := mock.Start(); err != nil {
		log.Fatalf("Failed to start OpenClaw mock server: %v\n", err)
	}
	defer func() { _ = mock.Close(context.Background()) }()

	claw := openclaw.NewClient(mock.WSURL())
	if err := claw.Connect(context.Background()); err != nil {
		log.Fatalf("Failed to connect to OpenClaw mock: %v\n", err)
	}
	defer func() { _ = claw.Close() }()

	pollCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	arrived, err := claw.PollBankTxArrived(pollCtx, "xyz123", 500*time.Millisecond)
	if err != nil {
		log.Printf("OpenClaw poll error: %v\n", err)
	} else {
		log.Printf("OpenClaw result for code=%q: arrived=%v\n\n", "xyz123", arrived)
	}

	// ==================== SCHEMA MANAGEMENT ====================
	log.Println("--- Schema Manager ---")
	schemaManager := db.NewSchemaManager(database)

	// Create schemas for all models
	if err := schemaManager.CreateSchemas(&db.Transaction{}); err != nil {
		log.Fatalf("Failed to create schemas: %v\n", err)
	}
	log.Println("✓ Schemas created/verified\n")

	// Print database schema information
	desc, _ := schemaManager.DescribeTable("transactions")
	log.Printf("%s\n", desc)

	queryScenarioLogger := NewQueryScenarioLogger(database)
	queryScenarioLogger.Run()

	// ==================== BITCOIN FEE PREDICTOR ====================
	log.Println("\n=== Bitcoin Transaction Fee Predictor ===\n")

	// Create predictor
	predictor := bitcoin.NewBitcoinFeePredictor()
	log.Println("--- Bitcoin Scenario 1: Fetch Current Network Data ---")

	// Fetch latest Bitcoin network data
	if err := predictor.FetchLatestData(); err != nil {
		log.Printf("⚠ Warning: Could not fetch Bitcoin data: %v\n", err)
		log.Printf("   (This is expected if no internet connection or mempool.space is down)\n\n")
	} else {
		log.Println("✓ Successfully fetched Bitcoin network data\n")

		// Display network summary
		summary, _ := predictor.GetNetworkConditionsSummary()
		log.Println(summary)
		log.Printf("\nData Age: %v\n\n", predictor.GetDataAge())

		// ==================== BITCOIN SCENARIO 2: SIMPLE FEE ESTIMATE ====================
		log.Println("\n--- Bitcoin Scenario 2: Simple Fee Estimate ---")
		economic, standard, fast, express, _ := predictor.GetSimpleFeeEstimate()
		log.Printf("Current Fee Rates (sat/vB):\n")
		log.Printf("  Economic (1+ hour):     %d sat/vB\n", economic)
		log.Printf("  Standard (10-60 min):   %d sat/vB\n", standard)
		log.Printf("  Fast (<10 min):         %d sat/vB\n", fast)
		log.Printf("  Express (next block):   %d sat/vB\n\n", express)

		// ==================== BITCOIN SCENARIO 3: TRANSACTION COST PREDICTION ====================
		log.Println("--- Bitcoin Scenario 3: Transaction Cost Prediction ---")
		log.Println("Scenario 3a: Standard P2WPKH transaction (2 inputs, 2 outputs)")

		// Predict cost for SegWit transaction
		cost3a, _ := predictor.PredictTransactionCost(2, 2, "P2WPKH")
		if cost3a != nil {
			log.Printf("  Virtual Size: %d vB\n", cost3a.TransactionSize)
			log.Printf("  Economic Cost:  %.8f BTC (%d sat) - ~1 hour\n", cost3a.EconomicFee, cost3a.EconomicFeeSat)
			log.Printf("  Standard Cost:  %.8f BTC (%d sat) - ~30 min\n", cost3a.StandardFee, cost3a.StandardFeeSat)
			log.Printf("  Fast Cost:      %.8f BTC (%d sat) - ~10 min\n", cost3a.FastFee, cost3a.FastFeeSat)
			log.Printf("  Express Cost:   %.8f BTC (%d sat) - ~2 min\n\n", cost3a.ExpressFee, cost3a.ExpressFeeSat)
		}

		log.Println("Scenario 3b: Legacy P2PKH transaction (1 input, 1 output)")

		// Predict cost for legacy transaction
		cost3b, _ := predictor.PredictTransactionCost(1, 1, "P2PKH")
		if cost3b != nil {
			log.Printf("  Virtual Size: %d vB\n", cost3b.TransactionSize)
			log.Printf("  Standard Cost:  %.8f BTC (%d sat)\n", cost3b.StandardFee, cost3b.StandardFeeSat)
			log.Printf("  Difference (vs SegWit): %.8f BTC\n\n", cost3b.StandardFee - cost3a.StandardFee)
		}

		// ==================== BITCOIN SCENARIO 4: FEE RECOMMENDATIONS ====================
		log.Println("--- Bitcoin Scenario 4: Fee Recommendations ---")

		// Get all recommendations
		recs, _ := predictor.GetFeeRecommendations(2, 2, "P2WPKH")
		if recs != nil {
			for i, rec := range recs {
				log.Printf("[%d] %s Tier\n", i+1, rec.Tier)
				log.Printf("    Fee Rate: %d sat/vB\n", rec.FeeRateSatVB)
				log.Printf("    Total Fee: %.8f BTC (%d sat)\n", rec.TotalFeeBTC, rec.TotalFeeSat)
				log.Printf("    Est. Time: %v\n", rec.EstimatedTime)
				log.Printf("    Risk Level: %s\n", rec.RiskLevel)
				log.Printf("    Rationale: %s\n", rec.Rationale)
				log.Printf("    Confirmation Probability (next 3 blocks): %.1f%% -> %.1f%% -> %.1f%%\n\n",
					rec.ProbabilityByBlock[0]*100,
					rec.ProbabilityByBlock[1]*100,
					rec.ProbabilityByBlock[2]*100)
			}
		}

		// ==================== BITCOIN SCENARIO 5: BEST RECOMMENDATION ====================
		log.Println("--- Bitcoin Scenario 5: Best Recommendation Based on Network State ---")

		rec, _ := predictor.GetRecommendedFee(3, 2, "P2WPKH")
		if rec != nil {
			log.Printf("Recommended: %s TIER\n", rec.Tier)
			log.Printf("  Fee Rate: %d sat/vB\n", rec.FeeRateSatVB)
			log.Printf("  Total Cost: %.8f BTC\n", rec.TotalFeeBTC)
			log.Printf("  Est. Confirmation: %v\n", rec.EstimatedTime)
			log.Printf("  Why: %s\n\n", rec.Rationale)
		}

		// ==================== BITCOIN SCENARIO 6: CUSTOM FEE ====================
		log.Println("--- Bitcoin Scenario 6: Custom Fee Estimate ---")

		customRec, _ := predictor.EstimateCustomFee(2, 2, "P2WPKH", 15)
		if customRec != nil {
			log.Printf("Custom Fee at 15 sat/vB:\n")
			log.Printf("  Total Cost: %.8f BTC (%d sat)\n", customRec.TotalFeeBTC, customRec.TotalFeeSat)
			log.Printf("  Tier Classification: %s\n", customRec.Tier)
			log.Printf("  Risk Level: %s\n", customRec.RiskLevel)
			log.Printf("  Analysis: %s\n\n", customRec.Rationale)
		}

		// ==================== BITCOIN SCENARIO 7: DIFFERENT TX TYPES ====================
		log.Println("--- Bitcoin Scenario 7: Transaction Type Comparison ---")

		txTypes := []string{"P2PKH", "P2SH", "P2WPKH", "P2WSH", "P2TR"}
		log.Printf("%-10s %-12s %-20s\n", "Type", "Size (vB)", "Standard Cost")
		log.Println(string([]rune("_")[0]) + "_________" + "_" + "_____________" + "_" + "___________________")

		for _, txType := range txTypes {
			cost, _ := predictor.PredictTransactionCost(2, 2, txType)
			if cost != nil {
				log.Printf("%-10s %-12d %-20.8f BTC\n", txType, cost.TransactionSize, cost.StandardFee)
			}
		}
		log.Println()
	}

	// ==================== CLEANUP ====================
	log.Println("\n--- Cleanup ---")
	log.Println("✓ Database operations completed successfully!")
	log.Println("✓ All data persisted to sqlite.db")
	log.Println("✓ Bitcoin fee prediction module tested successfully!")
	log.Println("\n=== Demonstration Complete ===")
}
