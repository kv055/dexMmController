package tradeflow

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"commons/mmcapabilities"
	"dexTakerController/internal/cexaccount"
)

type fakeCapabilityStore map[string]mmcapabilities.ProviderState

func (f fakeCapabilityStore) State(_ context.Context, provider string) (mmcapabilities.ProviderState, error) {
	key := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(provider)), "_futures")
	state, ok := f[key]
	if !ok {
		return mmcapabilities.ProviderState{}, fmt.Errorf("missing provider %s", provider)
	}
	return state, nil
}

func capabilityState(provider string, spot, hedge bool) mmcapabilities.ProviderState {
	return mmcapabilities.ProviderState{
		Provider: provider,
		Exchange: strings.ToLower(provider),
		Spot:     mmcapabilities.LegState{EffectiveEnabled: spot},
		Hedge:    mmcapabilities.LegState{EffectiveEnabled: hedge},
	}
}

func TestRunRejectsPolicyDisabledSpotBeforeExecution(t *testing.T) {
	svc := &fakeService{}
	tf := New(svc, nil).WithCapabilities(fakeCapabilityStore{
		"binance": capabilityState("Binance", false, true),
	})
	_, err := tf.Run(context.Background(), validOrder())
	if err == nil || !strings.Contains(err.Error(), "spot capability is disabled") {
		t.Fatalf("Run error = %v", err)
	}
	if svc.preTradeCallCount+svc.spotCallCount+svc.hedgeCallCount+svc.withdrawCallCount != 0 {
		t.Fatal("policy must fail before any exchange service call")
	}
}

func TestRunRequiresSelectedDecoupledHedgeProvider(t *testing.T) {
	svc := &fakeService{}
	order := validOrder()
	order.HedgeVenue = "coinbase_futures"
	tf := New(svc, nil).WithCapabilities(fakeCapabilityStore{
		"binance":  capabilityState("Binance", true, true),
		"coinbase": capabilityState("Coinbase", true, false),
	})
	_, err := tf.Run(context.Background(), order)
	if err == nil || !strings.Contains(err.Error(), "Coinbase hedge capability is disabled") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestHedgelessRunDoesNotRequireHedgeCapability(t *testing.T) {
	order := validOrder()
	order.TradeMode = cexaccount.TradeModeHedgelessDoubleCollateral
	store := fakeCapabilityStore{"binance": capabilityState("Binance", true, false)}
	tf := New(&fakeService{}, nil).WithCapabilities(store)
	if err := tf.requireCapabilities(context.Background(), order); err != nil {
		t.Fatalf("hedgeless capability check = %v", err)
	}
}
