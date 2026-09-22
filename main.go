package main

import (
	"context"
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
	"dexMmController/internal/exchangeconnectors/dex/mostro"
	"dexMmController/internal/exchangeconnectors/dex/robosats"
	"dexMmController/internal/healthcheck"
	"dexMmController/internal/hedgestream"
	"dexMmController/internal/lndhealth"
	"dexMmController/internal/lsp"
	"dexMmController/internal/magma"
	mmmetrics "dexMmController/internal/metrics"
	"dexMmController/internal/pretrade"
	"dexMmController/internal/swap"
	"dexMmController/internal/tradeflow"
	lnd "dexMmController/internal/wallethub"

	"commons/credentialcrypto"
	"commons/logging"
	"commons/metrics"
	"commons/mmcapabilities"
	"commons/telegram"
	"commons/tracing"
	boltzlib "github.com/BoltzExchange/boltz-client/v2/pkg/boltz"

	"gorm.io/gorm"
)

// paymentMethodSnapshot converts a payment_methods DB row into the pre-trade
// checker's view of it.
func paymentMethodSnapshot(row *db.PaymentMethod) *pretrade.PaymentMethodSnapshot {
	return &pretrade.PaymentMethodSnapshot{
		Name:      row.Name,
		Category:  row.Category,
		AccountID: row.AccountID,
		IsActive:  row.IsActive,
		IsLinked:  row.IsLinked,
		Priority:  row.Priority,
	}
}

func splitCSV(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func main() {
	// ── Config ────────────────────────────────────────────────────────────────
	databaseURL := resolveDatabaseURL()
	hhBaseURL := envOr("HODLHODL_URL", "")
	hhEscrowSeed := envOr("HODLHODL_ESCROW_SEED", "")
	hhEscrowPaymentPassword := envOr("HODLHODL_ESCROW_PAYMENT_PASSWORD", "")
	mostroMnemonic := envOr("MOSTRO_MNEMONIC", "")
	mostroIdentityLabel := envOr("MOSTRO_IDENTITY_LABEL", "default")
	mostroRelays := splitCSV(envOr("MOSTRO_RELAYS", "wss://relay.mostro.network,wss://relay.mostro.services,wss://mostro-p2p.tech"))
	dexReleaseToken := envOr("DEX_RELEASE_WEBHOOK_TOKEN", "")
	binanceBaseURL := envOr("BINANCE_API_BASE_URL", "")
	krakenBaseURL := envOr("KRAKEN_API_BASE_URL", "")
	krakenFuturesBaseURL := envOr("KRAKEN_FUTURES_API_BASE_URL", "")
	coinbaseBaseURL := envOr("COINBASE_API_BASE_URL", "")
	fiatControllerURL := envOr("FIAT_CONTROLLER_URL", "http://fiat-controller:8080")
	fiatControllerHeartbeatSecret := envOr("FIAT_CONTROLLER_HEARTBEAT_SECRET", envOr("PUSHPUSH_HEARTBEAT_SECRET", ""))
	bisqGRPCHost := envOr("BISQ_GRPC_HOST", "localhost:9998")
	bisqAPIPassword := envOr("BISQ_API_PASSWORD", "")
	robosatsURL := envOr("ROBOSATS_URL", "")
	boltzURL := strings.TrimRight(envOr("BOLTZ_URL", ""), "/")
	bisq2RESTHost := envOr("BISQ2_REST_HOST", "")
	bisq2APIPassword := envOr("BISQ2_API_PASSWORD", "")
	telegramCfg := telegram.LoadConfig("DEXMM")
	torProxyURL := envOr("TOR_PROXY_URL", "")
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
	lndRecoveryWindow := envOr("LND_RECOVERY_WINDOW", "")
	bitcoindRPCURL := envOr("LND_BITCOIND_RPC_URL", "")
	bitcoindRPCUser := envOr("LND_BITCOIND_RPCUSER", "")
	bitcoindRPCPass := envOr("LND_BITCOIND_RPCPASS", "")
	accountHealthInterval := parseAccountHealthInterval(envOr("ACCOUNT_HEALTH_INTERVAL", ""))
	healthcheckInterval := parseAccountHealthInterval(envOr("HEALTHCHECK_INTERVAL", "30s"))
	bisqFiatEscalationDelay := parseDurationOr(envOr("BISQ_FIAT_ESCALATION_AFTER", "2h"), 2*time.Hour)
	automatedTradeflowWorkers := strings.EqualFold(strings.TrimSpace(envOr("ENABLE_AUTOMATED_TRADEFLOW_WORKERS", "false")), "true")
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
	mmmetrics.Register(metricsReg.Registry)
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
	rawDatabase, err := database.DB()
	if err != nil {
		log.Printf("MM capability database handle: %v", err)
		return
	}
	capabilityStore := mmcapabilities.NewStore(rawDatabase, mmcapabilities.ParseMaxAge(os.Getenv("MM_CEX_CAPABILITY_MAX_AGE")))

	repo := db.NewTransactionRepository(database)
	transactionalLiquiditySettingsRepo := db.NewTransactionalLiquiditySettingsRepository(database)
	lspChannelConfigRepo := db.NewLSPChannelConfigRepository(database)
	lspMagmaOrderRepo := db.NewLSPMagmaOrderRepository(database)
	submarineSwapRepo := db.NewSubmarineSwapRepository(database)
	mmmetrics.RegisterTradeStats(metricsReg.Registry, repo)
	magmaCoordinator := &magma.Coordinator{
		Client:             magma.NewClient(strings.TrimRight(envOr("MAGMA_LSP_BASE_URL", ""), "/")),
		Offers:             magma.NewOffersClient(envOr("AMBOSS_GRAPHQL_URL", ""), envOr("AMBOSS_API_KEY", "")),
		Store:              db.NewMagmaOrderStore(lspMagmaOrderRepo),
		LSPRegistrar:       db.NewLSPConfigRegistrar(lspChannelConfigRepo),
		DefaultPool:        magma.Pool(envOr("MAGMA_LSP_DEFAULT_POOL", string(magma.PoolRecommended))),
		DefaultFeeLimitSat: envInt64Or("MAGMA_LSP_PAY_FEE_LIMIT_SAT", 1000),
	}

	lndAccount, err := repo.EnsureAccount(ctx, db.LNDAccountProvider)
	if err != nil {
		log.Fatalf("failed to ensure LND account row: %v", err)
	}

	fileInitCfg, err := loadLNDInitConfig(
		lndSeedMnemonicFile,
		lndAEZEEDPassphraseFile,
		lndRecoveryWindow,
	)
	if err != nil {
		log.Fatalf("invalid LND wallet secret configuration: %v", err)
	}
	if err := importLNDSeedFromFile(ctx, repo, int64(lndAccount.ID), fileInitCfg); err != nil {
		log.Fatalf("failed to import LND seed into database: %v", err)
	}

	// LND wallet bootstrap is defined below (lndInit) so it runs automatically at
	// startup — generating and storing the wallet password when none exists yet —
	// or on demand from the UI via POST /api/lnd/wallet/init.

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

	// The bisq wallet must stay unencrypted: encryption only adds failure modes for
	// automated trading (locked-wallet errors on balance/withdrawal calls), and the
	// headless daemon has no UI to re-enter a forgotten password. Probe once the
	// daemon is up and strip any encryption found; the API password doubles as the
	// candidate wallet password in deployments where they are configured together.
	go func() {
		const attempts = 60
		const interval = 15 * time.Second
		for i := 1; ; i++ {
			removed, err := bq.EnsureWalletUnencrypted(ctx, bisqAPIPassword)
			switch {
			case err == nil && removed:
				log.Printf("bisq wallet: removed wallet encryption (policy: wallets stay unencrypted)")
				return
			case err == nil:
				log.Printf("bisq wallet: verified unencrypted")
				return
			case strings.Contains(err.Error(), "wallet is not yet available") || strings.Contains(err.Error(), "not yet initialized"):
				if i == attempts {
					log.Printf("warning: bisq wallet encryption check gave up after %d attempts: daemon wallet never became available", attempts)
					return
				}
			default:
				log.Printf("warning: bisq wallet stays encrypted and the API password does not unlock it "+
					"(%v). Unlock it manually and call RemoveWalletPassword to enforce the unencrypted-wallet policy.", err)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()

	var mostroManager *mostro.Manager
	if strings.TrimSpace(mostroMnemonic) != "" || strings.EqualFold(envOr("MOSTRO_ENABLED", "false"), "true") {
		encrypted, err := credentialcrypto.Encrypt(mostroMnemonic)
		if err != nil {
			log.Fatalf("encrypt Mostro mnemonic: %v", err)
		}
		mostroManager = &mostro.Manager{Mnemonic: mostroMnemonic, IdentityLabel: mostroIdentityLabel, EncryptedMnemonic: []byte(encrypted), Relays: mostroRelays, Store: repo, Enabled: strings.EqualFold(envOr("MOSTRO_ENABLED", "false"), "true"), AllowedNodes: splitCSV(envOr("MOSTRO_ALLOWED_NODES", "")), Privacy: strings.EqualFold(envOr("MOSTRO_PRIVACY_MODE", "false"), "true")}
	}
	var mostroTaker dexaccount.MostroTaker
	if mostroManager != nil {
		mostroTaker = mostroManager
	}
	dexTaker := dexaccount.NewTaker(repo, bq, hh, rs, mostroTaker).WithBisq2(b2)
	// dexReleaser performs Stage-6 BTC release; dexReleaseWorker drains the webhook-fed
	// queue and runs the (idempotent) releases in the background.
	dexReleaser := dexaccount.NewReleaser(repo, bq, hh, rs, dexaccount.HodlHodlEscrowConfig{
		EncryptedSeed:   hhEscrowSeed,
		PaymentPassword: hhEscrowPaymentPassword,
	})
	dexReleaser.WithMostro(mostroManager).WithBisq2(b2)
	if mostroManager != nil {
		dexPaymentResolver.WithMostro(mostroManager)
	}
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
		HodlHodlBaseURL:      hhBaseURL,
		BinanceBaseURL:       binanceBaseURL,
		KrakenBaseURL:        krakenBaseURL,
		KrakenFuturesBaseURL: krakenFuturesBaseURL,
		CoinbaseBaseURL:      coinbaseBaseURL,
		CoinbaseINTXBaseURL:  envOr("COINBASE_INTX_API_BASE_URL", ""),
		RobosatsBaseURL:      robosatsURL,
		TorProxyURL:          torProxyURL,
	})
	preTradeChecker := pretrade.NewPreTradeChecker(
		pretrade.BalanceFetcherFunc(func(ctx context.Context, provider, currency string) (float64, error) {
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
		pretrade.WhitelistFetcherFunc(func(ctx context.Context, provider string) ([]string, error) {
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
		pretrade.PermissionCheckerFunc(func(ctx context.Context, provider string) (map[string]string, error) {
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
		pretrade.PaymentSetupFetcherFuncs{
			MethodFn: func(ctx context.Context, name string) (*pretrade.PaymentMethodSnapshot, error) {
				row, err := repo.GetPaymentMethodByName(ctx, name)
				if err != nil || row == nil {
					return nil, err
				}
				return paymentMethodSnapshot(row), nil
			},
			PickFn: func(ctx context.Context, names []string) (*pretrade.PaymentMethodSnapshot, error) {
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
				return &pretrade.FiatAccountSnapshot{
					Provider:   acct.Provider,
					Email:      acct.Email,
					Phone:      acct.Phone,
					PaymentTag: acct.PaymentTag,
				}, nil
			},
			BankFn: func(ctx context.Context, accountID int64) (*cexaccount.BankAccountSnapshot, error) {
				row, err := repo.GetBankAccountForAccount(ctx, accountID)
				if err != nil || row == nil {
					return nil, err
				}
				details, err := credentialcrypto.Decrypt(row.DetailsEncrypted)
				if err != nil {
					return nil, fmt.Errorf("decrypt bank account details: %w", err)
				}
				return &pretrade.BankAccountSnapshot{
					Scheme:   row.Scheme,
					Details:  pretrade.ParseBankAccountDetails([]byte(details)),
					IsActive: row.IsActive,
				}, nil
			},
		},
		repo,
	)
	preTradeChecker.SetCommunicationGateway(telegramStore)
	var boltzAPI *boltzlib.Api
	if boltzURL != "" {
		boltzAPI = &boltzlib.Api{URL: boltzURL, Client: http.Client{Timeout: 10 * time.Second}}
		preTradeChecker.SetSwapQuoteSource(swap.BoltzQuoteSource{API: boltzAPI})
	}
	preTradeChecker.SetFiatControllerChecker(cexaccount.NewHTTPFiatControllerChecker(
		fiatControllerURL,
		fiatControllerHeartbeatSecret,
		&http.Client{Timeout: 5 * time.Second},
	))
	cexAccountService.WithPreTradeChecker(preTradeChecker)
	dexRelayWorker := dexrelay.NewWorker(repo, cexAccountService, btc, dexTaker, dexPaymentResolver)
	lndHealthChecker := lndhealth.NewChecker(repo, nil).
		WithChainBackend(bitcoin.NewCoreRPCClient(bitcoindRPCURL, bitcoindRPCUser, bitcoindRPCPass))
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
		WithDirectDestinationResolver(dexaccount.NewDirectResolver(bq)).
		WithChainConfirmationSource(btc).
		WithCapabilities(capabilityStore)
	hc := healthcheck.NewHealthChecker(repo, btc, hh, tradeFlow).
		WithRecoveryGracePeriod(parseAccountHealthInterval(envOr("TRADEFLOW_RECOVERY_GRACE", "3m"))).
		WithBisqFiatEscalationDelay(bisqFiatEscalationDelay).
		WithPaymentResolver(dexPaymentResolver).
		WithMostro(mostroManager).
		WithTelegramRelay(telegramSvc, anomalyNotifier).
		WithBisq(bq)
	apiServer = apiServer.WithRepository(repo).
		WithAccountHealth(exchangeAccountHealth).
		WithCapabilities(capabilityStore).
		WithCexAccount(cexAccountService).
		WithTradeFlow(tradeFlow).
		WithHedgeStream(hedgeStreamManager).
		WithLNDHealth(lndHealthChecker).
		WithDexRelease(dexReleaseWorker, dexReleaseToken).
		WithBisqPayments(bq).
		WithBisqWallet(bq).
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
		WithTelegramSettings(telegramSettingsRepo, telegramStore, anomalyNotifier).
		WithTransactionalLiquiditySettings(transactionalLiquiditySettingsRepo).
		WithLSPChannelConfig(lspChannelConfigRepo).
		WithSubmarineSwapHistory(submarineSwapRepo).
		WithMagmaLSP(magmaCoordinator)

	// lndInit creates (or restores) the LND wallet, auto-generating and storing an
	// encrypted password when the operator has not configured one. It persists the
	// generated seed + primary address + channel backup and injects the live client
	// into the running API server and health checker. It runs automatically at
	// startup via retryLNDInitialization, or on demand from the UI via
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
			return api.LNDInitResult{}, fmt.Errorf("load LND wallet credentials: %w", err)
		}
		if strings.TrimSpace(password) == "" {
			// No operator-configured password: generate one, store it encrypted,
			// and use it for wallet creation. It stays revealable via the
			// dexMonitor Accounts panel.
			generated, genErr := generateWalletPassword()
			if genErr != nil {
				return api.LNDInitResult{}, genErr
			}
			if err := repo.EnsureLNDWalletPassword(reqCtx, int64(lndAccount.ID), generated); err != nil {
				return api.LNDInitResult{}, fmt.Errorf("store generated LND wallet password: %w", err)
			}
			// Re-read so concurrent provisioning can never init the wallet with a
			// password that differs from the stored one.
			stored, _, credErr := repo.LNDWalletCredentials(reqCtx)
			if credErr != nil {
				return api.LNDInitResult{}, fmt.Errorf("reload LND wallet password after generation: %w", credErr)
			}
			if strings.TrimSpace(stored) == "" {
				return api.LNDInitResult{}, fmt.Errorf("generated LND wallet password was not persisted")
			}
			password = stored
			log.Println("generated LND wallet password and stored it encrypted in accounts.password (revealable via dexMonitor Accounts panel)")
		}
		cfg := mergeLNDInitConfig(fileInitCfg, aezeed)
		if strings.EqualFold(strings.TrimSpace(os.Getenv("LND_INIT_FRESH")), "true") {
			// Keep previously stored recovery material intact, but don't apply it
			// while deliberately initializing a separate, empty wallet.
			log.Println("LND fresh-wallet initialization requested; skipping stored seed and channel backup")
			cfg = lnd.InitConfig{}
		} else {
			cfg.ChannelBackupRestore = loadLNDChannelBackupRestore(reqCtx, repo, int64(lndAccount.ID))
		}

		primaryAddress, lndClient, err := ensurePrimaryDepositAddress(
			reqCtx, repo, lndHost, lndTLSCertPath, lndMacaroonPath, password, cfg,
		)
		if err != nil {
			return api.LNDInitResult{}, err
		}
		if lndClient == nil {
			return api.LNDInitResult{}, fmt.Errorf("LND wallet client was not created: %w", api.ErrLNDConfigIncomplete)
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
		magmaCoordinator.Wallet = func(context.Context) magma.LNDWallet { return lndClient }
		lndHealthChecker.SetClient(lndClient)
		mmmetrics.SetLNDStatsSource(lndStatsFetcher(lndClient))
		preTradeChecker.SetWallet(pretrade.WalletSourceFunc(func(ctx context.Context) ([]string, error) {
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
		preTradeChecker.SetBTCBalance(pretrade.BTCBalanceSourceFunc(func(ctx context.Context) (float64, error) {
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
		preTradeChecker.SetLightningBalance(pretrade.LightningBalanceSourceFuncs{
			OutboundFn: func(ctx context.Context) (float64, error) {
				bal, err := lndClient.ChannelBalance(ctx)
				if err != nil {
					return 0, err
				}
				if bal == nil || strings.TrimSpace(bal.LocalBalance.Sat) == "" {
					return 0, nil
				}
				sats, err := strconv.ParseInt(strings.TrimSpace(bal.LocalBalance.Sat), 10, 64)
				if err != nil {
					return 0, fmt.Errorf("parse LND local channel balance: %w", err)
				}
				return float64(sats) / 1e8, nil
			},
			InboundFn: func(ctx context.Context) (float64, error) {
				bal, err := lndClient.ChannelBalance(ctx)
				if err != nil {
					return 0, err
				}
				if bal == nil || strings.TrimSpace(bal.RemoteBalance.Sat) == "" {
					return 0, nil
				}
				sats, err := strconv.ParseInt(strings.TrimSpace(bal.RemoteBalance.Sat), 10, 64)
				if err != nil {
					return 0, fmt.Errorf("parse LND remote channel balance: %w", err)
				}
				return float64(sats) / 1e8, nil
			},
		})
		preTradeChecker.SetLightningChannels(pretrade.LightningChannelSourceFunc(func(ctx context.Context) (bool, error) {
			channels, err := lndClient.ListChannels(ctx)
			if err != nil {
				return false, err
			}
			for _, ch := range channels {
				if ch.Active {
					return true, nil
				}
			}
			return false, nil
		}))
		lspStatusSource := pretrade.LSPStatusSourceFunc(func(ctx context.Context) (lsp.Status, error) {
			cfg, err := lspChannelConfigRepo.Get(ctx)
			if err != nil {
				return lsp.Inspect(lsp.Config{}, nil), err
			}
			channels, err := lndClient.ListChannels(ctx)
			if err != nil {
				return lsp.Status{}, err
			}
			return lsp.Inspect(cfg, channels), nil
		})
		preTradeChecker.SetLSPStatus(lspStatusSource)
		preTradeChecker.SetLNDOnline(pretrade.LNDOnlineSourceFunc(func(ctx context.Context) error {
			return lndClient.HealthCheck(ctx)
		}))
		cexAccountService.WithLND(lndClient)
		if mostroManager != nil {
			mostroManager.Wallet = lndClient
		}
		dexRelayWorker.SetLND(dexrelay.LNDAdapter{Client: lndClient})

		// The coordinator is embedded in mm-controller. It can reserve sufficient
		// existing outbound balance even without Boltz configured, but it cannot
		// initiate a swap until the URL and both operator fee ceilings are present.
		walletRuntime := lndBoltzRuntime{client: lndClient}
		coordinator := &swap.Coordinator{
			Wallet: lndClient,
			Store:  submarineSwapRepo,
			Sealer: swap.CredentialSealer{},
			FeeLimits: func(limitCtx context.Context) (int64, int64, error) {
				cfg, cfgErr := lspChannelConfigRepo.Get(limitCtx)
				if cfgErr != nil {
					return 0, 0, cfgErr
				}
				if !cfg.Enabled || cfg.MaxSwapFeeSat <= 0 || cfg.MaxSwapFeePPM <= 0 {
					return 0, 0, fmt.Errorf("submarine swaps require an enabled LSP channel and positive absolute and relative fee ceilings")
				}
				return cfg.MaxSwapFeeSat, cfg.MaxSwapFeePPM, nil
			},
			Revalidate: func(checkCtx context.Context, quote swap.Quote) error {
				cfg, cfgErr := lspChannelConfigRepo.Get(checkCtx)
				if cfgErr != nil {
					return cfgErr
				}
				channels, channelsErr := lndClient.ListChannels(checkCtx)
				if channelsErr != nil {
					return channelsErr
				}
				status := lsp.Inspect(cfg, channels)
				if !status.LSPHealthy {
					return fmt.Errorf("registered LSP channel unavailable: %s", status.Reason)
				}
				if status.LSPInboundSat < quote.LightningOutputSat {
					return fmt.Errorf("registered LSP inbound liquidity changed: available %d sats, required %d sats", status.LSPInboundSat, quote.LightningOutputSat)
				}
				if err := quote.Validate(time.Now(), cfg.MaxSwapFeeSat, cfg.MaxSwapFeePPM); err != nil {
					return err
				}
				balanceBTC, balanceErr := walletRuntime.ConfirmedOnChainBTC(checkCtx)
				if balanceErr != nil {
					return balanceErr
				}
				if confirmedSat := int64(balanceBTC * 1e8); confirmedSat < quote.LockupAmountSat+quote.FundingTxFeeSat {
					return fmt.Errorf("confirmed LND on-chain balance changed: available %d sats, required %d sats", confirmedSat, quote.LockupAmountSat+quote.FundingTxFeeSat)
				}
				return nil
			},
			SatPerVByte: 2,
		}
		if boltzAPI != nil {
			refunder := swap.LibraryRefunder{API: boltzAPI, Recovery: submarineSwapRepo, Destination: walletRuntime, SatPerVByte: 2}
			coordinator.Provider = swap.OfficialBoltzProvider{
				Quotes:   swap.BoltzQuoteSource{API: boltzAPI},
				Invoices: walletRuntime,
				Refunder: refunder,
			}
		}
		liquidityExecutor := tradeflow.SwapExecutor{Coordinator: coordinator, Balance: walletRuntime, Status: lspStatusSource}
		tradeFlow.WithLiquidityExecutor(liquidityExecutor)
		dexRelayWorker.SetLiquidityExecutor(liquidityExecutor)
		if automatedTradeflowWorkers {
			tradeFlow.WithHedgelessExecution(dexTaker, dexrelay.LNDAdapter{Client: lndClient})
		}
		// Periodic backup must outlive the request, so use the long-lived app context.
		lndBackupOnce.Do(func() {
			go runPeriodicLNDChannelBackup(ctx, lndBackupInterval, repo, lndClient, int64(lndAccount.ID))
		})
		lndWorkersOnce.Do(func() {
			lndHealthChecker.RunAndLog(ctx)
			go lndHealthChecker.RunPeriodic(ctx, accountHealthInterval)
			go magmaCoordinator.RunPoller(ctx)
			if boltzAPI != nil {
				go func() {
					if recoverErr := coordinator.Recover(ctx); recoverErr != nil && ctx.Err() == nil {
						log.Printf("Boltz funded-swap recovery: %v", recoverErr)
					}
				}()
			}
		})

		if primaryAddress != "" {
			log.Printf("primary CEX deposit address ready: %s", primaryAddress)
		}
		return api.LNDInitResult{PrimaryAddress: primaryAddress}, nil
	}
	lndHealthChecker.WithInitStatus(func() string {
		lndInitStatusMu.RLock()
		defer lndInitStatusMu.RUnlock()
		return strings.TrimSpace(lastLNDInitError)
	})
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

	// The supervisor provisions credentials (generating a wallet password when the
	// operator has not set one), then creates or restores the wallet. It retries
	// transient failures forever and stops on permanent misconfigurations.
	go retryLNDInitialization(ctx, apiServer, startupState)

	// ── Periodic healthcheck + dexrelay ─────────────────────────────────────────
	// Money-moving recovery remains an explicit deployment opt-in. Hedgeless
	// execution is rejected until the LND wallet is wired, and its refill worker
	// shares this gate with the existing hedgefull recovery/relay workers.
	if automatedTradeflowWorkers {
		go hc.RunPeriodic(ctx, healthcheckInterval)
		go dexRelayWorker.RunPeriodic(ctx, healthcheckInterval)
		go tradeFlow.RunRefillWorker(ctx, healthcheckInterval)
	} else {
		log.Println("automated tradeflow workers disabled; set ENABLE_AUTOMATED_TRADEFLOW_WORKERS=true after staging validation")
	}

	go exchangeAccountHealth.RunPeriodic(ctx, accountHealthInterval)
	// The Stage-6 release worker only acts on webhook-delivered jobs (no autonomous
	// money-moving poll), so it runs unconditionally — unlike the team-gated dexrelay loop.
	go dexReleaseWorker.Run(ctx)

	<-ctx.Done()
}

// lndStatsFetcher adapts the live wallet client to the metrics LND collector. Wired
// here (not in metrics) because metrics must not import wallethub — wallethub pulls in
// the CEX connectors, which already record into metrics.
func lndStatsFetcher(client *lnd.Client) mmmetrics.LNDStatsFunc {
	parseSat := func(raw string) float64 {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0
		}
		return v
	}
	return func(ctx context.Context) mmmetrics.LNDStats {
		var s mmmetrics.LNDStats
		if err := client.HealthCheck(ctx); err != nil {
			return s
		}
		s.Up = true
		if info, err := client.NodeInfo(ctx); err == nil && info != nil {
			s.SyncedToChain = info.SyncedToChain
			s.SyncedToGraph = info.SyncedToGraph
			s.BlockHeight = float64(info.BlockHeight)
		}
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

func envInt64Or(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return n
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

// retryLNDInitialization drives the LND wallet bootstrap loop. Transient
// failures (database or LND still coming up) are retried with capped exponential
// backoff; permanent misconfigurations stop the loop and surface through the
// startup state until the operator fixes them.
func retryLNDInitialization(ctx context.Context, server *api.Server, state *api.StartupState) {
	delay := time.Second
	for {
		state.SetLNDReady(false, "lnd_bootstrap", nil)
		result, err := server.InitializeLND(ctx)
		if err == nil {
			log.Printf("LND wallet bootstrap complete (primary address %s)", result.PrimaryAddress)
			return
		}
		if isPermanentLNDInitError(err) {
			state.SetLNDReady(false, "lnd_bootstrap_failed", err)
			log.Printf("LND wallet bootstrap stopped: %s (fix the configuration and restart)", describePermanentLNDInitError(err))
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

func ensurePrimaryDepositAddress(
	ctx context.Context,
	repo *db.TransactionRepository,
	lndHost, tlsCertPath, macaroonPath, password string,
	initCfg lnd.InitConfig,
) (string, *lnd.Client, error) {
	if lndHost == "" || tlsCertPath == "" || macaroonPath == "" {
		log.Println("LND startup configuration incomplete; skipping primary wallet bootstrap")
		if existing, err := repo.GetPrimaryWalletAddress(); err == nil && existing.Address != "" {
			return existing.Address, nil, fmt.Errorf("%w (stored primary address %s reused)", api.ErrLNDConfigIncomplete, existing.Address)
		}
		return "", nil, api.ErrLNDConfigIncomplete
	}
	if strings.TrimSpace(password) == "" {
		return "", nil, fmt.Errorf("LND wallet password missing in accounts.password for provider %q", db.LNDAccountProvider)
	}

	log.Println("initializing LND wallet hub")
	if initCfg.HasRestoreSecret() {
		if err := initCfg.Validate(); err != nil {
			return "", nil, fmt.Errorf("%w: %v", api.ErrLNDRestoreSecretInvalid, err)
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

func parseDurationOr(raw string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return d
}

func loadLNDInitConfig(
	seedMnemonicFile, aezeedPassphraseFile, recoveryWindowRaw string,
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
	if value := strings.TrimSpace(recoveryWindowRaw); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return cfg, fmt.Errorf("parse LND_RECOVERY_WINDOW: %w", err)
		}
		cfg.RecoveryWindow = int32(parsed)
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
