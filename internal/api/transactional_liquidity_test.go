package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dexMmController/internal/db"
)

type fakeTransactionalLiquiditySettingsRepo struct {
	row     db.TransactionalLiquiditySettings
	updated db.TransactionalLiquiditySettings
	update  db.TransactionalLiquiditySettingsUpdate
	err     error
}

func (f *fakeTransactionalLiquiditySettingsRepo) Get(context.Context) (db.TransactionalLiquiditySettings, error) {
	return f.row, f.err
}

func (f *fakeTransactionalLiquiditySettingsRepo) Update(_ context.Context, update db.TransactionalLiquiditySettingsUpdate) (db.TransactionalLiquiditySettings, error) {
	f.update = update
	return f.updated, f.err
}

func TestTransactionalLiquiditySettingsPutForwardsTargetDirection(t *testing.T) {
	repo := &fakeTransactionalLiquiditySettingsRepo{updated: db.TransactionalLiquiditySettings{TargetDirection: db.TransactionalLiquidityTargetAsk}}
	server := (&Server{}).WithTransactionalLiquiditySettings(repo)
	req := httptest.NewRequest(http.MethodPut, "/api/transactional-liquidity", strings.NewReader(`{
		"fiat_currency":"EUR",
		"amount_source":"custom",
		"custom_amount":500,
		"trade_mode":"hedgefull_single_collateral",
		"target_direction":"ask"
	}`))
	recorder := httptest.NewRecorder()

	server.handleTransactionalLiquiditySettings(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if repo.update.TargetDirection != db.TransactionalLiquidityTargetAsk {
		t.Fatalf("target direction = %q, want %q", repo.update.TargetDirection, db.TransactionalLiquidityTargetAsk)
	}
}
