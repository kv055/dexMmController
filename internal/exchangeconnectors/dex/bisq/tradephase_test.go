package bisq

import "testing"

func TestTradeFiatSent(t *testing.T) {
	tests := []struct {
		name  string
		trade *Trade
		want  bool
	}{
		{name: "nil trade", trade: nil},
		{name: "explicit daemon signal", trade: &Trade{IsPaymentStartedMessageSent: true}, want: true},
		{name: "fiat sent phase", trade: &Trade{Phase: "FIAT_SENT"}, want: true},
		{name: "later phase", trade: &Trade{Phase: "PAYOUT_PUBLISHED"}, want: true},
		{name: "payment message sent state", trade: &Trade{State: "BUYER_SENT_FIAT_PAYMENT_INITIATED_MSG"}, want: true},
		{name: "payment message send failed state", trade: &Trade{State: "BUYER_SEND_FAILED_FIAT_PAYMENT_INITIATED_MSG"}, want: true},
		{name: "later state", trade: &Trade{State: "SELLER_RECEIVED_FIAT_PAYMENT_INITIATED_MSG"}, want: true},
		{name: "deposit phase", trade: &Trade{Phase: "DEPOSIT_CONFIRMED"}},
		{name: "buyer only clicked ui", trade: &Trade{State: "BUYER_CONFIRMED_IN_UI_FIAT_PAYMENT_INITIATED"}},
		{name: "unknown names", trade: &Trade{Phase: "FUTURE_PHASE", State: "FUTURE_STATE"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.trade.FiatSent(); got != test.want {
				t.Fatalf("FiatSent() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTradePayoutPublished(t *testing.T) {
	tests := []struct {
		name  string
		trade *Trade
		want  bool
	}{
		{name: "nil trade", trade: nil},
		{name: "explicit daemon signal", trade: &Trade{IsPayoutPublished: true}, want: true},
		{name: "payout phase", trade: &Trade{Phase: "PAYOUT_PUBLISHED"}, want: true},
		{name: "withdrawn phase", trade: &Trade{Phase: "WITHDRAWN"}, want: true},
		{name: "payout state", trade: &Trade{State: "SELLER_PUBLISHED_PAYOUT_TX"}, want: true},
		{name: "later payout state", trade: &Trade{State: "BUYER_RECEIVED_PAYOUT_TX_PUBLISHED_MSG"}, want: true},
		{name: "withdraw completed", trade: &Trade{State: "WITHDRAW_COMPLETED"}, want: true},
		{name: "fiat received phase", trade: &Trade{Phase: "FIAT_RECEIVED"}},
		{name: "payment received message only", trade: &Trade{IsPaymentReceivedMessageSent: true}},
		{name: "unknown names", trade: &Trade{Phase: "FUTURE_PHASE", State: "FUTURE_STATE"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.trade.PayoutPublished(); got != test.want {
				t.Fatalf("PayoutPublished() = %v, want %v", got, test.want)
			}
		})
	}
}
