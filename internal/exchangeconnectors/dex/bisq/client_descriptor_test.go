package bisq

import "testing"

func TestVersionDescriptor(t *testing.T) {
	if msg := newMessage("GetVersionRequest"); msg == nil {
		t.Fatal("descriptor missing GetVersionRequest")
	}
	reply := newMessage("GetVersionReply")
	if reply.Descriptor().Fields().ByName("version") == nil {
		t.Fatal("descriptor missing GetVersionReply.version")
	}
}

// TestFundingAddressDescriptor builds the proto descriptor (protodesc.NewFile panics on
// a malformed set) and verifies the new Wallets/GetFundingAddresses messages resolve.
func TestFundingAddressDescriptor(t *testing.T) {
	for _, name := range []string{"GetFundingAddressesRequest", "GetFundingAddressesReply", "AddressBalanceInfo"} {
		if msg := newMessage(name); msg == nil {
			t.Fatalf("descriptor missing message %s", name)
		}
	}
}

func TestWithdrawFundsDescriptor(t *testing.T) {
	for _, name := range []string{"WithdrawFundsRequest", "WithdrawFundsReply"} {
		if msg := newMessage(name); msg == nil {
			t.Fatalf("descriptor missing message %s", name)
		}
	}
	req := newMessage("WithdrawFundsRequest")
	setString(req, "trade_id", "trade-1")
	setString(req, "address", "bc1qpool")
	setString(req, "memo", "hedgeless-deposit-return-42")
	if got := stringField(req, "trade_id"); got != "trade-1" {
		t.Fatalf("trade_id = %q", got)
	}
	if got := stringField(req, "address"); got != "bc1qpool" {
		t.Fatalf("address = %q", got)
	}
}

func TestTradeInfoPaymentStateDescriptor(t *testing.T) {
	msg := newMessage("TradeInfo")
	setString(msg, "phase", "FIAT_SENT")
	setBool(msg, "is_payment_started_message_sent", true)
	setBool(msg, "is_payment_received_message_sent", true)
	setBool(msg, "is_payout_published", true)

	trade := tradeFromMessage(msg)
	if !trade.IsPaymentStartedMessageSent {
		t.Fatal("payment-started signal was not decoded")
	}
	if !trade.IsPaymentReceivedMessageSent {
		t.Fatal("payment-received signal was not decoded")
	}
	if !trade.IsPayoutPublished {
		t.Fatal("payout-published signal was not decoded")
	}
}

// TestPaymentAccountMutationDescriptor verifies the patched CreateSepaPaymentAccount /
// DeletePaymentAccount messages resolve and their request fields are settable.
func TestPaymentAccountMutationDescriptor(t *testing.T) {
	for _, name := range []string{
		"GetPaymentMethodsRequest", "GetPaymentMethodsReply",
		"GetPaymentAccountFormRequest", "GetPaymentAccountFormReply",
		"CreatePaymentAccountRequest", "CreatePaymentAccountReply",
		"CreateSepaPaymentAccountRequest", "CreateSepaPaymentAccountReply",
		"DeletePaymentAccountRequest", "DeletePaymentAccountReply",
	} {
		if msg := newMessage(name); msg == nil {
			t.Fatalf("descriptor missing message %s", name)
		}
	}

	formReq := newMessage("GetPaymentAccountFormRequest")
	setString(formReq, "payment_method_id", "REVOLUT")
	if got := stringField(formReq, "payment_method_id"); got != "REVOLUT" {
		t.Fatalf("payment_method_id = %q", got)
	}
	createReq := newMessage("CreatePaymentAccountRequest")
	setString(createReq, "payment_account_form", `{"paymentMethodId":"REVOLUT"}`)
	if got := stringField(createReq, "payment_account_form"); got == "" {
		t.Fatal("generic payment_account_form was not set")
	}

	req := newMessage("CreateSepaPaymentAccountRequest")
	setString(req, "account_name", "Main")
	setString(req, "holder_name", "Alice")
	setString(req, "iban", "DE89370400440532013000")
	setString(req, "bic", "COBADEFFXXX")
	setString(req, "country_code", "DE")
	if got := stringField(req, "iban"); got != "DE89370400440532013000" {
		t.Fatalf("iban field = %q, want the set value", got)
	}
	if got := stringField(req, "country_code"); got != "DE" {
		t.Fatalf("country_code field = %q", got)
	}

	del := newMessage("DeletePaymentAccountRequest")
	setString(del, "payment_account_id", "acct-1")
	if got := stringField(del, "payment_account_id"); got != "acct-1" {
		t.Fatalf("payment_account_id field = %q", got)
	}
}

func TestQuoteAndGuardedTakeDescriptor(t *testing.T) {
	for _, name := range []string{
		"QuoteTakeOfferRequest", "QuoteTakeOfferReply", "GuardedTakeOfferRequest",
	} {
		if msg := newMessage(name); msg == nil {
			t.Fatalf("descriptor missing message %s", name)
		}
	}
	quote := newMessage("QuoteTakeOfferReply")
	setUint64(quote, "trade_amount_sats", 1_000_000)
	setUint64(quote, "security_deposit_sats", 150_000)
	setUint64(quote, "taker_fee_sats", 5_000)
	setUint64(quote, "mining_fee_sats", 12_000)
	setUint64(quote, "total_to_pay_sats", 1_167_000)
	setInt64(quote, "volume_as_long", 5_000_000)
	setBool(quote, "is_buy_offer", true)
	got := takeOfferQuoteFromMessage(quote)
	if got.TradeAmountSats != 1_000_000 || got.TotalToPaySats != 1_167_000 || got.VolumeAsLong != 5_000_000 {
		t.Fatalf("quote decode = %+v", got)
	}
	if FiatAmountFromVolumeAsLong(got.VolumeAsLong) != 500 {
		t.Fatalf("fiat amount = %v", FiatAmountFromVolumeAsLong(got.VolumeAsLong))
	}
	guarded := newMessage("GuardedTakeOfferRequest")
	setString(guarded, "payment_account_id", "acct-1")
	setUint64(guarded, "amount", 1_000_000)
	setUint64(guarded, "max_wallet_debit_sats", 1_200_000)
	setInt64(guarded, "min_volume_as_long", 4_900_000)
	if stringField(guarded, "payment_account_id") != "acct-1" {
		t.Fatal("guarded payment_account_id was not set")
	}
}
