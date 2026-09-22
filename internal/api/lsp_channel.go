package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dexMmController/internal/db"
	"dexMmController/internal/lsp"
)

type lspChannelConfigBackend interface {
	Get(context.Context) (lsp.Config, error)
	Update(context.Context, lsp.Config) (lsp.Config, error)
}

// lspChannelCandidate is populated from LND's current channel list, allowing
// the UI to select a channel without typing identifiers by hand.
type lspChannelCandidate struct {
	ChannelPoint string `json:"channel_point"`
	RemotePubkey string `json:"remote_pubkey"`
	RemoteAlias  string `json:"remote_alias,omitempty"`
	Active       bool   `json:"active"`
	CapacitySat  int64  `json:"capacity_sat"`
}

// WithLSPChannelConfig enables manual LSP channel configuration endpoints.
func (s *Server) WithLSPChannelConfig(repo lspChannelConfigBackend) *Server {
	s.lspChannelConfig = repo
	return s
}

func (s *Server) handleLSPChannelConfig(w http.ResponseWriter, r *http.Request) {
	if s.lspChannelConfig == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LSP channel configuration is not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		cfg, err := s.lspChannelConfig.Get(r.Context())
		if errors.Is(err, db.ErrLSPChannelConfigNotFound) {
			writeJSON(w, http.StatusOK, lsp.Config{})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPut:
		var cfg lsp.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
			return
		}
		if cfg.Enabled {
			client := s.currentLNDWallet()
			if client == nil {
				writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet client is not configured; select an LSP channel from the live channel list"))
				return
			}
			channels, listErr := client.ListChannels(r.Context())
			if listErr != nil {
				writeError(w, http.StatusBadGateway, fmt.Errorf("list LND channels: %w", listErr))
				return
			}
			found := false
			for _, ch := range channels {
				if strings.EqualFold(strings.TrimSpace(ch.ChannelPoint), strings.TrimSpace(cfg.ChannelPoint)) && strings.EqualFold(strings.TrimSpace(ch.RemotePubkey), strings.TrimSpace(cfg.RemotePubkey)) {
					found = true
					break
				}
			}
			if !found {
				writeError(w, http.StatusBadRequest, fmt.Errorf("selected LSP channel must match a live LND channel point and remote pubkey"))
				return
			}
		}
		updated, err := s.lspChannelConfig.Update(r.Context(), cfg)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLSPChannelCandidates(w http.ResponseWriter, r *http.Request) {
	client := s.currentLNDWallet()
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LND wallet client is not configured"))
		return
	}
	channels, err := client.ListChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("list LND channels: %w", err))
		return
	}
	candidates := make([]lspChannelCandidate, 0, len(channels))
	for _, ch := range channels {
		capacity, _ := strconv.ParseInt(strings.TrimSpace(ch.Capacity), 10, 64)
		alias := ""
		if node, nodeErr := client.GraphNode(r.Context(), ch.RemotePubkey); nodeErr == nil && node != nil {
			alias = node.Node.Alias
		}
		candidates = append(candidates, lspChannelCandidate{ChannelPoint: ch.ChannelPoint, RemotePubkey: ch.RemotePubkey, RemoteAlias: alias, Active: ch.Active, CapacitySat: capacity})
	}
	writeJSON(w, http.StatusOK, candidates)
}

func (s *Server) handleLSPChannelStatus(w http.ResponseWriter, r *http.Request) {
	if s.lspChannelConfig == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("LSP channel configuration is not configured"))
		return
	}
	cfg, err := s.lspChannelConfig.Get(r.Context())
	if errors.Is(err, db.ErrLSPChannelConfigNotFound) {
		writeJSON(w, http.StatusOK, lsp.Inspect(lsp.Config{}, nil))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	client := s.currentLNDWallet()
	if client == nil {
		status := lsp.Inspect(cfg, nil)
		status.Reason = "LND wallet client is not configured"
		writeJSON(w, http.StatusOK, status)
		return
	}
	channels, err := client.ListChannels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("list LND channels: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, lsp.Inspect(cfg, channels))
}
