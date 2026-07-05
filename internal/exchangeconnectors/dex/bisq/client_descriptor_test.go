package bisq

import "testing"

// TestFundingAddressDescriptor builds the proto descriptor (protodesc.NewFile panics on
// a malformed set) and verifies the new Wallets/GetFundingAddresses messages resolve.
func TestFundingAddressDescriptor(t *testing.T) {
	for _, name := range []string{"GetFundingAddressesRequest", "GetFundingAddressesReply", "AddressBalanceInfo"} {
		if msg := newMessage(name); msg == nil {
			t.Fatalf("descriptor missing message %s", name)
		}
	}
}

// TestPaymentAccountMutationDescriptor verifies the patched CreateSepaPaymentAccount /
// DeletePaymentAccount messages resolve and their request fields are settable.
func TestPaymentAccountMutationDescriptor(t *testing.T) {
	for _, name := range []string{
		"CreateSepaPaymentAccountRequest", "CreateSepaPaymentAccountReply",
		"DeletePaymentAccountRequest", "DeletePaymentAccountReply",
	} {
		if msg := newMessage(name); msg == nil {
			t.Fatalf("descriptor missing message %s", name)
		}
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
