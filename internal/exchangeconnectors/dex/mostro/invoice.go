package mostro

import (
	lnd "dexMmController/internal/wallethub"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func validateInvoice(invoice string, d *lnd.DecodedInvoice, r TakeRequest, purpose string) (int64, error) {
	if d != nil && r.LightningNode != "" && d.Destination != r.LightningNode {
		return 0, fmt.Errorf("mostro: invoice destination differs from signed node identity")
	}
	// Mainnet amount-bearing BOLT11, never an amountless invoice or another chain.
	if d == nil || len(invoice) < 6 || !strings.HasPrefix(invoice, "lnbc") || invoice[4] < '0' || invoice[4] > '9' {
		return 0, fmt.Errorf("mostro: expected a mainnet amount-bearing BOLT11")
	}
	amount, err := strconv.ParseInt(d.NumSatoshis, 10, 64)
	if err != nil || amount <= 0 {
		return 0, fmt.Errorf("mostro: invalid invoice amount")
	}
	if d.NumMSatoshis != "" {
		msat, err := strconv.ParseInt(d.NumMSatoshis, 10, 64)
		if err != nil || msat/1000 != amount || msat%1000 != 0 {
			return 0, fmt.Errorf("mostro: invoice must use whole satoshis")
		}
	}
	ceiling := r.MaxEscrowSat
	if purpose == "bond" {
		ceiling = r.MaxBondSat
	}
	if ceiling <= 0 || amount > ceiling {
		return 0, fmt.Errorf("mostro: %s invoice exceeds its approved %d sat ceiling", purpose, ceiling)
	}
	stamp, e1 := strconv.ParseInt(d.Timestamp, 10, 64)
	expiry, e2 := strconv.ParseInt(d.Expiry, 10, 64)
	if e1 != nil || e2 != nil || expiry <= 0 || stamp < 0 || stamp > time.Now().Add(time.Minute).Unix() || expiry > 1<<31 || stamp+expiry <= time.Now().Add(30*time.Second).Unix() {
		return 0, fmt.Errorf("mostro: invoice expired or too close to expiry")
	}
	hash, err := hex.DecodeString(d.PaymentHash)
	if err != nil || len(hash) != 32 {
		return 0, fmt.Errorf("mostro: invalid payment hash")
	}
	return amount, nil
}
