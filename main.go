package main

import (
	"context"
	"log"
	"os"

	"dexMmController/internal/bitcoin"
	"dexMmController/internal/db"
	"dexMmController/internal/healthcheck"
	"dexMmController/internal/hodlhodl"
	"dexMmController/internal/hummingbot"
	"dexMmController/internal/openclaw"
)

func main() {
	// ── Config ────────────────────────────────────────────────────────────────
	dbPath := envOr("DB_PATH", "./sqlite.db")
	openclawURL := mustEnv("OPENCLAW_URL")
	hbURL := envOr("HUMMINGBOT_URL", "http://localhost:8000")
	hbUser := envOr("HUMMINGBOT_USERNAME", "admin")
	hbPass := mustEnv("HUMMINGBOT_PASSWORD")

	// ── Database ──────────────────────────────────────────────────────────────
	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close(database)

	repo := db.NewTransactionRepository(database)

	// ── OpenClaw ──────────────────────────────────────────────────────────────
	claw := openclaw.NewClient(openclawURL)
	if err := claw.Connect(context.Background()); err != nil {
		log.Fatalf("failed to connect to openclaw at %s: %v", openclawURL, err)
	}
	defer func() { _ = claw.Close() }()

	// ── Hummingbot ────────────────────────────────────────────────────────────
	hb := hummingbot.NewClient(hbURL, hbUser, hbPass)

	// ── Bitcoin ───────────────────────────────────────────────────────────────
	btc := bitcoin.NewNetworkDataFetcher()

	// ── HodlHodl ─────────────────────────────────────────────────────────────
	hhAPIKey := envOr("HODLHODL_API_KEY", "")
	hhBaseURL := envOr("HODLHODL_URL", "")
	hh := hodlhodl.NewClient(hhBaseURL, hhAPIKey)

	// ── HealthChecker ─────────────────────────────────────────────────────────
	hc := healthcheck.NewHealthChecker(repo, claw, hb, btc, hh)

	// ── Run once ──────────────────────────────────────────────────────────────
	log.Println("running healthcheck")
	if err := hc.Run(context.Background()); err != nil {
		log.Fatalf("healthcheck failed: %v", err)
	}
	log.Println("healthcheck complete")
}

// envOr returns the value of the environment variable key, or fallback if unset.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// mustEnv returns the value of the environment variable key or exits if unset.
func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}
