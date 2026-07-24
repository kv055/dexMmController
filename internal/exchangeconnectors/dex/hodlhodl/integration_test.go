//go:build integration

package hodlhodl

import (
	"bufio"
	"commons/exchangecredentials"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// loadEnv reads key=value pairs from the project root .env file into a map.
func loadEnv(t *testing.T) map[string]string {
	t.Helper()
	// Walk up from this test file to the project root.
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	f, err := os.Open(filepath.Join(root, ".env"))
	if err != nil {
		t.Skipf("no .env file found, skipping integration test: %v", err)
	}
	defer f.Close()

	env := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return env
}

func integrationClient(t *testing.T) *Client {
	t.Helper()
	env := loadEnv(t)
	databaseURL := env["DATABASE_URL"]
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set in .env, skipping")
	}
	if appKey := env["APP_ENCRYPTION_KEY"]; appKey != "" {
		t.Setenv("APP_ENCRYPTION_KEY", appKey)
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	credentials, err := exchangecredentials.NewStore(database).Resolve("HodlHodl")
	if err != nil {
		t.Skipf("no active HodlHodl database credentials, skipping: %v", err)
	}
	baseURL := env["HODLHODL_URL"] // empty → default
	return NewClient(baseURL, credentials.APIKey).WithSignatureKey(credentials.APISecret)
}

func TestIntegration_GetPublicOffer(t *testing.T) {
	c := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Fetch the public offers list (no auth needed, but tests connectivity).
	offers, err := c.GetMyOffers(ctx)
	if err != nil {
		t.Fatalf("GetMyOffers: %v", err)
	}
	t.Logf("got %d offers from HodlHodl", len(offers))
	for i, o := range offers {
		t.Logf("  [%d] id=%s status=%s side=%s title=%q", i, o.ID, o.Status, o.Side, o.Title)
	}
}

func TestIntegration_GetMyContracts(t *testing.T) {
	c := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	contracts, err := c.GetMyContracts(ctx, "")
	if err != nil {
		t.Fatalf("GetMyContracts: %v", err)
	}
	t.Logf("got %d contracts from HodlHodl", len(contracts))
	for i, ct := range contracts {
		t.Logf("  [%d] id=%s status=%s role=%s", i, ct.ID, ct.Status, ct.YourRole)
	}
}
