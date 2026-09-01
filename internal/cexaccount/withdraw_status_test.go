package cexaccount

import (
	"testing"

	"dexMmController/internal/exchangeconnectors/cex/coinbase"
)

func TestCoinbaseWithdrawalStatusNormalizesConfirmedAndFee(t *testing.T) {
	tx := &coinbase.Transaction{ID: "tx-1", Status: "pending", UpdatedAt: "2026-09-01T10:00:00Z"}
	tx.Network.Status = "confirmed"
	tx.Network.Hash = "hash-1"
	tx.Network.TransactionFee.Value = "0.000123"
	tx.To.Address = "bc1qmanaged"
	tx.Amount.Value = "-0.01"
	status := coinbaseWithdrawalStatusFromTransaction(tx)
	if status.CexStatus != "completed" || status.CexStatusCode != 6 || status.FeeBTC != 0.000123 {
		t.Fatalf("status = %#v", status)
	}
	if status.AmountBTC != "0.01" || status.TxHash != "hash-1" {
		t.Fatalf("status = %#v", status)
	}
}
