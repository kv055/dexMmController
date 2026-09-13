package mostro

import (
	"encoding/json"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"testing"
	"time"
)

func TestChatSharedKeysAndAuthenticatedInnerSender(t *testing.T) {
	alice, bob := testKeys(t)
	a, _ := nostr.GetPublicKey(alice)
	b, _ := nostr.GetPublicKey(bob)
	ka, err := deriveChatKeys(alice, b)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := deriveChatKeys(bob, a)
	if err != nil {
		t.Fatal(err)
	}
	if ka != kb || ka.conversation == ka.signing {
		t.Fatal("chat key derivation disagrees or lacks domain separation")
	}
	now := time.Now()
	outer, err := buildChatEvent("bank details", alice, ka, now)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := decodeChatEvent(outer, kb, b, a, now)
	if err != nil || inner.Content != "bank details" || inner.PubKey != a {
		t.Fatalf("round trip: %v", err)
	}
	for _, attack := range []string{"wrong_inner_signer", "forged_inner_signature", "duplicate_recipient", "future_timestamp", "mismatched_timestamp"} {
		t.Run(attack, func(t *testing.T) {
			e := outer
			in := inner
			switch attack {
			case "wrong_inner_signer":
				_ = in.Sign(nostr.GeneratePrivateKey())
			case "forged_inner_signature":
				in.Content = "forged"
			case "duplicate_recipient":
				e.Tags = nostr.Tags{{"p", ka.address}, {"p", ka.address}}
			case "future_timestamp":
				in.CreatedAt = nostr.Timestamp(now.Add(time.Hour).Unix())
				_ = in.Sign(alice)
				e.CreatedAt = in.CreatedAt
			case "mismatched_timestamp":
				in.CreatedAt -= 120
				_ = in.Sign(alice)
			}
			key, _ := nip44.GenerateConversationKey(ka.address, ka.conversation)
			raw, _ := json.Marshal(in)
			e.Content, _ = nip44.Encrypt(string(raw), key)
			_ = e.Sign(ka.signing)
			if _, err := decodeChatEvent(e, kb, b, a, now); err == nil {
				t.Fatal("forged chat accepted")
			}
		})
	}
}
