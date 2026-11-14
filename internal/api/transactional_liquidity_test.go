package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dexTakerController/internal/db"
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
		"amount_fiat":500,
		"trade_mode":"hedgefull_single_collateral",
		"target_direction":"ask",
		"lightning_liquidity_source":"lsp_channel",
		"supported_routing_profiles":["Direct_main2main","Hub_main2main"],
		"fee_tier":"economic",
		"coupled_hedge":true
	}`))
	recorder := httptest.NewRecorder()

	server.handleTransactionalLiquiditySettings(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if repo.update.TargetDirection != db.TransactionalLiquidityTargetAsk {
		t.Fatalf("target direction = %q, want %q", repo.update.TargetDirection, db.TransactionalLiquidityTargetAsk)
	}
	if repo.update.LightningLiquiditySource != db.TransactionalLiquidityLSPChannel {
		t.Fatalf("lightning liquidity source = %q, want %q", repo.update.LightningLiquiditySource, db.TransactionalLiquidityLSPChannel)
	}
	if !stringSlicesEqual(repo.update.SupportedRoutingProfiles, []string{db.TransactionalRoutingProfileDirect, db.TransactionalRoutingProfileHub}) {
		t.Fatalf("supported routing profiles = %#v", repo.update.SupportedRoutingProfiles)
	}
	if repo.update.AmountFiat == nil || *repo.update.AmountFiat != 500 || repo.update.FeeTier != db.TransactionalLiquidityFeeEconomic || !repo.update.CoupledHedge {
		t.Fatalf("routing settings were not forwarded: %+v", repo.update)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
