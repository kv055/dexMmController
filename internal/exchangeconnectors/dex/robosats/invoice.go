package robosats

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	lnd "dexMmController/internal/wallethub"
)

func validateHoldInvoice(invoice string, d *lnd.DecodedInvoice, capSat int64, now time.Time) (amountSat int64, amountMsat int64, err error) {
	if d == nil {
		return 0, 0, fmt.Errorf("robosats: invoice decode returned no data")
	}
	lower := strings.ToLower(strings.TrimSpace(invoice))
	if !strings.HasPrefix(lower, "lnbc") {
		return 0, 0, fmt.Errorf("robosats: invoice network is not Bitcoin mainnet")
	}
	if len(lower) < 6 || lower[4] < '0' || lower[4] > '9' {
		return 0, 0, fmt.Errorf("robosats: expected an amount-bearing BOLT11")
	}
	amountSat, err = strconv.ParseInt(strings.TrimSpace(d.NumSatoshis), 10, 64)
	if err != nil || amountSat <= 0 || amountSat > math.MaxInt64/1000 {
		return 0, 0, fmt.Errorf("robosats: invoice has invalid amount")
	}
	amountMsat = amountSat * 1000
	if strings.TrimSpace(d.NumMSatoshis) != "" {
		msat, merr := strconv.ParseInt(strings.TrimSpace(d.NumMSatoshis), 10, 64)
		if merr != nil || msat <= 0 {
			return 0, 0, fmt.Errorf("robosats: invoice millisatoshi amount is invalid")
		}
		amountMsat = msat
		amountSat = msat / 1000
		if msat%1000 != 0 {
			amountSat++
		}
	}
	if capSat <= 0 || capSat > math.MaxInt64/1000 || amountMsat > capSat*1000 {
		return 0, 0, fmt.Errorf("robosats: invoice %d msat exceeds confirmed cap %d sat", amountMsat, capSat)
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(d.Timestamp), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("robosats: invoice timestamp is invalid")
	}
	expiry, err := strconv.ParseInt(strings.TrimSpace(d.Expiry), 10, 64)
	if err != nil || expiry <= 0 || expiry > math.MaxInt64/int64(time.Second) {
		return 0, 0, fmt.Errorf("robosats: invoice expiry is invalid")
	}
	if !time.Unix(ts, 0).Add(time.Duration(expiry) * time.Second).After(now.Add(30 * time.Second)) {
		return 0, 0, fmt.Errorf("robosats: invoice expired or too close to expiry")
	}
	if strings.TrimSpace(d.Destination) == "" || strings.TrimSpace(d.PaymentHash) == "" {
		return 0, 0, fmt.Errorf("robosats: invoice destination or payment hash is missing")
	}
	return amountSat, amountMsat, nil
}
