package api

import (
	"net/http"

	"dexTakerController/internal/db"
)

type dexPendingLiquidityResponse struct {
	BTCValue         float64                `json:"btc_value"`
	TransactionCount int64                  `json:"transaction_count"`
	Statuses         []db.TransactionStatus `json:"statuses"`
}

func (s *Server) handleDexPendingLiquidity(w http.ResponseWriter, r *http.Request) {
	repo := s.dexPendingLiquidityRepo
	if repo == nil {
		repo = s.currentRepo()
	}
	btcValue, transactionCount, err := repo.GetDexPendingLiquidity()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, dexPendingLiquidityResponse{
		BTCValue:         btcValue,
		TransactionCount: transactionCount,
		Statuses:         []db.TransactionStatus{db.StatusDexPositionTaken, db.StatusDexFiatHasArrived},
	})
}
