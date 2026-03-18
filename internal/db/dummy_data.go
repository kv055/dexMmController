package db

import (
	"time"
	"gorm.io/datatypes"
)

// DummyDataGenerator creates sample transaction data for testing
type DummyDataGenerator struct{}

// NewDummyDataGenerator creates a new dummy data generator
func NewDummyDataGenerator() *DummyDataGenerator {
	return &DummyDataGenerator{}
}

// GenerateSingleTransaction generates a single sample transaction
func (d *DummyDataGenerator) GenerateSingleTransaction(direction string, amount float64, cex CexExchange) *Transaction {
	return &Transaction{
		Direction:                    direction,
		FiatAmount:                   amount,
		FiatCurrency:                 "USD",
		Cex:                          cex,
		CexHedgingPosition:           CexKraken,
		Dex:                          DexBisq,
		Status:                       StatusDexPositionReleased,
		TransferFiatToCexStartTime:   time.Now(),
		TransferFiatToCexArrivalTime: time.Now().Add(10 * time.Minute),

		CexTransactionDetails:       datatypes.JSON([]byte(`{"status":"completed","amount":0.5}`)),
		CexTransactionTimestamp:     time.Now().Add(15 * time.Minute),
		CexTransactionFee:           5.0,
		CexToDexWithdrawalTxDetails: datatypes.JSON([]byte(`{"hash":"0x123abc","status":"success"}`)),
		CexWithdrawalFee:            2.5,

		CexCounterPositionOpenDetails:  datatypes.JSON([]byte(`{"leverage":2.0,"entry_price":40000}`)),
		CexCounterPositionCloseDetails: datatypes.JSON([]byte(`{"exit_price":41000,"pnl":500}`)),
		CexCounterPositionFee:          1.0,

		DexOfferCreation:   datatypes.JSON([]byte(`{"offer_id":"DEX001","created_at":"2026-03-15"}`)),
		DexOfferTaken:      time.Now().Add(1 * time.Hour),
		DexFiatArrival:     datatypes.JSON([]byte(`{"amount":1000,"timestamp":"2026-03-15T12:00:00Z"}`)),
		DexBtcRelease:      time.Now().Add(2 * time.Hour),
		DexFees:            0.5,

		OpenClawLlmInferenceCost: 0.02,
		VpsHostingCost:           5.0,

		TotalTransactionPnl:                      50.0,
		TotalTransactionTimeElapsed:              7200,
		TotalTransactionVolatilityInBtcPrice:     0.03,
		PremiumOverCexPriceToBreakEvenPrediction: 0.015,
	}
}

// GenerateMultipleTransactions generates multiple sample transactions
func (d *DummyDataGenerator) GenerateMultipleTransactions(count int) []*Transaction {
	transactions := make([]*Transaction, 0)

	cexes := []CexExchange{CexKraken, CexBinance, CexCoinbase, CexBitStamp}
	directions := []string{"BUY", "SELL"}
	amounts := []float64{1000, 1500, 2000, 2500, 3000}

	for i := 0; i < count; i++ {
		cex := cexes[i%len(cexes)]
		direction := directions[i%len(directions)]
		amount := amounts[i%len(amounts)]

		tx := d.GenerateSingleTransaction(direction, amount, cex)
		// Vary the data slightly
		tx.TotalTransactionPnl = float64(i*10) - 50
		tx.TotalTransactionTimeElapsed = int64(3600 + i*600)
		tx.TotalTransactionVolatilityInBtcPrice = 0.02 + float64(i)*0.005

		transactions = append(transactions, tx)
	}

	return transactions
}

// GenerateBuyTransactions generates sample BUY transactions
func (d *DummyDataGenerator) GenerateBuyTransactions(count int) []*Transaction {
	transactions := make([]*Transaction, 0)
	cexes := []CexExchange{CexKraken, CexBinance, CexCoinbase}

	for i := 0; i < count; i++ {
		cex := cexes[i%len(cexes)]
		amount := 1000.0 + float64(i*500)

		tx := d.GenerateSingleTransaction("BUY", amount, cex)
		tx.TotalTransactionPnl = float64(i * 25)
		transactions = append(transactions, tx)
	}

	return transactions
}

// GenerateSellTransactions generates sample SELL transactions
func (d *DummyDataGenerator) GenerateSellTransactions(count int) []*Transaction {
	transactions := make([]*Transaction, 0)
	cexes := []CexExchange{CexBinance, CexCoinbase, CexBybit}

	for i := 0; i < count; i++ {
		cex := cexes[i%len(cexes)]
		amount := 1500.0 + float64(i*600)

		tx := d.GenerateSingleTransaction("SELL", amount, cex)
		tx.TotalTransactionPnl = float64(i * 20)
		transactions = append(transactions, tx)
	}

	return transactions
}

// GenerateHighProfitTransactions generates transactions with high PnL
func (d *DummyDataGenerator) GenerateHighProfitTransactions(count int) []*Transaction {
	transactions := make([]*Transaction, 0)

	for i := 0; i < count; i++ {
		direction := map[int]string{0: "BUY", 1: "SELL"}[i%2]
		cex := []CexExchange{CexKraken, CexBinance}[i%2]

		tx := d.GenerateSingleTransaction(direction, 5000+float64(i*1000), cex)
		tx.TotalTransactionPnl = 500.0 + float64(i*100) // High profits
		tx.TotalTransactionVolatilityInBtcPrice = 0.05 + float64(i)*0.01

		transactions = append(transactions, tx)
	}

	return transactions
}

// GenerateLossingTransactions generates transactions with losses
func (d *DummyDataGenerator) GenerateLossingTransactions(count int) []*Transaction {
	transactions := make([]*Transaction, 0)

	for i := 0; i < count; i++ {
		direction := map[int]string{0: "BUY", 1: "SELL"}[i%2]
		cex := []CexExchange{CexCoinbase, CexGemini}[i%2]

		tx := d.GenerateSingleTransaction(direction, 2000+float64(i*500), cex)
		tx.TotalTransactionPnl = -100.0 - float64(i*50) // Losses
		tx.TotalTransactionVolatilityInBtcPrice = 0.08 + float64(i)*0.02

		transactions = append(transactions, tx)
	}

	return transactions
}

// GenerateKrakenTransactions generates transactions for Kraken exchange
func (d *DummyDataGenerator) GenerateKrakenTransactions(count int) []*Transaction {
	transactions := make([]*Transaction, 0)
	directions := []string{"BUY", "SELL"}

	for i := 0; i < count; i++ {
		direction := directions[i%len(directions)]
		amount := 1000.0 + float64(i*800)

		tx := d.GenerateSingleTransaction(direction, amount, CexKraken)
		tx.TotalTransactionPnl = float64((i - count/2) * 30)
		transactions = append(transactions, tx)
	}

	return transactions
}

// GenerateBinanceTransactions generates transactions for Binance exchange
func (d *DummyDataGenerator) GenerateBinanceTransactions(count int) []*Transaction {
	transactions := make([]*Transaction, 0)
	directions := []string{"BUY", "SELL"}

	for i := 0; i < count; i++ {
		direction := directions[i%len(directions)]
		amount := 1500.0 + float64(i*700)

		tx := d.GenerateSingleTransaction(direction, amount, CexBinance)
		tx.TotalTransactionPnl = float64((i - count/2) * 35)
		transactions = append(transactions, tx)
	}

	return transactions
}

// SampleScenario1 returns a realistic trading scenario
func (d *DummyDataGenerator) SampleScenario1() []*Transaction {
	transactions := make([]*Transaction, 0)

	// Start with 2 BUY transactions on Kraken
	transactions = append(transactions, d.GenerateKrakenTransactions(2)...)

	// Then 3 SELL transactions on Binance
	transactions = append(transactions, d.GenerateBinanceTransactions(3)...)

	// Add some mixed high profit transactions
	transactions = append(transactions, d.GenerateHighProfitTransactions(2)...)

	return transactions
}

// SampleScenario2 returns a volatile trading scenario
func (d *DummyDataGenerator) SampleScenario2() []*Transaction {
	transactions := make([]*Transaction, 0)

	// Mix of buy and sell
	transactions = append(transactions, d.GenerateBuyTransactions(3)...)
	transactions = append(transactions, d.GenerateSellTransactions(2)...)
	transactions = append(transactions, d.GenerateLossingTransactions(2)...)

	return transactions
}

// All generates all types of transactions for comprehensive testing
func (d *DummyDataGenerator) All(count int) []*Transaction {
	transactions := make([]*Transaction, 0)
	transactions = append(transactions, d.GenerateMultipleTransactions(count)...)
	return transactions
}
