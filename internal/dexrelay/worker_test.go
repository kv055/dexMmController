package dexrelay

import (
	"encoding/json"
	"testing"

	"dexMmController/internal/db"
)

func TestRelayAlreadyCompleted(t *testing.T) {
	if shouldSkipRelay(nil) {
		t.Fatal("nil details should not skip")
	}
	raw, _ := json.Marshal(db.CexToDexRelayDetails{TxID: "deadbeef"})
	if !shouldSkipRelay(raw) {
		t.Fatal("txid should skip relay")
	}
}

func shouldSkipRelay(raw json.RawMessage) bool {
	return db.HasRelayCompleted(raw)
}
