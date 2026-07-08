package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dexMmController/internal/accounthealth"
	"dexMmController/internal/api"
	"dexMmController/internal/bitcoin"
	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	"dexMmController/internal/dexaccount"
	"dexMmController/internal/dexnotify"
	"dexMmController/internal/dexrelay"
	"dexMmController/internal/dexrelease"
	"dexMmController/internal/exchangeconnectors/cex"
	"dexMmController/internal/exchangeconnectors/cex/binance"
	"dexMmController/internal/exchangeconnectors/cex/coinbase"
	"dexMmController/internal/exchangeconnectors/cex/kraken"
	"dexMmController/internal/exchangeconnectors/dex/bisq"
	"dexMmController/internal/exchangeconnectors/dex/bisq2"
	"dexMmController/internal/exchangeconnectors/dex/hodlhodl"
	"dexMmController/internal/exchangeconnectors/dex/robosats"
	"dexMmController/internal/healthcheck"
	"dexMmController/internal/hedgestream"
	"dexMmController/internal/lndhealth"
	"dexMmController/internal/obs"
	"dexMmController/internal/openclaw"
	"dexMmController/internal/premiumoffers"
	"dexMmController/internal/tradeflow"
	lnd "dexMmController/internal/wallethub"

	"commons/logging"
	"commons/metrics"
	"commons/telegram"
	"commons/tracing"
)

// paymentMethodSnapshot converts a payment_methods DB row into the pre-trade
// checker's view of it.
func paymentMethodSnapshot(row *db.PaymentMethod) *cexaccount.PaymentMethodSnapshot {
	return &cexaccount.PaymentMethodSnapshot{
		Name:            row.Name,
		Category:        row.Category,
		BankAccountID:   row.BankAccountID,
		AccountID:       row.AccountID,
		WalletAddressID: row.WalletAddressID,
		IsActive:        row.IsActive,
		IsLinked:        row.IsLinked,
		Priority:        row.Priority,
	}
}

func main() {
	// ── Config ────────────────────────────────────────────────────────────────
	databaseURL := resolveDatabaseURL()
	openclawURL := envOr("OPENCLAW_URL", "")
	hhAPIKey := envOr("HODLHODL_API_KEY", "")
	hhBaseURL := envOr("HODLHODL_URL", "")
	hhSignatureKey := envOr("HODLHODL_SIGNATURE_KEY", "")
	hhEscrowSeed := envOr("HODLHODL_ESCROW_SEED", "")
	hhEscrowPaymentPassword := envOr("HODLHODL_ESCROW_PAYMENT_PASSWORD", "")
	dexReleaseToken := envOr("DEX_RELEASE_WEBHOOK_TOKEN", "")
	binanceBaseURL := envOr("BINANCE_API_BASE_URL", "")
	krakenBaseURL := envOr("KRAKEN_API_BASE_URL", "")
	coinbaseBaseURL := envOr("COINBASE_API_BASE_URL", "")
	coinbaseExchangeBaseURL := envOr("COINBASE_EXCHANGE_API_BASE_URL", "")
	bisqGRPCHost := envOr("BISQ_GRPC_HOST", "localhost:9998")
	bisqAPIPassword := envOr("BISQ_API_PASSWORD", "")
	robosatsURL := envOr("ROBOSATS_URL", "")
	robosatsAuthToken := envOr("ROBOSATS_AUTH_TOKEN", "")
	bisq2RESTHost := envOr("BISQ2_REST_HOST", "")
	bisq2APIPassword := envOr("BISQ2_API_PASSWORD", "")
	telegramCfg := telegram.LoadConfig("DEXMM")
	torProxyURL := envOr("TOR_PROXY_URL", "")
	offerPremiumWSSURL := envOr("OFFER_PREMIUM_WSS_URL", "")
	// Internal secret for the secret-gated /internal/ws/live_offers route on
	// unified-api. Empty when OFFER_PREMIUM_WSS_URL points at the browser
	// session-cookie path or when running without INTERNAL_API_SECRET
	// (unified-api then fails the dial closed unless ALLOW_INSECURE_INTERNAL).
	offerPremiumWSSSecret := os.Getenv("INTERNAL_API_SECRET")
	apiAddr := envOr("API_ADDR", ":5001")
	lndHost := envOr("LND_HOST", "")
	lndTLSCertPath := envOr("LND_TLS_CERT_PATH", "")
	lndMacaroonPath := envOr("LND_MACAROON_PATH", "")
	lndSeedMnemonicFile := envOr("LND_SEED_MNEMONIC_FILE", "")
	lndAEZEEDPassphraseFile := envOr("LND_AEZEED_PASSPHRASE_FILE", "")
	lndExtendedMasterKeyFile := envOr("LND_EXTENDED_MASTER_KEY_FILE", "")
	lndExtendedMasterKeyBirthdayTimestampFile := envOr("LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP_FILE", "")
	lndRecoveryWindow := envOr("LND_RECOVERY_WINDOW", "")
	lndExtendedMasterKeyBirthdayTimestamp := envOr("LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP", "")
	accountHealthInterval := parseAccountHealthInterval(envOr("ACCOUNT_HEALTH_INTERVAL", ""))
	healthcheckInterval := parseAccountHealthInterval(envOr("HEALTHCHECK_INTERVAL", "30s"))
	lndBackupInterval := parseLNDBackupInterval(envOr("LND_BACKUP_INTERVAL", ""), accountHealthInterval)

	// Graceful shutdown context for long-running stream.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Observability ─────────────────────────────────────────────────────────
	// Structured JSON logs on stdout (Alloy → Loki). slog.SetDefault also routes
	// the stdlib log package through the same handler, so existing log.Printf
	// call sites come out as JSON too.
	logging.Init("mmcontroller")
	if shutdownTracing, err := tracing.Init(ctx, "mmcontroller"); err != nil {
		log.Printf("warning: tracing init: %v", err)
	} else {
		defer func() { _ = shutdownTracing(context.Background()) }()
	}
	metricsReg := metrics.NewServiceRegistry("mmcontroller")
	obs.Register(metricsReg.Registry)
	telegram.RegisterMetrics(metricsReg.Registry)

	// ── Database ──────────────────────────────────────────────────────────────
	database, err := db.InitDB(databaseURL)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close(database)

	repo := db.NewTransactionRepository(database)
	obs.RegisterTradeStats(metricsReg.Registry, repo)

	lndAccount, err := repo.EnsureAccount(ctx, db.LNDAccountProvider)
	if err != nil {
		log.Fatalf("failed to ensure LND account row: %v", err)
	}

	if err := recordLNDMasterKeyHash(repo, lndExtendedMasterKeyFile); err != nil {
		log.Fatalf("failed to record LND master key hash: %v", err)
	}

	fileInitCfg, err := loadLNDInitConfig(
		lndSeedMnemonicFile,
		lndAEZEEDPassphraseFile,
		lndExtendedMasterKeyFile,
		lndExtendedMasterKeyBirthdayTimestampFile,
		lndRecoveryWindow,
		lndExtendedMasterKeyBirthdayTimestamp,
	)
	if err != nil {
		log.Fatalf("invalid LND wallet secret configuration: %v", err)
	}
	if err := importLNDSeedFromFile(ctx, repo, int64(lndAccount.ID), fileInitCfg); err != nil {
		log.Fatalf("failed to import LND seed into database: %v", err)
	}

	walletPassword, _, err := repo.LNDWalletCredentials(ctx)
	if err != nil {
		log.Fatalf("failed to load LND wallet credentials from database: %v", err)
	}
	// LND wallet bootstrap is defined below (lndInit) so it can run at startup when a
	// password already exists, or on demand from the UI via POST /api/lnd/wallet/init.

	// ── OpenClaw ──────────────────────────────────────────────────────────────
	claw := openclaw.NewClient(openclawURL)
	if openclawURL == "" {
		log.Println("OPENCLAW_URL is not set; continuing without openclaw connection")
	} else if err := claw.Connect(context.Background()); err != nil {
		log.Printf("warning: failed to connect to openclaw at %s: %v", openclawURL, err)
	}
	defer func() { _ = claw.Close() }()

	// ── Bitcoin ───────────────────────────────────────────────────────────────
	btc := bitcoin.NewNetworkDataFetcher()

	// ── HodlHodl ─────────────────────────────────────────────────────────────
	hh := hodlhodl.NewClient(hhBaseURL, hhAPIKey).WithSignatureKey(hhSignatureKey)

	// ── Bisq / RoboSats ──────────────────────────────────────────────────────
	bq := bisq.NewClient(bisqGRPCHost, bisqAPIPassword)
	dbRobosatsAuthToken, err := robosatsAuthTokenFromDB(ctx, repo, robosatsAuthToken)
	if err != nil {
		log.Printf("warning: failed to load Robosats token from database; using environment fallback: %v", err)
	}
	rs := robosats.NewClient(robosats.Config{
		BaseURL:   robosatsURL,
		AuthToken: dbRobosatsAuthToken,
		ProxyURL:  torProxyURL,
	})
	b2 := bisq2.NewClient(bisq2RESTHost, bisq2APIPassword, nil)

	// ── Telegram (shared bot: trade-chat relay + anomaly alerts, DEXMM webhook) ─
	// Config is seeded from env (LoadConfig("DEXMM")) for backwards compatibility,
	// then overridden by the shared telegram_settings DB row when present. The
	// store is the hot-reloadable runtime holder; the poller below keeps it in
	// sync with the DB row so the UI can change credentials without a restart.
	telegramSettingsRepo := db.NewTelegramSettingsRepository(database)
	seedCfg := telegramCfg
	if row, err := telegramSettingsRepo.Get(ctx); err == nil && strings.TrimSpace(row.BotToken) != "" {
		seedCfg = telegram.Config{
			Token:      row.BotToken,
			ChatID:     row.ChatID,
			WebhookURL: row.DexmmWebhookURL,
		}
	} else if err != nil && err != db.ErrTelegramSettingsNotFound {
		log.Printf("warning: load telegram settings: %v", err)
	}
	telegramStore := telegram.NewStore(seedCfg, &http.Client{Timeout: 10 * time.Second})
	tgClient := telegramStore.Client()
	chatRelay := telegram.NewChatRelay(tgClient, nil)
	dexPaymentResolver := dexaccount.NewPaymentResolver(repo, bq, rs, b2, hh)
	telegramSvc := dexnotify.NewService(chatRelay, dexPaymentResolver)
	anomalyNotifier := dexnotify.NewAnomalyNotifier(tgClient)
	if telegramStore.Enabled() && strings.TrimSpace(telegramStore.Get().WebhookURL) != "" {
		if err := tgClient.SetWebhook(ctx, telegramStore.Get().WebhookURL); err != nil {
			log.Printf("warning: telegram setWebhook: %v", err)
		}
	}
	go chatRelay.Run(ctx)
	go startTelegramSettingsPoller(ctx, telegramSettingsRepo, telegramStore, chatRelay, anomalyNotifier)

	dexTaker := dexaccount.NewTaker(repo, bq, hh, rs)
	// dexReleaser performs Stage-6 BTC release; dexReleaseWorker drains the webhook-fed
	// queue and runs the (idempotent) releases in the background.
	dexReleaser := dexaccount.NewReleaser(repo, bq, hh, rs, dexaccount.HodlHodlEscrowConfig{
		EncryptedSeed:   hhEscrowSeed,
		PaymentPassword: hhEscrowPaymentPassword,
	})
	dexReleaseWorker := dexrelease.NewWorker(repo, dexReleaser)
	exchangeAccountHealth := accounthealth.NewChecker(repo, cexaccount.Config{
		HodlHodlBaseURL: hhBaseURL,
		BinanceBaseURL:  binanceBaseURL,
		KrakenBaseURL:   krakenBaseURL,
		CoinbaseBaseURL: coinbaseBaseURL,
		RobosatsBaseURL: robosatsURL,
		TorProxyURL:     torProxyURL,
	})
	cexAccountService := cexaccount.NewService(repo, cexaccount.Config{
		HodlHodlBaseURL:         hhBaseURL,
		BinanceBaseURL:          binanceBaseURL,
		KrakenBaseURL:           krakenBaseURL,
		CoinbaseBaseURL:         coinbaseBaseURL,
		CoinbaseExchangeBaseURL: coinbaseExchangeBaseURL,
		RobosatsBaseURL:         robosatsURL,
		TorProxyURL:             torProxyURL,
	})
	bidChecker := cexaccount.NewBidChecker(
		cexaccount.BalanceFetcherFunc(func(ctx context.Context, provider, currency string) (float64, error) {
			b, err := cexAccountService.Balances(ctx, provider)
			if err != nil {
				return 0, err
			}
			for _, e := range b.Fiat {
				if strings.EqualFold(e.Asset, currency) {
					return e.Available, nil
				}
			}
			return 0, nil
		}),
		cexaccount.WhitelistFetcherFunc(func(ctx context.Context, provider string) ([]string, error) {
			addrs, err := cexAccountService.WithdrawalAddresses(ctx, provider)
			if err != nil {
				return nil, err
			}
			var out []string
			for _, a := range addrs {
				if a.Whitelisted {
					out = append(out, a.Address)
				}
			}
			return out, nil
		}),
		cexaccount.PermissionCheckerFunc(func(ctx context.Context, provider string) (map[string]string, error) {
			keys, err := repo.GetActiveAPIKeysForProvider(ctx, provider)
			if err != nil {
				return nil, err
			}
			// Merge permissions from all active keys: "granted" beats everything else.
			merged := map[string]string{}
			for _, key := range keys {
				if len(key.Permissions) == 0 {
					continue
				}
				var perms map[string]string
				if err := json.Unmarshal(key.Permissions, &perms); err != nil {
					continue
				}
				for perm, status := range perms {
					if merged[perm] == "" || status == "granted" {
						merged[perm] = status
					}
				}
			}
			return merged, nil
		}),
		cexaccount.PaymentSetupFetcherFuncs{
			MethodFn: func(ctx context.Context, name string) (*cexaccount.PaymentMethodSnapshot, error) {
				row, err := repo.GetPaymentMethodByName(ctx, name)
				if err != nil || row == nil {
					return nil, err
				}
				return paymentMethodSnapshot(row), nil
			},
			PickFn: func(ctx context.Context, names []string) (*cexaccount.PaymentMethodSnapshot, error) {
				row, err := repo.PickPaymentMethod(ctx, names)
				if err != nil || row == nil {
					return nil, err
				}
				return paymentMethodSnapshot(row), nil
			},
			FiatFn: func(ctx context.Context, id int64) (*cexaccount.FiatAccountSnapshot, error) {
				acct, err := repo.GetAccountByID(ctx, id)
				if err != nil || acct == nil {
					return nil, err
				}
				return &cexaccount.FiatAccountSnapshot{
					Provider: acct.Provider,
					Email:    acct.Email,
					Phone:    acct.Phone,
				}, nil
			},
			BankFn: func(ctx context.Context, id int64) (*cexaccount.BankAccountSnapshot, error) {
				row, err := repo.GetBankAccountByID(ctx, id)
				if err != nil || row == nil {
					return nil, err
				}
				return &cexaccount.BankAccountSnapshot{
					Scheme:   row.Scheme,
					Details:  cexaccount.ParseBankAccountDetails(row.Details),
					IsActive: row.IsActive,
				}, nil
			},
			WalletFn: func(ctx context.Context, id int64) (*cexaccount.WalletAddressSnapshot, error) {
				addr, err := repo.GetWalletAddressByID(ctx, id)
				if err != nil || addr == nil {
					return nil, err
				}
				return &cexaccount.WalletAddressSnapshot{
					Label:   addr.Label,
					Address: addr.Address,
				}, nil
			},
		},
	)
	bidChecker.SetCommunicationGateway(telegramStore)
	cexAccountService.WithBidChecker(bidChecker)
	dexRelayWorker := dexrelay.NewWorker(repo, cexAccountService, btc, dexTaker, dexPaymentResolver)
	lndHealthChecker := lndhealth.NewChecker(repo, nil, lndhealth.Config{
		ExtendedMasterKeyFile: lndExtendedMasterKeyFile,
	})
	// hedgeStreamManager multiplexes each futures venue's live position feed to any
	// number of WebSocket subscribers. Sources reuse the futures clients' env-var
	// credentials (same as OpenHedge); each is started lazily on first subscriber.
	hedgeStreamManager := hedgestream.NewManager(map[string]hedgestream.SourceFunc{
		"kraken_futures": func(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
			return kraken.NewFuturesClient().StreamPositions(ctx)
		},
		"binance_futures": func(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
			return binance.NewFuturesClient().StreamPositions(ctx)
		},
		"coinbase_futures": func(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
			return coinbase.NewFuturesClient().StreamPositions(ctx)
		},
	})
	tradeFlow := tradeflow.New(cexAccountService, repo).
		WithDirectDestinationResolver(dexaccount.NewDirectResolver(bq))
	hc := healthcheck.NewHealthChecker(repo, claw, btc, hh, tradeFlow).
		WithRecoveryGracePeriod(parseAccountHealthInterval(envOr("TRADEFLOW_RECOVERY_GRACE", "3m"))).
		WithPaymentResolver(dexPaymentResolver).
		WithTelegramRelay(telegramSvc, anomalyNotifier).
		WithBisq(bq)
	apiServer := api.NewServer(apiAddr, repo).
		WithMetrics(metricsReg).
		WithAccountHealth(exchangeAccountHealth).
		WithCexAccount(cexAccountService).
		WithTradeFlow(tradeFlow).
		WithHedgeStream(hedgeStreamManager).
		WithLNDHealth(lndHealthChecker).
		WithDexRelease(dexReleaseWorker, dexReleaseToken).
		WithBisqPayments(bq).
		WithTelegramRelay(chatRelay).
		WithTelegramSettings(telegramSettingsRepo, telegramStore, anomalyNotifier)

	// lndInit creates (or restores) the LND wallet using the password stored in the
	// database, persists the generated seed + primary address + channel backup, and
	// injects the live client into the running API server and health checker. It runs
	// once at startup when a password already exists, and on demand from the UI via
	// POST /api/lnd/wallet/init.
	var lndBackupOnce sync.Once
	lndInit := func(reqCtx context.Context) (api.LNDInitResult, error) {
		password, aezeed, err := repo.LNDWalletCredentials(reqCtx)
		if err != nil {
			return api.LNDInitResult{}, err
		}
		if strings.TrimSpace(password) == "" {
			return api.LNDInitResult{}, api.ErrLNDWalletPasswordMissing
		}
		cfg := mergeLNDInitConfig(fileInitCfg, aezeed)
		cfg.ChannelBackupRestore = loadLNDChannelBackupRestore(reqCtx, repo, int64(lndAccount.ID))

		primaryAddress, lndClient, err := ensurePrimaryDepositAddress(
			reqCtx, repo, lndHost, lndTLSCertPath, lndMacaroonPath, password, cfg,
		)
		if err != nil {
			return api.LNDInitResult{}, err
		}
		if lndClient == nil {
			return api.LNDInitResult{}, fmt.Errorf("LND wallet client was not created; check LND_HOST/LND_TLS_CERT_PATH/LND_MACAROON_PATH")
		}

		if mnemonic := lndClient.GeneratedCipherSeedMnemonic(); mnemonic != "" {
			if err := repo.EnsureLNDSeed(reqCtx, int64(lndAccount.ID), mnemonic); err != nil {
				log.Printf("warning: failed to persist generated LND aezeed: %v", err)
			} else {
				log.Println("LND aezeed generated and stored in database")
			}
		}
		if err := persistLNDChannelBackup(reqCtx, repo, lndClient, int64(lndAccount.ID)); err != nil {
			log.Printf("warning: LND channel backup failed: %v", err)
		}

		apiServer.WithLNDWallet(lndClient)
		lndHealthChecker.SetClient(lndClient)
		obs.SetLNDStatsSource(lndStatsFetcher(lndClient))
		bidChecker.SetWallet(cexaccount.WalletSourceFunc(func(ctx context.Context) ([]string, error) {
			props, err := lndClient.ListWalletAddresses(ctx)
			if err != nil {
				return nil, err
			}
			addrs := make([]string, len(props))
			for i, p := range props {
				addrs[i] = p.Address
			}
			return addrs, nil
		}))
		cexAccountService.WithLND(lndClient)
		dexRelayWorker.SetLND(dexrelay.LNDAdapter{Client: lndClient})
		// Periodic backup must outlive the request, so use the long-lived app context.
		lndBackupOnce.Do(func() {
			go runPeriodicLNDChannelBackup(ctx, lndBackupInterval, repo, lndClient, int64(lndAccount.ID))
		})

		if primaryAddress != "" {
			log.Printf("primary CEX deposit address ready: %s", primaryAddress)
		}
		return api.LNDInitResult{PrimaryAddress: primaryAddress}, nil
	}
	apiServer = apiServer.WithLNDManagement(api.LNDManagement{
		Init: lndInit,
		StateProbe: func(probeCtx context.Context) (string, error) {
			return lnd.ProbeState(probeCtx, lndHost, lndTLSCertPath)
		},
	})

	if strings.TrimSpace(walletPassword) != "" {
		if _, err := lndInit(ctx); err != nil {
			log.Printf("warning: failed to initialize LND wallet at startup: %v", err)
		}
	} else {
		log.Println("LND wallet password not set; skipping wallet bootstrap — create it from the UI")
	}

	// ── Periodic healthcheck + dexrelay ─────────────────────────────────────────
	// TEMPORARILY DISABLED: the background workers (transaction recovery + CEX→DEX
	// relay) take automatic, money-moving corrective actions. They are wired up but
	// not started while we validate the tradeflow/recovery logic. Re-enable by
	// uncommenting the two goroutines below.
	// go hc.RunPeriodic(ctx, healthcheckInterval)
	// go dexRelayWorker.RunPeriodic(ctx, healthcheckInterval)
	_ = hc
	_ = dexRelayWorker
	_ = healthcheckInterval

	lndHealthChecker.RunAndLog(ctx)
	go lndHealthChecker.RunPeriodic(ctx, accountHealthInterval)
	go exchangeAccountHealth.RunPeriodic(ctx, accountHealthInterval)
	// The Stage-6 release worker only acts on webhook-delivered jobs (no autonomous
	// money-moving poll), so it runs unconditionally — unlike the team-gated dexrelay loop.
	go dexReleaseWorker.Run(ctx)

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

	watcher := premiumoffers.NewWatcher(repo, bq, hh, rs, offerPremiumWSSURL, offerPremiumWSSSecret)
	if err := watcher.Run(ctx); err != nil && ctx.Err() == nil {
		log.Printf("premium-offer watcher failed: %v", err)
	}
}

// lndStatsFetcher adapts the live wallet client to the obs LND collector. Wired
// here (not in obs) because obs must not import wallethub — wallethub pulls in
// the CEX connectors, which already record into obs.
func lndStatsFetcher(client *lnd.Client) obs.LNDStatsFunc {
	parseSat := func(raw string) float64 {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0
		}
		return v
	}
	return func(ctx context.Context) obs.LNDStats {
		var s obs.LNDStats
		if err := client.HealthCheck(ctx); err != nil {
			return s
		}
		s.Up = true
		if channels, err := client.ListChannels(ctx); err == nil {
			for _, c := range channels {
				if c.Active {
					s.ChannelsActive++
				} else {
					s.ChannelsInactive++
				}
			}
		}
		if bal, err := client.OnChainBalance(ctx); err == nil && bal != nil {
			s.ConfirmedSat = parseSat(bal.ConfirmedBalance)
			s.UnconfirmedSat = parseSat(bal.UnconfirmedBalance)
		}
		if bal, err := client.ChannelBalance(ctx); err == nil && bal != nil {
			s.LocalSat = parseSat(bal.LocalBalance.Sat)
			s.RemoteSat = parseSat(bal.RemoteBalance.Sat)
		}
		return s
	}
}

// envOr returns the value of the environment variable key, or fallback if unset.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// resolveDatabaseURL picks the Postgres connection string.
//
// When POSTGRES_HOST is set, the URL is assembled from the individual
// POSTGRES_HOST / POSTGRES_PORT / POSTGRES_DB / POSTGRES_USER / POSTGRES_PASSWORD
// vars. This lets the deployment choose the network location (e.g. the docker
// bridge DNS name "postgres") without re-stating credentials: compose sets
// POSTGRES_HOST, the operator's .env supplies the rest.
//
// When POSTGRES_HOST is empty (e.g. a host-networked deployment relying on the
// .env's pre-built URL), DATABASE_URL is used as-is, preserving prior behavior.
func resolveDatabaseURL() string {
	host := os.Getenv("POSTGRES_HOST")
	if host == "" {
		return envOr("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/dexarb?sslmode=disable")
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		envOr("POSTGRES_USER", "postgres"),
		envOr("POSTGRES_PASSWORD", "postgres"),
		host,
		envOr("POSTGRES_PORT", "5432"),
		envOr("POSTGRES_DB", "dexarb"),
	)
}

type robosatsTokenRepo interface {
	GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error)
}

func robosatsAuthTokenFromDB(ctx context.Context, repo robosatsTokenRepo, fallback string) (string, error) {
	keys, err := repo.GetActiveAPIKeysForProvider(ctx, "Robosats")
	if err != nil {
		return fallback, err
	}
	for _, key := range keys {
		if token := strings.TrimSpace(key.APIKey); token != "" {
			return token, nil
		}
	}
	return fallback, nil
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
	initCfg lnd.InitConfig,
) (string, *lnd.Client, error) {
	if lndHost == "" || tlsCertPath == "" || macaroonPath == "" {
		log.Println("LND startup configuration incomplete; skipping primary wallet bootstrap")
		if existing, err := repo.GetPrimaryWalletAddress(); err == nil && existing.Address != "" {
			return existing.Address, nil, nil
		}
		return "", nil, nil
	}
	if strings.TrimSpace(password) == "" {
		return "", nil, fmt.Errorf("LND wallet password missing in accounts.password for provider %q", db.LNDAccountProvider)
	}

	log.Println("initializing LND wallet hub")
	if initCfg.HasRestoreSecret() {
		if err := initCfg.Validate(); err != nil {
			return "", nil, fmt.Errorf("invalid LND wallet restore secret: %w", err)
		}
	}
	client, err := lnd.New(ctx, lndHost, tlsCertPath, macaroonPath, password, initCfg)
	if err != nil {
		return "", nil, err
	}
	if existing, err := repo.GetPrimaryWalletAddress(); err == nil && existing.Address != "" {
		log.Printf("reusing stored primary wallet address %s", existing.Address)
		return existing.Address, client, nil
	}

	address, err := client.NewAddress(ctx)
	if err != nil {
		return "", client, err
	}

	record := &db.WalletAddress{
		Label:   "Primary",
		Address: address,
	}
	if err := repo.SaveWalletAddress(record); err != nil {
		return "", client, err
	}

	log.Printf("generated new primary wallet address %s", address)
	return address, client, nil
}

func parseAccountHealthInterval(raw string) time.Duration {
	if raw == "" {
		return 10 * time.Minute
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return 10 * time.Minute
	}
	return interval
}

func loadLNDInitConfig(
	seedMnemonicFile, aezeedPassphraseFile, extendedMasterKeyFile, birthdayTimestampFile, recoveryWindowRaw, birthdayTimestampRaw string,
) (lnd.InitConfig, error) {
	cfg := lnd.InitConfig{}

	if path := strings.TrimSpace(seedMnemonicFile); path != "" {
		value, err := readSecretFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read LND_SEED_MNEMONIC_FILE: %w", err)
		}
		cfg.CipherSeedMnemonic = strings.Fields(value)
	}
	if path := strings.TrimSpace(aezeedPassphraseFile); path != "" {
		value, err := readSecretFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read LND_AEZEED_PASSPHRASE_FILE: %w", err)
		}
		cfg.AEZEEDPassphrase = value
	}
	if path := strings.TrimSpace(extendedMasterKeyFile); path != "" {
		value, err := readSecretFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read LND_EXTENDED_MASTER_KEY_FILE: %w", err)
		}
		cfg.ExtendedMasterKey = value
	}
	if path := strings.TrimSpace(birthdayTimestampFile); path != "" {
		value, err := readSecretFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP_FILE: %w", err)
		}
		birthdayTimestampRaw = value
	}
	if value := strings.TrimSpace(recoveryWindowRaw); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return cfg, fmt.Errorf("parse LND_RECOVERY_WINDOW: %w", err)
		}
		cfg.RecoveryWindow = int32(parsed)
	}
	if value := strings.TrimSpace(birthdayTimestampRaw); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("parse LND_EXTENDED_MASTER_KEY_BIRTHDAY_TIMESTAMP: %w", err)
		}
		cfg.ExtendedMasterKeyBirthdayTimestamp = parsed
	}

	return cfg, nil
}

func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func recordLNDMasterKeyHash(repo *db.TransactionRepository, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	hash, size, err := hashFileSHA256(path)
	if err != nil {
		return err
	}
	if err := repo.RecordLNDMasterKeyHash(hash, path, size); err != nil {
		return err
	}
	log.Printf("recorded LND master key file hash for %s", path)
	return nil
}

func hashFileSHA256(path string) (string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("read LND extended master key file: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), int64(len(data)), nil
}

// startTelegramSettingsPoller watches the shared telegram_settings row for
// config_version changes and applies them at runtime: it rebuilds the store's
// client, pushes the new client to the chat relay and anomaly notifier, and
// re-registers the DEXMM webhook with Telegram when one is configured. This is
// the hot-reload path that lets the UI update bot credentials without a restart.
func startTelegramSettingsPoller(ctx context.Context, repo *db.TelegramSettingsRepository, store *telegram.Store, relay *telegram.ChatRelay, anomaly *dexnotify.AnomalyNotifier) {
	ticker := time.NewTicker(telegram.PollInterval)
	defer ticker.Stop()
	var lastVersion int64
	if v, err := repo.ConfigVersion(ctx); err == nil {
		lastVersion = v
	}
	apply := func() {
		row, err := repo.Get(ctx)
		if err != nil {
			if err != db.ErrTelegramSettingsNotFound {
				log.Printf("telegram poller: load settings: %v", err)
			}
			return
		}
		if row.ConfigVersion == lastVersion {
			return
		}
		lastVersion = row.ConfigVersion
		store.Set(telegram.Config{
			Token:      row.BotToken,
			ChatID:     row.ChatID,
			WebhookURL: row.DexmmWebhookURL,
		})
		client := store.Client()
		if relay != nil {
			relay.SetClient(client)
		}
		if anomaly != nil {
			anomaly.SetClient(client)
		}
		if store.Enabled() && strings.TrimSpace(row.DexmmWebhookURL) != "" {
			if err := client.SetWebhook(ctx, row.DexmmWebhookURL); err != nil {
				log.Printf("telegram poller: setWebhook: %v", err)
			}
		}
		log.Printf("telegram settings reloaded (version=%d)", row.ConfigVersion)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			apply()
		}
	}
}
