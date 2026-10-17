package robosats

import "testing"

func TestOrderPathEscapesOrderID(t *testing.T) {
	c := &Client{orderActionPath: "/api/order/?order_id=%s"}
	cases := map[string]string{
		"12345":             "/api/order/?order_id=12345",
		"1/../admin":        "/api/order/?order_id=1%2F..%2Fadmin",
		"1?admin=1":         "/api/order/?order_id=1%3Fadmin%3D1",
		"1&admin=1":         "/api/order/?order_id=1%26admin%3D1",
		"1#frag":            "/api/order/?order_id=1%23frag",
		"../../api/limits/": "/api/order/?order_id=..%2F..%2Fapi%2Flimits%2F",
	}
	for in, want := range cases {
		if got := c.orderPath(in); got != want {
			t.Errorf("orderPath(%q) = %q, want %q", in, got, want)
		}
	}
}
