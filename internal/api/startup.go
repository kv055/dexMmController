package api

import (
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// StartupState is the small, safe-to-publish view of controller bootstrap.
// It deliberately contains no connection strings, credentials, or raw errors.
type StartupState struct {
	mu            sync.RWMutex
	databaseReady bool
	lndReady      bool
	phase         string
	lastError     string

	databaseGauge prometheus.Gauge
	lndGauge      prometheus.Gauge
	failures      prometheus.Counter
}

type StartupSnapshot struct {
	DatabaseReady bool   `json:"database_ready"`
	LNDReady      bool   `json:"lnd_ready"`
	Phase         string `json:"phase"`
	LastError     string `json:"last_error,omitempty"`
}

func NewStartupState() *StartupState {
	return &StartupState{phase: "starting"}
}

// RegisterMetrics adds bootstrap instruments to the controller's existing registry.
func (s *StartupState) RegisterMetrics(reg prometheus.Registerer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.databaseGauge != nil {
		return
	}
	s.databaseGauge = prometheus.NewGauge(prometheus.GaugeOpts{Name: "dex_mm_startup_database_ready", Help: "1 when the controller database repository is installed."})
	s.lndGauge = prometheus.NewGauge(prometheus.GaugeOpts{Name: "dex_mm_startup_lnd_ready", Help: "1 when the LND wallet client and backup setup are complete."})
	s.failures = prometheus.NewCounter(prometheus.CounterOpts{Name: "dex_mm_startup_failures_total", Help: "Dependency bootstrap failures by dependency.", ConstLabels: nil})
	reg.MustRegister(s.databaseGauge, s.lndGauge, s.failures)
}

func (s *StartupState) SetDatabaseReady(ready bool, phase string, err error) {
	s.set(&ready, nil, phase, err)
}
func (s *StartupState) SetLNDReady(ready bool, phase string, err error) {
	s.set(nil, &ready, phase, err)
}

func (s *StartupState) set(databaseReady, lndReady *bool, phase string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if databaseReady != nil {
		s.databaseReady = *databaseReady
		if s.databaseGauge != nil {
			s.databaseGauge.Set(boolFloat(*databaseReady))
		}
	}
	if lndReady != nil {
		s.lndReady = *lndReady
		if s.lndGauge != nil {
			s.lndGauge.Set(boolFloat(*lndReady))
		}
	}
	if strings.TrimSpace(phase) != "" {
		s.phase = phase
	}
	if err != nil {
		s.lastError = sanitizeStartupError(err.Error())
		if s.failures != nil {
			s.failures.Inc()
		}
	} else if (databaseReady != nil && *databaseReady) || (lndReady != nil && *lndReady) {
		s.lastError = ""
	}
}

func (s *StartupState) Snapshot() StartupSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StartupSnapshot{DatabaseReady: s.databaseReady, LNDReady: s.lndReady, Phase: s.phase, LastError: s.lastError}
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func sanitizeStartupError(message string) string {
	message = strings.TrimSpace(message)
	// PostgreSQL URLs and LND RPC errors can include credentials. Keep the useful
	// diagnostic category while never publishing a URL-like value.
	if i := strings.Index(message, "://"); i >= 0 {
		return "dependency connection failed"
	}
	if len(message) > 300 {
		return message[:300]
	}
	return message
}
