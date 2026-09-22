package bisqexec

import (
	"commons/bisqroute"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouteQuoterAuthenticatedAndFailClosed(t *testing.T) {
	for _, status := range []int{200, 401, 409, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/internal/bisq/requote" || r.Header.Get("X-Internal-Secret") != "test-secret" {
					t.Error("bad route request")
				}
				var q bisqroute.Request
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.TradeAmountSats != 1234567 {
					t.Errorf("bad exact amount: %+v %v", q, err)
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(testRoute())
			}))
			defer srv.Close()
			q := HTTPRouteQuoter{BaseURL: srv.URL, Secret: "test-secret", Client: srv.Client()}
			_, err := q.RequoteBisq(context.Background(), bisqroute.Request{TradeAmountSats: 1234567})
			if (err != nil) != (status != 200) {
				t.Fatal(err)
			}
		})
	}
}
