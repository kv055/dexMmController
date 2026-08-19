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

func TestTransactionalLiquiditySettingsRejectEmptyRoutingProfiles(t *testing.T) {
	amount := 100.0
	_, err := (&TransactionalLiquiditySettingsRepository{}).Update(context.Background(), TransactionalLiquiditySettingsUpdate{
		FiatCurrency:             "EUR",
		AmountSource:             TransactionalLiquidityCustomAmount,
		AmountFiat:               &amount,
		TradeMode:                "hedgefull_single_collateral",
		TargetDirection:          TransactionalLiquidityTargetBid,
		LightningLiquiditySource: TransactionalLiquidityOwnChannels,
		SupportedRoutingProfiles: []string{},
		FeeTier:                  TransactionalLiquidityFeeEconomic,
	})

	var validation TransactionalLiquidityValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want validation error", err)
	}
	if validation.Message != "supported_routing_profiles must not be empty" {
		t.Fatalf("validation message = %q", validation.Message)
	}
}

func TestTransactionalLiquiditySettingsRejectUnknownRoutingProfiles(t *testing.T) {
	amount := 100.0
	_, err := (&TransactionalLiquiditySettingsRepository{}).Update(context.Background(), TransactionalLiquiditySettingsUpdate{
		FiatCurrency:             "EUR",
		AmountSource:             TransactionalLiquidityCustomAmount,
		AmountFiat:               &amount,
		TradeMode:                "hedgefull_single_collateral",
		TargetDirection:          TransactionalLiquidityTargetBid,
		LightningLiquiditySource: TransactionalLiquidityOwnChannels,
		SupportedRoutingProfiles: []string{TransactionalRoutingProfileDirect, "bogus_profile"},
		FeeTier:                  TransactionalLiquidityFeeEconomic,
	})

	var validation TransactionalLiquidityValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want validation error", err)
	}
	if validation.Message != "supported_routing_profiles contains an unsupported profile" {
		t.Fatalf("validation message = %q", validation.Message)
	}
}

func TestNormalizeSupportedRoutingProfilesPersistsCanonicalSubset(t *testing.T) {
	got, err := normalizeSupportedRoutingProfiles([]string{
		TransactionalRoutingProfileSwap,
		"  " + TransactionalRoutingProfileDirect,
		TransactionalRoutingProfileSwap,
	})
	if err != nil {
		t.Fatalf("normalize subset: %v", err)
	}
	want := []string{TransactionalRoutingProfileDirect, TransactionalRoutingProfileSwap}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("profiles = %#v, want %#v", got, want)
	}
}

func TestNormalizeSupportedRoutingProfilesDefaultsNilToAll(t *testing.T) {
	got, err := normalizeSupportedRoutingProfiles(nil)
	if err != nil {
		t.Fatalf("normalize nil: %v", err)
	}
	if !stringSlicesEqual(got, defaultSupportedRoutingProfiles) {
		t.Fatalf("profiles = %#v, want %#v", got, defaultSupportedRoutingProfiles)
	}
}
