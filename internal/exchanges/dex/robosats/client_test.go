package robosats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetRobotSendsAuthToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/robot/" {
			t.Fatalf("path = %q, want /api/robot/", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nickname":"testbot"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewClient(Config{BaseURL: srv.URL, AuthToken: "robot-token"})
	robot, err := client.GetRobot(context.Background())
	if err != nil {
		t.Fatalf("GetRobot: %v", err)
	}
	wantPrefix := "Token " + AuthHeaderToken("robot-token") + " | Public "
	if !strings.HasPrefix(gotAuth, wantPrefix) {
		t.Fatalf("Authorization = %q, want prefix %q", gotAuth, wantPrefix)
	}
	if !strings.Contains(gotAuth, " | Private ") || !strings.Contains(gotAuth, " | Nostr ") {
		t.Fatalf("Authorization = %q, want Private and Nostr identity parts", gotAuth)
	}
	if robot["nickname"] != "testbot" {
		t.Fatalf("nickname = %v, want testbot", robot["nickname"])
	}
}

func TestGetRobotRequiresBaseURL(t *testing.T) {
	client := NewClient(Config{AuthToken: "robot-token"})
	if _, err := client.GetRobot(context.Background()); err == nil {
		t.Fatal("GetRobot error = nil, want missing base URL error")
	}
}

func TestGetRobotBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	client := NewClient(Config{BaseURL: srv.URL, AuthToken: "robot-token"})
	if _, err := client.GetRobot(context.Background()); err == nil {
		t.Fatal("GetRobot error = nil, want bad status error")
	}
}
