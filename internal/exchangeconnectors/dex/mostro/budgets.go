package mostro

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/nbd-wtf/go-nostr"
)

// checkBudgets checks signed economics before the CEX leg or a taker bond.
// Market-priced/range orders have no committed satoshi amount before take.
// They require a separate firm-quote protocol and are deliberately rejected.
func checkBudgets(node, order nostr.Event, r TakeRequest, bond bool) error {
	for _, key := range []string{"amt", "fa"} {
		count := 0
		for _, t := range order.Tags {
			if len(t) > 0 && t[0] == key {
				count++
				if len(t) != 2 {
					return fmt.Errorf("mostro: fixed, unambiguous %s required for budget validation", key)
				}
			}
		}
		if count != 1 {
			return fmt.Errorf("mostro: missing or duplicate %s", key)
		}
	}
	amount, err := strconv.ParseInt(tag(order, "amt"), 10, 64)
	if err != nil || amount <= 0 {
		return fmt.Errorf("mostro: floating or invalid satoshi quote cannot be budgeted before take")
	}
	// Mostro util::get_fee splits the published fee between both parties;
	// show_hold_invoice charges amount + seller fee. Round upward to keep a
	// conservative ceiling across daemon floating-point rounding differences.
	fee, err := fraction(node, "fee", "0.05")
	if err != nil {
		return err
	}
	fee.Quo(fee, big.NewRat(2, 1))
	escrow := new(big.Int).Add(big.NewInt(amount), ceilProduct(amount, fee))
	if !escrow.IsInt64() || escrow.Int64() > r.MaxEscrowSat {
		return fmt.Errorf("mostro: escrow budget %d is below signed amount plus fee %s", r.MaxEscrowSat, escrow)
	}
	if !bond {
		return nil
	}
	pct, err := fraction(node, "bond_amount_pct", "1")
	if err != nil {
		return err
	}
	floorText, err := singleValue(node, "bond_base_amount_sats")
	if err != nil {
		return err
	}
	floor, err := strconv.ParseInt(floorText, 10, 64)
	if err != nil || floor < 0 {
		return fmt.Errorf("mostro: invalid bond floor")
	}
	required := ceilProduct(amount, pct)
	if required.Cmp(big.NewInt(floor)) < 0 {
		required.SetInt64(floor)
	}
	if required.Sign() <= 0 || !required.IsInt64() || required.Int64() > r.MaxBondSat {
		return fmt.Errorf("mostro: bond budget %d is below required bond %s", r.MaxBondSat, required)
	}
	return nil
}
func singleValue(e nostr.Event, key string) (string, error) {
	value, count := "", 0
	for _, t := range e.Tags {
		if len(t) > 0 && t[0] == key {
			count++
			if len(t) != 2 {
				return "", fmt.Errorf("mostro: invalid %s", key)
			}
			value = t[1]
		}
	}
	if count != 1 || value == "" {
		return "", fmt.Errorf("mostro: missing or ambiguous %s", key)
	}
	return value, nil
}
func fraction(e nostr.Event, key, maximum string) (*big.Rat, error) {
	value, err := singleValue(e, key)
	if err != nil {
		return nil, err
	}
	// Accept decimal wire values, not Rat's fractions or other non-wire forms.
	for _, c := range value {
		if (c < '0' || c > '9') && c != '.' {
			return nil, fmt.Errorf("mostro: invalid %s", key)
		}
	}
	n, ok := new(big.Rat).SetString(value)
	max, _ := new(big.Rat).SetString(maximum)
	if !ok || n.Sign() < 0 || n.Cmp(max) > 0 {
		return nil, fmt.Errorf("mostro: invalid %s", key)
	}
	return n, nil
}
func ceilProduct(amount int64, fraction *big.Rat) *big.Int {
	numerator := new(big.Int).Mul(big.NewInt(amount), fraction.Num())
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(numerator, fraction.Denom(), rem)
	if rem.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}
