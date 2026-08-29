package bisq

// Bisq emits trade phase and state as enum names rather than enum numbers. Keep
// their ordering here so callers do not need to depend on Bisq protocol strings.
var tradePhaseRank = map[string]int{
	"INIT":                1,
	"TAKER_FEE_PUBLISHED": 2,
	"DEPOSIT_PUBLISHED":   3,
	"DEPOSIT_CONFIRMED":   4,
	"FIAT_SENT":           5,
	"FIAT_RECEIVED":       6,
	"PAYOUT_PUBLISHED":    7,
	"WITHDRAWN":           8,
}

var tradeStateRank = map[string]int{
	"PREPARATION":                                        1,
	"TAKER_PUBLISHED_TAKER_FEE_TX":                       2,
	"MAKER_SENT_PUBLISH_DEPOSIT_TX_REQUEST":              3,
	"MAKER_SAW_ARRIVED_PUBLISH_DEPOSIT_TX_REQUEST":       4,
	"MAKER_STORED_IN_MAILBOX_PUBLISH_DEPOSIT_TX_REQUEST": 5,
	"MAKER_SEND_FAILED_PUBLISH_DEPOSIT_TX_REQUEST":       6,
	"TAKER_RECEIVED_PUBLISH_DEPOSIT_TX_REQUEST":          7,
	"SELLER_PUBLISHED_DEPOSIT_TX":                        8,
	"SELLER_SENT_DEPOSIT_TX_PUBLISHED_MSG":               9,
	"SELLER_SAW_ARRIVED_DEPOSIT_TX_PUBLISHED_MSG":        10,
	"SELLER_STORED_IN_MAILBOX_DEPOSIT_TX_PUBLISHED_MSG":  11,
	"SELLER_SEND_FAILED_DEPOSIT_TX_PUBLISHED_MSG":        12,
	"BUYER_RECEIVED_DEPOSIT_TX_PUBLISHED_MSG":            13,
	"BUYER_SAW_DEPOSIT_TX_IN_NETWORK":                    14,
	"DEPOSIT_CONFIRMED_IN_BLOCK_CHAIN":                   15,
	"BUYER_CONFIRMED_IN_UI_FIAT_PAYMENT_INITIATED":       16,
	"BUYER_SENT_FIAT_PAYMENT_INITIATED_MSG":              17,
	"BUYER_SAW_ARRIVED_FIAT_PAYMENT_INITIATED_MSG":       18,
	"BUYER_STORED_IN_MAILBOX_FIAT_PAYMENT_INITIATED_MSG": 19,
	"BUYER_SEND_FAILED_FIAT_PAYMENT_INITIATED_MSG":       20,
	"SELLER_RECEIVED_FIAT_PAYMENT_INITIATED_MSG":         21,
	"SELLER_CONFIRMED_IN_UI_FIAT_PAYMENT_RECEIPT":        22,
	"SELLER_PUBLISHED_PAYOUT_TX":                         23,
	"SELLER_SENT_PAYOUT_TX_PUBLISHED_MSG":                24,
	"SELLER_SAW_ARRIVED_PAYOUT_TX_PUBLISHED_MSG":         25,
	"SELLER_STORED_IN_MAILBOX_PAYOUT_TX_PUBLISHED_MSG":   26,
	"SELLER_SEND_FAILED_PAYOUT_TX_PUBLISHED_MSG":         27,
	"BUYER_RECEIVED_PAYOUT_TX_PUBLISHED_MSG":             28,
	"BUYER_SAW_PAYOUT_TX_IN_NETWORK":                     29,
	"WITHDRAW_COMPLETED":                                 30,
}

// FiatSent reports whether Bisq has reached the point where the BTC buyer says
// fiat was sent. The explicit daemon boolean is preferred, with phase and state
// names retained as compatibility fallbacks for daemon versions that omit it.
func (t *Trade) FiatSent() bool {
	if t == nil {
		return false
	}
	if t.IsPaymentStartedMessageSent {
		return true
	}
	if rank, ok := tradePhaseRank[t.Phase]; ok && rank >= tradePhaseRank["FIAT_SENT"] {
		return true
	}
	if rank, ok := tradeStateRank[t.State]; ok && rank >= tradeStateRank["BUYER_SENT_FIAT_PAYMENT_INITIATED_MSG"] {
		return true
	}
	return false
}

// PayoutPublished reports whether Bisq has published the payout transaction.
// The daemon boolean is preferred, with phase and state names retained as
// compatibility fallbacks for daemon versions that omit it.
func (t *Trade) PayoutPublished() bool {
	if t == nil {
		return false
	}
	if t.IsPayoutPublished {
		return true
	}
	if rank, ok := tradePhaseRank[t.Phase]; ok && rank >= tradePhaseRank["PAYOUT_PUBLISHED"] {
		return true
	}
	if rank, ok := tradeStateRank[t.State]; ok && rank >= tradeStateRank["SELLER_PUBLISHED_PAYOUT_TX"] {
		return true
	}
	return false
}
