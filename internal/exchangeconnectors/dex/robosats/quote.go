package robosats

import (
	"commons/fiat"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type QuoteInput struct {
	CoordinatorID string `json:"venue_coordinator_id"`
	OrderID       string `json:"offer_id"`
	Amount        string `json:"amount"`
	Unit          string `json:"unit"`
	Currency      string `json:"fiat_currency"`
	PaymentMethod string `json:"payment_method"`
}
type NativeTerms struct {
	CoordinatorID string    `json:"venue_coordinator_id"`
	OrderID       string    `json:"offer_id"`
	FiatAmount    string    `json:"fiat_amount"`
	Currency      string    `json:"fiat_currency"`
	PaymentMethod string    `json:"payment_method"`
	Price         string    `json:"price"`
	BondPercent   string    `json:"bond_percent"`
	TakerFee      string    `json:"taker_fee"`
	PrincipalSat  int64     `json:"principal_sat"`
	MaxBondSat    int64     `json:"max_bond_sat"`
	MaxEscrowSat  int64     `json:"max_escrow_sat"`
	NodePubKey    string    `json:"node_pubkey"`
	ExpiresAt     time.Time `json:"expires_at"`
}

var decimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func decimal(s string) (*big.Rat, error) {
	if !decimalPattern.MatchString(s) {
		return nil, fmt.Errorf("invalid decimal")
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid decimal")
	}
	return v, nil
}
func ceilSats(v *big.Rat) (int64, error) {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(v.Num(), v.Denom(), r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Sign() <= 0 || q.Cmp(big.NewInt(2100000000000000)) > 0 {
		return 0, fmt.Errorf("amount outside Bitcoin supply")
	}
	return q.Int64(), nil
}

// Quote uses authenticated fresh order/info responses, never the CEX reference price.
func (c *Client) Quote(ctx context.Context, in QuoteInput) (*NativeTerms, error) {
	robot, err := c.GetRobot(ctx)
	if err != nil {
		return nil, err
	}
	if active := robotString(robot, "active_order_id"); active != "" {
		return nil, fmt.Errorf("robot already has an active order")
	}
	p, err := c.GetPosition(ctx, in.OrderID)
	if err != nil {
		return nil, err
	}
	var info struct {
		Network string      `json:"network"`
		Node    string      `json:"node_id"`
		Fee     json.Number `json:"taker_fee"`
	}
	if err = c.request(ctx, http.MethodGet, "/api/info/", nil, &info); err != nil {
		return nil, err
	}
	if info.Network != "mainnet" || len(info.Node) != 66 {
		return nil, fmt.Errorf("mainnet coordinator node identity unavailable")
	}
	return quoteFromPosition(p, in, info.Fee.String(), info.Node, time.Now())
}
func quoteFromPosition(p *Position, in QuoteInput, feeString, node string, now time.Time) (*NativeTerms, error) {
	if p == nil || p.ID != in.OrderID || p.Type != TypeBuy || p.Status != StatusPublic || p.IsMaker || p.IsTaker {
		return nil, fmt.Errorf("public maker-buy offer required")
	}
	var raw struct {
		Currency int         `json:"currency"`
		Price    json.Number `json:"price_now"`
		Bond     json.Number `json:"bond_size"`
		Password bool        `json:"has_password"`
	}
	if json.Unmarshal(p.Raw, &raw) != nil || raw.Password || raw.Currency == 0 || raw.Currency != fiat.RoboSatsCurrencyID(in.Currency) {
		return nil, fmt.Errorf("offer currency or access terms mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(p.PaymentMethod), strings.TrimSpace(in.PaymentMethod)) {
		return nil, fmt.Errorf("payment method must match coordinator terms exactly")
	}
	price, err := decimal(raw.Price.String())
	if err != nil || price.Sign() <= 0 {
		return nil, fmt.Errorf("coordinator price unavailable")
	}
	bond, err := decimal(raw.Bond.String())
	if err != nil || bond.Sign() <= 0 || bond.Cmp(big.NewRat(100, 1)) > 0 {
		return nil, fmt.Errorf("invalid bond percentage")
	}
	fee, err := decimal(feeString)
	if err != nil || fee.Cmp(big.NewRat(1, 1)) >= 0 {
		return nil, fmt.Errorf("invalid coordinator fee")
	}
	amount, err := decimal(in.Amount)
	if err != nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("invalid amount")
	}
	if in.Unit == "btc" {
		amount.Mul(amount, price)
	} else if in.Unit != "fiat" {
		return nil, fmt.Errorf("invalid unit")
	}
	// Fiat precision is explicitly confirmed to two decimals; never truncate silently.
	fiatString := amount.FloatString(2)
	amount, err = decimal(fiatString)
	if err != nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("invalid fiat amount")
	}
	min, max := p.Amount.String(), p.Amount.String()
	if p.HasRange {
		min, max = p.MinAmount.String(), p.MaxAmount.String()
	}
	lo, e1 := decimal(min)
	hi, e2 := decimal(max)
	if e1 != nil || e2 != nil || amount.Cmp(lo) < 0 || amount.Cmp(hi) > 0 {
		return nil, fmt.Errorf("amount outside coordinator offer limits")
	}
	sats := new(big.Rat).Mul(new(big.Rat).Quo(amount, price), big.NewRat(100000000, 1))
	principal, err := ceilSats(sats)
	if err != nil {
		return nil, err
	}
	bondSat, err := ceilSats(new(big.Rat).Mul(big.NewRat(principal, 1), new(big.Rat).Quo(bond, big.NewRat(100, 1))))
	if err != nil {
		return nil, err
	}
	escrow, err := ceilSats(new(big.Rat).Mul(big.NewRat(principal, 1), new(big.Rat).Add(big.NewRat(1, 1), fee)))
	if err != nil {
		return nil, err
	}
	expires, err := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if err != nil || !expires.After(now.Add(30*time.Second)) {
		return nil, fmt.Errorf("offer deadline unavailable or expired")
	}
	return &NativeTerms{CoordinatorID: in.CoordinatorID, OrderID: in.OrderID, FiatAmount: fiatString, Currency: in.Currency, PaymentMethod: in.PaymentMethod, Price: raw.Price.String(), BondPercent: raw.Bond.String(), TakerFee: feeString, PrincipalSat: principal, MaxBondSat: bondSat, MaxEscrowSat: escrow, NodePubKey: node, ExpiresAt: expires}, nil
}

func (t *NativeTerms) CheckPosition(p *Position, now time.Time) error {
	fresh, err := quoteFromPosition(p, QuoteInput{CoordinatorID: t.CoordinatorID, OrderID: t.OrderID, Amount: t.FiatAmount, Unit: "fiat", Currency: t.Currency, PaymentMethod: t.PaymentMethod}, t.TakerFee, t.NodePubKey, now)
	if err != nil {
		return err
	}
	if fresh.Price != t.Price || fresh.BondPercent != t.BondPercent || fresh.MaxBondSat > t.MaxBondSat || fresh.MaxEscrowSat > t.MaxEscrowSat {
		return fmt.Errorf("coordinator terms changed; confirmation required")
	}
	return nil
}
