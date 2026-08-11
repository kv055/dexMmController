package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeDexPendingLiquidityRepo struct {
	btcValue float64
	count    int64
	err      error
}

func (f fakeDexPendingLiquidityRepo) GetDexPendingLiquidity() (float64, int64, error) {
	return f.btcValue, f.count, f.err
}

func TestHandleDexPendingLiquidity(t *testing.T) {
	server := &Server{dexPendingLiquidityRepo: fakeDexPendingLiquidityRepo{btcValue: 0.125, count: 3}}
	recorder := httptest.NewRecorder()
	server.handleDexPendingLiquidity(recorder, httptest.NewRequest(http.MethodGet, "/api/transactions/dex-pending-liquidity", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{`"btc_value":0.125`, `"transaction_count":3`, `"DexPositionTaken"`, `"DexFiatHasArrived"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("response %q does not contain %q", body, expected)
		}
	}
}

func TestHandleDexPendingLiquidityRepositoryError(t *testing.T) {
	server := &Server{dexPendingLiquidityRepo: fakeDexPendingLiquidityRepo{err: errors.New("database unavailable")}}
	recorder := httptest.NewRecorder()
	server.handleDexPendingLiquidity(recorder, httptest.NewRequest(http.MethodGet, "/api/transactions/dex-pending-liquidity", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}
