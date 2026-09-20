package lnd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type paymentRoundTripper func(*http.Request) (*http.Response, error)

func (f paymentRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func paymentResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func TestHoldPaymentTracksBeforeSendingAndReturnsInFlight(t *testing.T) {
	hash := strings.Repeat("a", 64)
	calls := 0
	c := &Client{host: "https://lnd.test", httpClient: &http.Client{Transport: paymentRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/v2/router/track/") {
				t.Fatal("did not track first")
			}
			return paymentResponse(404, `{"code":5,"message":"payment not found"}`), nil
		}
		if r.Method != "POST" || r.URL.Path != "/v2/router/send" {
			t.Fatal("wrong router endpoint")
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["fee_limit_sat"] != "25" || req["no_inflight_updates"] != false || req["payment_request"] != "invoice" {
			t.Fatalf("wrong router request: %v", req)
		}
		// Reader supplies a status but never EOF. Waiting for the complete HTTP body
		// would deadlock on a held invoice. The implementation must close the stream.
		reader, writer := io.Pipe()
		go func() {
			_, _ = io.WriteString(writer, `{"result":{"payment_hash":"`+hash+`","status":"IN_FLIGHT"}}`+"\n")
		}()
		return &http.Response{StatusCode: 200, Body: reader}, nil
	})}}
	status, err := c.HoldPayment(context.Background(), "invoice", hash, 25)
	if err != nil || status != "IN_FLIGHT" || calls != 2 {
		t.Fatalf("status=%s err=%v calls=%d", status, err, calls)
	}
}
func TestHoldPaymentDoesNotResubmitExistingOrAmbiguousPayments(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, state := range []string{"IN_FLIGHT", "SUCCEEDED", "FAILED", "unavailable", "wrong_hash"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			c := &Client{host: "https://lnd.test", httpClient: &http.Client{Transport: paymentRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" {
					t.Fatal("resubmitted payment")
				}
				if state == "unavailable" {
					return paymentResponse(503, `{"code":14,"message":"unavailable"}`), nil
				}
				h := hash
				if state == "wrong_hash" {
					h = strings.Repeat("b", 64)
				}
				return paymentResponse(200, `{"result":{"payment_hash":"`+h+`","status":"`+state+`"}}`), nil
			})}}
			got, err := c.HoldPayment(context.Background(), "invoice", hash, 10)
			if state == "unavailable" || state == "wrong_hash" {
				if err == nil {
					t.Fatal("ambiguous response accepted")
				}
			} else if err != nil || got != state {
				t.Fatalf("%s %v", got, err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestPayInvoiceUsesFeeLimitFixed(t *testing.T) {
	c := &Client{host: "https://lnd.test", httpClient: &http.Client{Transport: paymentRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/v1/channels/transactions" {
			t.Fatalf("path=%s %s", r.Method, r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if _, ok := req["fee_limit_sat"]; ok {
			t.Fatalf("legacy fee_limit_sat still present: %v", req)
		}
		limit, _ := req["fee_limit"].(map[string]any)
		if req["payment_request"] != "lnbc1invoice" || limit["fixed"] != "17" {
			t.Fatalf("wrong sendpayment request: %v", req)
		}
		return paymentResponse(200, `{"payment_error":"","payment_preimage":"aa"}`), nil
	})}}
	if _, err := c.PayInvoice(context.Background(), "lnbc1invoice", 17); err != nil {
		t.Fatal(err)
	}
}
