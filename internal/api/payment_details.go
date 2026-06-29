package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// paymentDetailsResponse is the focused view of a transaction's stored counterparty fiat
// details. The full payload is also present on GET /api/transactions/{id}; this endpoint exists
// so an operator (or the fiat-controller) can poll just the payment info for the fiat leg.
type paymentDetailsResponse struct {
	TransactionID uint            `json:"transaction_id"`
	Available     bool            `json:"available"`
	PaymentInfo   json.RawMessage `json:"counterparty_payment_info,omitempty"`
}

// handleTransactionPaymentDetails returns the counterparty fiat details captured for a
// transaction (Bisq: from the trade contract; Robosats: from the trade chat). Available is
// false until the offer is taken and the details have been fetched.
func (s *Server) handleTransactionPaymentDetails(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid transaction id"))
		return
	}

	tx, err := s.repo.GetTransaction(uint(id))
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "transaction not found") {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}

	writeJSON(w, http.StatusOK, paymentDetailsResponse{
		TransactionID: tx.ID,
		Available:     len(tx.CounterpartyPaymentInfo) > 0,
		PaymentInfo:   tx.CounterpartyPaymentInfo,
	})
}
