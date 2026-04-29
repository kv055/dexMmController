package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dexMmController/internal/accounthealth"
	"dexMmController/internal/db"
)

// Server represents a server.
type Server struct {
	addr          string
	repo          *db.TransactionRepository
	accountHealth accountHealthChecker
}

type accountHealthChecker interface {
	CheckProvider(ctx context.Context, provider string) (accounthealth.Result, error)
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

// Run runs value.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/transactions", s.handleTransactions)
	mux.HandleFunc("GET /api/transactions/{id}", s.handleTransactionByID)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("POST /api/accounts/health_check", s.handleAccountHealthCheck)

	server := &http.Server{
		Addr:              s.addr,
		Handler:           corsMiddleware(mux),
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
	Provider  string `json:"provider"`
	Status    string `json:"status"`
	Healthy   bool   `json:"healthy"`
	Message   string `json:"message"`
	CheckedAt string `json:"checked_at"`
}

func (s *Server) handleAccountHealthCheck(w http.ResponseWriter, r *http.Request) {
	if s.accountHealth == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("account health checker is not configured"))
		return
	}
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("provider query parameter is required"))
		return
	}
	if _, err := accounthealth.CanonicalProvider(provider); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported provider %q; supported providers: %s", provider, strings.Join(accounthealth.SupportedProviders(), ", ")))
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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
