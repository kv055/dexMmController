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

	"gorm.io/gorm"
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
	hhBaseURL := envOr("HODLHODL_URL", "")
	hhEscrowSeed := envOr("HODLHODL_ESCROW_SEED", "")
	hhEscrowPaymentPassword := envOr("HODLHODL_ESCROW_PAYMENT_PASSWORD", "")
	dexReleaseToken := envOr("DEX_RELEASE_WEBHOOK_TOKEN", "")
	binanceBaseURL := envOr("BINANCE_API_BASE_URL", "")
	krakenBaseURL := envOr("KRAKEN_API_BASE_URL", "")
	krakenFuturesBaseURL := envOr("KRAKEN_FUTURES_API_BASE_URL", "")
	coinbaseBaseURL := envOr("COINBASE_API_BASE_URL", "")
	coinbaseExchangeBaseURL := envOr("COINBASE_EXCHANGE_API_BASE_URL", "")
	fiatControllerURL := envOr("FIAT_CONTROLLER_URL", "http://fiat-controller:8080")
	fiatControllerHeartbeatSecret := envOr("FIAT_CONTROLLER_HEARTBEAT_SECRET", envOr("PUSHPUSH_HEARTBEAT_SECRET", ""))
	bisqGRPCHost := envOr("BISQ_GRPC_HOST", "localhost:9998")
	bisqAPIPassword := envOr("BISQ_API_PASSWORD", "")
	robosatsURL := envOr("ROBOSATS_URL", "")
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
	// The same secret gates every inbound API/WS route (except health, metrics
	// and the self-authenticating webhooks). Running without it leaves wallet
	// and trading endpoints open to anything on the Docker network, so refuse
	// to start unless that is explicitly opted into for local dev.
	internalAPISecret := strings.TrimSpace(os.Getenv("INTERNAL_API_SECRET"))
	if internalAPISecret == "" && !strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_INSECURE_INTERNAL")), "true") {
		log.Fatal("INTERNAL_API_SECRET is not set; refusing to serve the API unauthenticated (set ALLOW_INSECURE_INTERNAL=true to override for local dev)")
	}
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
	startupState := api.NewStartupState()
	startupState.RegisterMetrics(metricsReg.Registry)
	// Start the listener before any network dependency. This keeps liveness,
	// readiness and startup diagnostics available during daemon restarts.
	apiServer := api.NewServer(apiAddr, nil).
		WithInternalSecret(internalAPISecret).
		WithMetrics(metricsReg).
		WithStartupState(startupState)
	go func() {
		if err := apiServer.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("api server failed: %v", err)
			stop()
		}
	}()

	// ── Database ──────────────────────────────────────────────────────────────
	database, err := connectDatabaseWithRetry(ctx, databaseURL, startupState)
	if err != nil {
		log.Printf("database bootstrap stopped: %v", err)
		return
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
	hh, err := hodlHodlClientFromDB(ctx, repo, hhBaseURL)
	if err != nil {
		log.Printf("warning: failed to load HodlHodl credentials from database: %v", err)
		hh = hodlhodl.NewClient(hhBaseURL, "")
	}

	// ── Bisq / RoboSats ──────────────────────────────────────────────────────
	bq := bisq.NewClient(bisqGRPCHost, bisqAPIPassword)
	dbRobosatsAuthToken, err := robosatsAuthTokenFromDB(ctx, repo)
	if err != nil {
		log.Printf("warning: failed to load Robosats token from database: %v", err)
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
		webhookURL := strings.TrimSpace(row.DexmmWebhookURL)
		if webhookURL == "" {
			webhookURL = telegramCfg.WebhookURL
		}
		seedCfg = telegram.Config{
			Token:      row.BotToken,
			ChatID:     row.ChatID,
			WebhookURL: webhookURL,
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
		HodlHodlBaseURL:      hhBaseURL,
		BinanceBaseURL:       binanceBaseURL,
		KrakenBaseURL:        krakenBaseURL,
		KrakenFuturesBaseURL: krakenFuturesBaseURL,
		CoinbaseBaseURL:      coinbaseBaseURL,
		CoinbaseINTXBaseURL:  envOr("COINBASE_INTX_API_BASE_URL", ""),
		RobosatsBaseURL:      robosatsURL,
		TorProxyURL:          torProxyURL,
	})
	cexAccountService := cexaccount.NewService(repo, cexaccount.Config{
		HodlHodlBaseURL:         hhBaseURL,
		BinanceBaseURL:          binanceBaseURL,
		KrakenBaseURL:           krakenBaseURL,
		KrakenFuturesBaseURL:    krakenFuturesBaseURL,
		CoinbaseBaseURL:         coinbaseBaseURL,
		CoinbaseINTXBaseURL:     envOr("COINBASE_INTX_API_BASE_URL", ""),
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
			canonical, err := cexaccount.CanonicalProvider(provider)
			if err != nil || !cexaccount.IsCEXProvider(canonical) {
				return nil, fmt.Errorf("unsupported CEX provider %q", provider)
			}
			keys, err := repo.GetActiveAPIKeysForProvider(ctx, canonical)
			if err != nil {
				return nil, err
			}
			results := make([]cexaccount.KeyPermissionResult, 0, len(keys))
			for _, key := range keys {
				var perms map[string]string
				if len(key.Permissions) > 0 {
					if err := json.Unmarshal(key.Permissions, &perms); err != nil {
						return nil, fmt.Errorf("decode cached permissions for key %d: %w", key.ID, err)
					}
				}
				result := cexaccount.KeyPermissionResult{KeyID: key.ID, Name: key.Name, Permissions: perms}
				if key.PermissionCheckError != nil {
					result.Error = *key.PermissionCheckError
				}
				results = append(results, result)
			}
			// Return the assessed map even when some permissions are missing so
			// BidChecker can require spot-only (hedgeless) or spot+hedge based on
			// trade mode / decoupled hedge venue.
			combined, err := cexaccount.AssessCEXPermissions(canonical, results)
			if len(combined) == 0 {
				return nil, err
			}
			return combined, nil
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
		repo,
	)
	bidChecker.SetCommunicationGateway(telegramStore)
	bidChecker.SetFiatControllerChecker(cexaccount.NewHTTPFiatControllerChecker(
		fiatControllerURL,
		fiatControllerHeartbeatSecret,
		&http.Client{Timeout: 5 * time.Second},
	))
	cexAccountService.WithBidChecker(bidChecker)
	dexRelayWorker := dexrelay.NewWorker(repo, cexAccountService, btc, dexTaker, dexPaymentResolver)
	lndHealthChecker := lndhealth.NewChecker(repo, nil, lndhealth.Config{
		ExtendedMasterKeyFile: lndExtendedMasterKeyFile,
	})
	// hedgeStreamManager multiplexes each futures venue's live position feed to any
	// number of WebSocket subscribers. Each lazy source resolves the active database
	// credential when the first subscriber starts it.
	hedgeStreamManager := hedgestream.NewManager(map[string]hedgestream.SourceFunc{
		"kraken_futures": func(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
			return cexAccountService.StreamHedgePositions(ctx, "kraken_futures")
		},
		"binance_futures": func(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
			return cexAccountService.StreamHedgePositions(ctx, "binance_futures")
		},
		"coinbase_futures": func(ctx context.Context) (<-chan cex.HedgePositionUpdate, error) {
			return cexAccountService.StreamHedgePositions(ctx, "coinbase_futures")
		},
	})
	tradeFlow := tradeflow.New(cexAccountService, repo).
		WithDirectDestinationResolver(dexaccount.NewDirectResolver(bq))
	hc := healthcheck.NewHealthChecker(repo, claw, btc, hh, tradeFlow).
		WithRecoveryGracePeriod(parseAccountHealthInterval(envOr("TRADEFLOW_RECOVERY_GRACE", "3m"))).
		WithPaymentResolver(dexPaymentResolver).
		WithTelegramRelay(telegramSvc, anomalyNotifier).
		WithBisq(bq)
	apiServer = apiServer.WithRepository(repo).
		WithAccountHealth(exchangeAccountHealth).
		WithCexAccount(cexAccountService).
		WithTradeFlow(tradeFlow).
		WithHedgeStream(hedgeStreamManager).
		WithLNDHealth(lndHealthChecker).
		WithDexRelease(dexReleaseWorker, dexReleaseToken).
		WithBisqPayments(bq).
		WithServiceVersions(func(versionCtx context.Context) api.ServiceVersions {
			result := api.ServiceVersions{
				Bisq1: strings.TrimPrefix(envOr("BISQ_VERSION", ""), "v"),
				Bisq2: strings.TrimPrefix(envOr("BISQ2_VERSION", ""), "v"),
				LND:   strings.TrimPrefix(envOr("LND_VERSION", ""), "v"),
			}
			type versionResult struct{ service, version string }
			results := make(chan versionResult, 2)
			probe := func(service string, fetch func(context.Context) (string, error)) {
				go func() {
					probeCtx, cancel := context.WithTimeout(versionCtx, 3*time.Second)
					defer cancel()
					version, err := fetch(probeCtx)
					if err == nil {
						results <- versionResult{service: service, version: version}
					}
				}()
			}
			probe("bisq1", bq.Version)
			probe("bisq2", b2.Version)
			deadline := time.NewTimer(4 * time.Second)
			defer deadline.Stop()
			for remaining := 2; remaining > 0; {
				select {
				case item := <-results:
					if item.service == "bisq1" {
						result.Bisq1 = item.version
					} else {
						result.Bisq2 = item.version
					}
					remaining--
				case <-deadline.C:
					remaining = 0
				}
			}
			return result
		}).
		WithTelegramRelay(chatRelay).
		WithTelegramSettings(telegramSettingsRepo, telegramStore, anomalyNotifier)

	// lndInit creates (or restores) the LND wallet using the password stored in the
	// database, persists the generated seed + primary address + channel backup, and
	// injects the live client into the running API server and health checker. It runs
	// once at startup when a password already exists, and on demand from the UI via
	// POST /api/lnd/wallet/init.
	var lndBackupOnce sync.Once
	var lndWorkersOnce sync.Once
	var lndInitStatusMu sync.RWMutex
	var lastLNDInitError string
	lndInit := func(reqCtx context.Context) (result api.LNDInitResult, err error) {
		defer func() {
			lndInitStatusMu.Lock()
			defer lndInitStatusMu.Unlock()
			if err != nil {
				lastLNDInitError = err.Error()
				startupState.SetLNDReady(false, "lnd_bootstrap", err)
				return
			}
			lastLNDInitError = ""
			startupState.SetLNDReady(true, "ready", nil)
		}()
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
			return api.LNDInitResult{}, fmt.Errorf("persist LND channel backup: %w", err)
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
		bidChecker.SetBTCBalance(cexaccount.BTCBalanceSourceFunc(func(ctx context.Context) (float64, error) {
			bal, err := lndClient.OnChainBalance(ctx)
			if err != nil {
				return 0, err
			}
			if bal == nil {
				return 0, fmt.Errorf("empty LND on-chain balance response")
			}
			raw := strings.TrimSpace(bal.ConfirmedBalance)
			if raw == "" {
				return 0, nil
			}
			sats, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse LND confirmed balance %q: %w", raw, err)
			}
			return float64(sats) / 1e8, nil
		}))
		bidChecker.SetLNDOnline(cexaccount.LNDOnlineSourceFunc(func(ctx context.Context) error {
			return lndClient.HealthCheck(ctx)
		}))
		cexAccountService.WithLND(lndClient)
		dexRelayWorker.SetLND(dexrelay.LNDAdapter{Client: lndClient})
		// Periodic backup must outlive the request, so use the long-lived app context.
		lndBackupOnce.Do(func() {
			go runPeriodicLNDChannelBackup(ctx, lndBackupInterval, repo, lndClient, int64(lndAccount.ID))
		})
		lndWorkersOnce.Do(func() {
			lndHealthChecker.RunAndLog(ctx)
			go lndHealthChecker.RunPeriodic(ctx, accountHealthInterval)
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
		LastInitError: func() string {
			lndInitStatusMu.RLock()
			defer lndInitStatusMu.RUnlock()
			return lastLNDInitError
		},
	})
	startupState.SetDatabaseReady(true, "lnd_bootstrap", nil)

	if strings.TrimSpace(walletPassword) != "" {
		go retryLNDInitialization(ctx, apiServer, startupState)
	} else {
		log.Println("LND wallet password not set; skipping wallet bootstrap — create it from the UI")
		startupState.SetLNDReady(false, "awaiting_lnd_password", api.ErrLNDWalletPasswordMissing)
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

	go exchangeAccountHealth.RunPeriodic(ctx, accountHealthInterval)
	// The Stage-6 release worker only acts on webhook-delivered jobs (no autonomous
	// money-moving poll), so it runs unconditionally — unlike the team-gated dexrelay loop.
	go dexReleaseWorker.Run(ctx)

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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// connectDatabaseWithRetry makes dependency outages observable instead of
// turning them into controller restart loops. Local configuration/auth failures
// are still returned immediately because waiting cannot repair them.
func connectDatabaseWithRetry(ctx context.Context, databaseURL string, state *api.StartupState) (*gorm.DB, error) {
	delay := time.Second
	for {
		database, err := db.InitDB(databaseURL)
		if err == nil {
			state.SetDatabaseReady(true, "database_ready", nil)
			log.Println("database dependency is ready")
			return database, nil
		}
		if nonRetryableDatabaseError(err) {
			state.SetDatabaseReady(false, "database_configuration_error", err)
			return nil, err
		}
		state.SetDatabaseReady(false, "waiting_for_database", err)
		log.Printf("database unavailable: %v; retrying in %s", err, delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func nonRetryableDatabaseError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "password authentication failed") ||
		strings.Contains(message, "database does not exist") ||
		strings.Contains(message, "invalid dsn")
}

func retryLNDInitialization(ctx context.Context, server *api.Server, state *api.StartupState) {
	delay := time.Second
	for {
		state.SetLNDReady(false, "lnd_bootstrap", nil)
		result, err := server.InitializeLND(ctx)
		if err == nil {
			log.Printf("LND wallet bootstrap complete (primary address %s)", result.PrimaryAddress)
			return
		}
		state.SetLNDReady(false, "waiting_for_lnd", err)
		log.Printf("LND wallet bootstrap failed: %v; retrying in %s", err, delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
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

type exchangeAPIKeyRepo interface {
	GetActiveAPIKeysForProvider(ctx context.Context, provider string) ([]db.APIKey, error)
}

func hodlHodlClientFromDB(ctx context.Context, repo exchangeAPIKeyRepo, baseURL string) (*hodlhodl.Client, error) {
	keys, err := repo.GetActiveAPIKeysForProvider(ctx, "HodlHodl")
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		apiKey := strings.TrimSpace(key.APIKey)
		signatureKey := strings.TrimSpace(key.APISecret)
		if apiKey != "" && signatureKey != "" {
			return hodlhodl.NewClient(baseURL, apiKey).WithSignatureKey(signatureKey), nil
		}
	}
	return nil, fmt.Errorf("no active HodlHodl API credentials configured")
}

func robosatsAuthTokenFromDB(ctx context.Context, repo exchangeAPIKeyRepo) (string, error) {
	keys, err := repo.GetActiveAPIKeysForProvider(ctx, "Robosats")
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if token := strings.TrimSpace(key.APIKey); token != "" {
			return token, nil
		}
	}
	return "", nil
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
		webhookURL := firstNonEmpty(row.DexmmWebhookURL, store.Get().WebhookURL)
		store.Set(telegram.Config{
			Token:      row.BotToken,
			ChatID:     row.ChatID,
			WebhookURL: webhookURL,
		})
		client := store.Client()
		if relay != nil {
			relay.SetClient(client)
		}
		if anomaly != nil {
			anomaly.SetClient(client)
		}
		if store.Enabled() && webhookURL != "" {
			if err := client.SetWebhook(ctx, webhookURL); err != nil {
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
