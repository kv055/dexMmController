package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"dexMmController/internal/swap"
)

type submarineSwapHistory interface {
	List(context.Context, int) ([]swap.Record, error)
}

func (s *Server) WithSubmarineSwapHistory(repo submarineSwapHistory) *Server {
	s.submarineSwapHistory = repo
	return s
}
func (s *Server) handleSubmarineSwapHistory(w http.ResponseWriter, r *http.Request) {
	if s.submarineSwapHistory == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("submarine swap history is not configured"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.submarineSwapHistory.List(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
