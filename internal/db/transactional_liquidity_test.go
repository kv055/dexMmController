package db

import (
	"context"
	"errors"
	"testing"
)

func TestTransactionalLiquiditySettingsRequireLightningLiquiditySource(t *testing.T) {
	amount := 100.0
	_, err := (&TransactionalLiquiditySettingsRepository{}).Update(context.Background(), TransactionalLiquiditySettingsUpdate{
		FiatCurrency:    "EUR",
		AmountSource:    TransactionalLiquidityCustomAmount,
		AmountFiat:      &amount,
		TradeMode:       "hedgefull_single_collateral",
		TargetDirection: TransactionalLiquidityTargetBid,
		FeeTier:         TransactionalLiquidityFeeEconomic,
	})

	var validation TransactionalLiquidityValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want validation error", err)
	}
	if validation.Message != "lightning_liquidity_source must be own_channels or lsp_channel" {
		t.Fatalf("validation message = %q", validation.Message)
	}
}
