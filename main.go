package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"dexMmController/internal/api"
	"dexMmController/internal/bitcoin"
	"dexMmController/internal/db"
	"dexMmController/internal/exchanges/dex/bisq"
	"dexMmController/internal/exchanges/dex/hodlhodl"
	"dexMmController/internal/exchanges/dex/robosats"
	"dexMmController/internal/healthcheck"
	"dexMmController/internal/hummingbot"
	"dexMmController/internal/openclaw"
	"dexMmController/internal/premiumoffers"
	lnd "dexMmController/internal/wallethub"
)

func main() {
	// ── Config ────────────────────────────────────────────────────────────────
	databaseURL := envOr("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/dexarb?sslmode=disable")
	openclawURL := envOr("OPENCLAW_URL", "")
	hbURL := envOr("HUMMINGBOT_URL", "http://localhost:8000")
	hbUser := envOr("HUMMINGBOT_USERNAME", "admin")
	hbPass := mustEnv("HUMMINGBOT_PASSWORD")
	hhAPIKey := envOr("HODLHODL_API_KEY", "")
	hhBaseURL := envOr("HODLHODL_URL", "")
	bisqGRPCHost := envOr("BISQ_GRPC_HOST", "localhost:9998")
	bisqAPIPassword := envOr("BISQ_API_PASSWORD", "")
	robosatsURL := envOr("ROBOSATS_URL", "")
	robosatsAuthToken := envOr("ROBOSATS_AUTH_TOKEN", "")
	offerPremiumWSSURL := envOr("OFFER_PREMIUM_WSS_URL", "")
	apiAddr := envOr("API_ADDR", ":5001")
	lndHost := envOr("LND_HOST", "")
	lndTLSCertPath := envOr("LND_TLS_CERT_PATH", "")
	lndMacaroonPath := envOr("LND_MACAROON_PATH", "")
	lndWalletPassword := os.Getenv("LND_WALLET_PASSWORD")

	// Graceful shutdown context for long-running stream.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Database ──────────────────────────────────────────────────────────────
	database, err := db.InitDB(databaseURL)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close(database)

	repo := db.NewTransactionRepository(database)

	primaryAddress, err := ensurePrimaryDepositAddress(
		ctx,
		repo,
		lndHost,
		lndTLSCertPath,
		lndMacaroonPath,
		lndWalletPassword,
	)
	if err != nil {
		log.Fatalf("failed to initialize primary wallet address: %v", err)
	}
	if primaryAddress != "" {
		log.Printf("primary CEX deposit address ready: %s", primaryAddress)
	}

	// ── OpenClaw ──────────────────────────────────────────────────────────────
	claw := openclaw.NewClient(openclawURL)
	if openclawURL == "" {
		log.Println("OPENCLAW_URL is not set; continuing without openclaw connection")
	} else if err := claw.Connect(context.Background()); err != nil {
		log.Printf("warning: failed to connect to openclaw at %s: %v", openclawURL, err)
	}
	defer func() { _ = claw.Close() }()

	// ── Hummingbot ────────────────────────────────────────────────────────────
	hb := hummingbot.NewClient(hbURL, hbUser, hbPass)

	// ── Bitcoin ───────────────────────────────────────────────────────────────
	btc := bitcoin.NewNetworkDataFetcher()

	// ── HodlHodl ─────────────────────────────────────────────────────────────
	hh := hodlhodl.NewClient(hhBaseURL, hhAPIKey)

	// ── Bisq / RoboSats ──────────────────────────────────────────────────────
	bq := bisq.NewClient(bisqGRPCHost, bisqAPIPassword)
	rs := robosats.NewClient(robosats.Config{
		BaseURL:   robosatsURL,
		AuthToken: robosatsAuthToken,
	})

	// ── HealthChecker ─────────────────────────────────────────────────────────
	hc := healthcheck.NewHealthChecker(repo, claw, hb, btc, hh)
	apiServer := api.NewServer(apiAddr, repo)

	// ── Run healthcheck once ──────────────────────────────────────────────────
	log.Println("running healthcheck")
	if err := hc.Run(context.Background()); err != nil {
		log.Fatalf("healthcheck failed: %v", err)
	}
	log.Println("healthcheck complete")

	go func() {
		if err := apiServer.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("api server failed: %v", err)
			stop()
		}
	}()

	// ── Offer Premium WSS Stream ──────────────────────────────────────────────
	if offerPremiumWSSURL == "" {
		log.Println("OFFER_PREMIUM_WSS_URL is not set; API server will stay up without premium stream")
		<-ctx.Done()
		return
	}

	watcher := premiumoffers.NewWatcher(repo, bq, hh, rs, offerPremiumWSSURL)
	if err := watcher.Run(ctx); err != nil && ctx.Err() == nil {
		log.Printf("premium-offer watcher failed: %v", err)
	}
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

func ensurePrimaryDepositAddress(
	ctx context.Context,
	repo *db.TransactionRepository,
	lndHost, tlsCertPath, macaroonPath, password string,
) (string, error) {
	if existing, err := repo.GetPrimaryWalletAddress(); err == nil && existing.Address != "" {
		log.Printf("reusing stored primary wallet address %s", existing.Address)
		return existing.Address, nil
	}

	if lndHost == "" || tlsCertPath == "" || macaroonPath == "" || password == "" {
		log.Println("LND startup configuration incomplete; skipping primary wallet bootstrap")
		return "", nil
	}

	log.Println("initializing LND wallet hub")
	client, err := lnd.New(ctx, lndHost, tlsCertPath, macaroonPath, password)
	if err != nil {
		return "", err
	}

	address, err := client.NewAddress(ctx)
	if err != nil {
		return "", err
	}

	record := &db.WalletAddress{
		Label:   "Primary",
		Address: address,
	}
	if err := repo.SaveWalletAddress(record); err != nil {
		return "", err
	}

	log.Printf("generated new primary wallet address %s", address)
	return address, nil
}
