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
