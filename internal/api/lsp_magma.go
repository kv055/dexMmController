package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dexMmController/internal/db"
	"dexMmController/internal/magma"
)

func (s *Server) WithMagmaLSP(coordinator *magma.Coordinator) *Server {
	s.magmaLSP = coordinator
	return s
}

func (s *Server) handleMagmaLSPInfo(w http.ResponseWriter, r *http.Request) {
	if s.magmaLSP == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("magma LSP integration is not configured"))
		return
	}
	pool := magma.Pool(r.URL.Query().Get("pool"))
	info, err := s.magmaLSP.GetInfo(r.Context(), pool)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleMagmaLSPOrders(w http.ResponseWriter, r *http.Request) {
	if s.magmaLSP == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("magma LSP integration is not configured"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows, err := s.magmaLSP.ListOrders(r.Context(), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, rows)
	case http.MethodPost:
		var req struct {
			Pool                string `json:"pool"`
			LSPBalanceSat       int64  `json:"lsp_balance_sat"`
			AnnounceChannel     bool   `json:"announce_channel"`
			ChannelExpiryBlocks int    `json:"channel_expiry_blocks"`
		}
		if err := decodeJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		record, err := s.magmaLSP.CreateOrder(r.Context(), magma.CreateParams{
			Pool:                magma.Pool(req.Pool),
			LSPBalanceSat:       req.LSPBalanceSat,
			AnnounceChannel:     req.AnnounceChannel,
			ChannelExpiryBlocks: req.ChannelExpiryBlocks,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusCreated, record)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMagmaLSPOrderByID(w http.ResponseWriter, r *http.Request) {
	if s.magmaLSP == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("magma LSP integration is not configured"))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("order id is required"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		record, err := s.magmaLSP.GetOrder(r.Context(), id)
		if errors.Is(err, db.ErrLSPMagmaOrderNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, record)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMagmaLSPOrderPay(w http.ResponseWriter, r *http.Request) {
	if s.magmaLSP == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("magma LSP integration is not configured"))
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("order id is required"))
		return
	}
	var req struct {
		FeeLimitSat int64 `json:"fee_limit_sat"`
	}
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	record, err := s.magmaLSP.PayOrder(r.Context(), id, req.FeeLimitSat)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}
