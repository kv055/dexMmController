package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dexMmController/internal/cexaccount"
	"dexMmController/internal/db"
	"dexMmController/internal/hedgestream"
	"dexMmController/internal/lndhealth"

	"commons/metrics"
	"commons/telegram"
)

// Server represents a server.
type Server struct {
	addr          string
	repo          *db.TransactionRepository
	accountHealth accountHealthChecker
	cexAccount    *cexaccount.Service
	tradeFlow     TradeFlowRunner
	hedgeStream   *hedgestream.Manager
	lndHealth     lndWalletHealthChecker

	// lndWallet is the live LND client. It can be wired at startup or swapped in at
	// runtime once the wallet is created via the init endpoint, so it is guarded.
	lndWalletMu sync.RWMutex
	lndWallet   lndWalletClient

	// LND on-demand management hooks, set by main via WithLNDManagement.
	lndInit       func(ctx context.Context) (LNDInitResult, error)
	lndStateProbe func(ctx context.Context) (string, error)
	lndInitMu     sync.Mutex

	// Stage-6 release webhook (fiat-controller → release worker), set via WithDexRelease.
	dexRelease      dexReleaseEnqueuer
	dexReleaseToken string

	// bisqPayments is the bisq1 gRPC client used to list/create/delete payment accounts in
	// the bisq daemon's own profile, set via WithBisqPayments. Nil when the DEX stack is
	// disabled, in which case the payment-account routes answer 503.
	bisqPayments bisqPaymentClient

	// telegramRelay forwards operator Telegram replies into active DEX trade chats.
	telegramRelay *telegram.ChatRelay

	// telegramSettingsRepo persists the shared bot token + per-service webhook URLs.
	// telegramStore is the runtime hot-reloadable config holder. telegramExtraSetters
	// are additional consumers (e.g. the anomaly notifier) that should receive a
	// rebuilt client whenever the operator saves new Telegram config via the UI.
	telegramSettingsRepo telegramSettingsBackend
	telegramStore        *telegram.Store
	telegramExtraSetters []telegramClientSetter

	// metrics is the commons service registry; when set, Run serves /metrics and
	// wraps the mux with the RED request middleware.
	metrics *metrics.ServiceRegistry

	// internalSecret gates every route except /health, /metrics and the two
	// webhook endpoints that carry their own credential. Callers on the Docker
	// network (dexMonitor proxy, account health) must send it as
	// X-Internal-Secret. Empty means the gate is disabled; main refuses to
	// start that way unless ALLOW_INSECURE_INTERNAL=true.
	internalSecret string
}

// LNDInitResult is returned by the on-demand wallet creation hook.
type LNDInitResult struct {
	PrimaryAddress string `json:"primary_address"`
}

// LNDManagement bundles the on-demand LND wallet hooks wired in by main.
type LNDManagement struct {
	// Init creates (or restores) the wallet using the password already stored in the
	// database, persists the generated seed + primary address, and injects the live
	// client into the running server and health checker.
	Init func(ctx context.Context) (LNDInitResult, error)
	// StateProbe reports LND's wallet state (e.g. NON_EXISTING, LOCKED, RPC_ACTIVE).
	StateProbe func(ctx context.Context) (string, error)
}

// ErrLNDWalletPasswordMissing signals the wallet cannot be created because no password
// is stored yet. The init hook returns it so the handler can answer 400.
var ErrLNDWalletPasswordMissing = errors.New("LND wallet password is not set; set it under Account → Edit first")

type accountHealthChecker interface {
	CheckProvider(ctx context.Context, provider string) (cexaccount.HealthResult, error)
}

type lndWalletHealthChecker interface {
	Check(ctx context.Context) (lndhealth.Result, error)
}

type transactionListResponse struct {
	Transactions []db.Transaction `json:"transactions"`
	Total        int64            `json:"total"`
	Limit        int              `json:"limit"`
	Offset       int              `json:"offset"`
}

type healthResponse struct {
	Status               string `json:"status"`
	TransactionCount     int64  `json:"transaction_count"`
	IncompleteCount      int    `json:"incomplete_count"`
	PrimaryWalletPresent bool   `json:"primary_wallet_present"`
}

// NewServer creates a server.
func NewServer(addr string, repo *db.TransactionRepository) *Server {
	return &Server{
		addr: addr,
		repo: repo,
	}
}

// WithAccountHealth attaches the account health service to the API server.
func (s *Server) WithAccountHealth(checker accountHealthChecker) *Server {
	s.accountHealth = checker
	return s
}

// WithLNDHealth attaches the LND wallet health checker to the API server.
func (s *Server) WithLNDHealth(checker lndWalletHealthChecker) *Server {
	s.lndHealth = checker
	return s
}

// WithLNDManagement attaches the on-demand LND wallet creation/state hooks.
func (s *Server) WithLNDManagement(m LNDManagement) *Server {
	s.lndInit = m.Init
	s.lndStateProbe = m.StateProbe
	return s
}

// WithMetrics attaches the Prometheus service registry: Run then serves
// GET /metrics and counts/times every request through the shared middleware.
func (s *Server) WithMetrics(reg *metrics.ServiceRegistry) *Server {
	s.metrics = reg
	return s
}

// WithBisqPayments attaches the bisq1 payment-account client used by the
// /api/bisq/payment-accounts routes.
func (s *Server) WithBisqPayments(client bisqPaymentClient) *Server {
	s.bisqPayments = client
	return s
}

// WithInternalSecret sets the shared secret required on X-Internal-Secret for
// all API and WS routes (webhooks and health/metrics excepted).
func (s *Server) WithInternalSecret(secret string) *Server {
	s.internalSecret = strings.TrimSpace(secret)
	return s
}

// Run runs value.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/transactions", s.handleTransactions)
	mux.HandleFunc("GET /api/transactions/{id}", s.handleTransactionByID)
	mux.HandleFunc("GET /api/transactions/{id}/payment-details", s.handleTransactionPaymentDetails)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("POST /api/accounts/health_check", s.handleAccountHealthCheck)
	mux.HandleFunc("GET /api/lnd/wallet", s.handleLNDWalletStatus)
	mux.HandleFunc("GET /api/lnd/wallet/state", s.handleLNDWalletState)
	mux.HandleFunc("POST /api/lnd/wallet/init", s.handleLNDWalletInit)
	mux.HandleFunc("POST /api/lnd/addresses", s.handleLNDCreateAddress)
	mux.HandleFunc("POST /api/lnd/channels/open", s.handleLNDOpenChannel)
	mux.HandleFunc("POST /api/lnd/channels/close", s.handleLNDCloseChannel)
	mux.HandleFunc("GET /api/cex/deposit-addresses", s.handleCexDepositAddresses)
	mux.HandleFunc("GET /api/cex/withdrawal-addresses", s.handleCexWithdrawalAddresses)
	mux.HandleFunc("GET /api/cex/balances", s.handleCexBalances)
	mux.HandleFunc("GET /api/cex/hedge-positions", s.handleCexHedgePositions)
	mux.HandleFunc("GET /api/cex/spot-trades", s.handleCexSpotTrades)
	mux.HandleFunc("POST /api/cex/pre-trade-check", s.handleCexPreTradeCheck)
	mux.HandleFunc("POST /api/cex/tradeflow", s.handleCexTradeflow)
	mux.HandleFunc("GET /api/cex/withdrawal-track", s.handleCexWithdrawalTrack)
	mux.HandleFunc("POST /api/webhooks/dex-release", s.handleDexReleaseWebhook)
	mux.HandleFunc("POST /telegram-webhook", s.handleTelegramWebhook)
	mux.HandleFunc("GET /api/telegram/config", s.handleTelegramSettingsConfig)
	mux.HandleFunc("PUT /api/telegram/config", s.handleTelegramSettingsConfig)
	mux.HandleFunc("POST /api/telegram/set-webhook", s.handleTelegramSetWebhook)
	mux.HandleFunc("POST /api/telegram/test", s.handleTelegramTest)
	mux.HandleFunc("GET /api/bisq/payment-accounts", s.handleBisqPaymentAccounts)
	mux.HandleFunc("POST /api/bisq/payment-accounts", s.handleBisqCreatePaymentAccount)
	mux.HandleFunc("DELETE /api/bisq/payment-accounts/{id}", s.handleBisqDeletePaymentAccount)
	mux.HandleFunc("GET /ws/cex/hedge-stream", s.handleCexHedgeStream)

	var handler http.Handler = s.internalSecretMiddleware(mux)
	if s.metrics != nil {
		mux.Handle("GET /metrics", s.metrics.Handler())
		handler = s.metrics.Middleware(handler)
	}

	server := &http.Server{
		Addr:              s.addr,
		Handler:           corsMiddleware(handler),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("starting dexMmController API server on %s", s.addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("mm controller api server: %w", err)
		}
		return nil
	}
}

type accountHealthResponse struct {
	Provider  string                              `json:"provider"`
	Status    string                              `json:"status"`
	Healthy   bool                                `json:"healthy"`
	Message   string                              `json:"message"`
	Keys      []cexaccount.KeyPermissionResult `json:"keys,omitempty"`
	CheckedAt string                              `json:"checked_at"`
}

func (s *Server) handleAccountHealthCheck(w http.ResponseWriter, r *http.Request) {
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provider query parameter is required"))
		return
	}
	if lndhealth.IsLNDWalletProvider(provider) {
		if s.lndHealth == nil {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet health checker is not configured"))
			return
		}
		result, err := s.lndHealth.Check(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, accountHealthResponse{
			Provider:  result.Provider,
			Status:    result.Status,
			Healthy:   result.Healthy,
			Message:   result.Message,
			CheckedAt: result.CheckedAt.UTC().Format(time.RFC3339),
		})
		return
	}
	if s.accountHealth == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("account health checker is not configured"))
		return
	}
	if _, err := cexaccount.CanonicalProvider(provider); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported provider %q; supported providers: %s", provider, strings.Join(cexaccount.SupportedProviders(), ", ")))
		return
	}
	result, err := s.accountHealth.CheckProvider(r.Context(), provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, accountHealthResponse{
		Provider:  result.Provider,
		Status:    result.Status,
		Healthy:   result.Healthy,
		Message:   result.Message,
		Keys:      result.Keys,
		CheckedAt: result.CheckedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	transactionCount, err := s.repo.GetTransactionCount()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	incomplete, err := s.repo.GetIncompleteTransactions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	primaryWalletPresent := false
	if wallet, err := s.repo.GetPrimaryWalletAddress(); err == nil && wallet != nil && wallet.Address != "" {
		primaryWalletPresent = true
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status:               "ok",
		TransactionCount:     transactionCount,
		IncompleteCount:      len(incomplete),
		PrimaryWalletPresent: primaryWalletPresent,
	})
}

func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 100)
	if limit > 500 {
		limit = 500
	}
	offset := queryInt(r, "offset", 0)

	transactions, total, err := s.repo.ListTransactions(db.TransactionFilters{
		Limit:          limit,
		Offset:         offset,
		Status:         r.URL.Query().Get("status"),
		Direction:      r.URL.Query().Get("direction"),
		CEX:            r.URL.Query().Get("cex"),
		Dex:            r.URL.Query().Get("dex"),
		IncompleteOnly: queryBool(r, "incomplete_only"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, transactionListResponse{
		Transactions: transactions,
		Total:        total,
		Limit:        limit,
		Offset:       offset,
	})
}

func (s *Server) handleTransactionByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid transaction id"))
		return
	}

	transaction, err := s.repo.GetTransaction(uint(id))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "transaction not found") {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}

	writeJSON(w, http.StatusOK, transaction)
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.repo.GetPnlStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	incomplete, err := s.repo.GetIncompleteTransactions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	stats["incomplete_count"] = len(incomplete)
	writeJSON(w, http.StatusOK, stats)
}

// internalSecretExempt lists the routes that must stay reachable without the
// shared secret: health/metrics probes, and the two webhooks that authenticate
// their caller themselves (dex-release via Bearer token, Telegram via
// X-Telegram-Bot-Api-Secret-Token).
func internalSecretExempt(path string) bool {
	return path == "/health" ||
		path == "/metrics" ||
		path == "/api/webhooks/dex-release" ||
		path == "/telegram-webhook"
}

// internalSecretMiddleware rejects any non-exempt request whose
// X-Internal-Secret header does not match the configured secret. With an empty
// secret the gate is disabled (dev/tests only; main fails startup in that
// state unless ALLOW_INSECURE_INTERNAL=true).
func (s *Server) internalSecretMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.internalSecret == "" || internalSecretExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		provided := r.Header.Get("X-Internal-Secret")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.internalSecret)) != 1 {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid internal secret"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

func queryBool(r *http.Request, key string) bool {
	raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get(key)))
	return raw == "1" || raw == "true" || raw == "yes"
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("api response encode error: %v", err)
	}
}
