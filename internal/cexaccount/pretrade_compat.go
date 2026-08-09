package cexaccount

// Compatibility aliases for callers that still import pre-trade types through
// cexaccount. New code should import dexMmController/internal/pretrade.

import (
	"encoding/json"

	"dexMmController/internal/pretrade"
)

type PreTradeParams = pretrade.Params
type CheckResult = pretrade.CheckResult
type Report = pretrade.Report
type BidChecker = pretrade.BidChecker

type FiatControllerChecker = pretrade.FiatControllerChecker
type FiatControllerHealthChecker = pretrade.FiatControllerHealthChecker
type BalanceFetcher = pretrade.BalanceFetcher
type WhitelistFetcher = pretrade.WhitelistFetcher
type WalletSource = pretrade.WalletSource
type BTCBalanceSource = pretrade.BTCBalanceSource
type LightningBalanceSource = pretrade.LightningBalanceSource
type LightningChannelSource = pretrade.LightningChannelSource
type LNDOnlineSource = pretrade.LNDOnlineSource
type CommunicationGateway = pretrade.CommunicationGateway
type PermissionChecker = pretrade.PermissionChecker
type PaymentSetupFetcher = pretrade.PaymentSetupFetcher
type ChatTemplateFetcher = pretrade.ChatTemplateFetcher

type BTCBalanceSourceFunc = pretrade.BTCBalanceSourceFunc
type LightningBalanceSourceFuncs = pretrade.LightningBalanceSourceFuncs
type LightningChannelSourceFunc = pretrade.LightningChannelSourceFunc
type LNDOnlineSourceFunc = pretrade.LNDOnlineSourceFunc
type BalanceFetcherFunc = pretrade.BalanceFetcherFunc
type WhitelistFetcherFunc = pretrade.WhitelistFetcherFunc
type WalletSourceFunc = pretrade.WalletSourceFunc
type PermissionCheckerFunc = pretrade.PermissionCheckerFunc
type PaymentSetupFetcherFuncs = pretrade.PaymentSetupFetcherFuncs

type PaymentMethodSnapshot = pretrade.PaymentMethodSnapshot
type FiatAccountSnapshot = pretrade.FiatAccountSnapshot
type BankAccountSnapshot = pretrade.BankAccountSnapshot

const (
	TradeModeHedgefullSingleCollateral = pretrade.TradeModeHedgefullSingleCollateral
	TradeModeHedgelessDoubleCollateral = pretrade.TradeModeHedgelessDoubleCollateral
)

func NormalizeTradeMode(raw string) string      { return pretrade.NormalizeTradeMode(raw) }
func ParseTradeMode(raw string) (string, error) { return pretrade.ParseTradeMode(raw) }
func ProviderForHedgeVenue(venue string) (string, error) {
	return pretrade.ProviderForHedgeVenue(venue)
}

func NewPreTradeChecker(balance BalanceFetcher, whitelist WhitelistFetcher, permissions PermissionChecker, paymentSetup PaymentSetupFetcher, chatTemplates ChatTemplateFetcher) *BidChecker {
	return pretrade.NewPreTradeChecker(balance, whitelist, permissions, paymentSetup, chatTemplates)
}

func NewBidChecker(balance BalanceFetcher, whitelist WhitelistFetcher, permissions PermissionChecker, paymentSetup PaymentSetupFetcher, chatTemplates ChatTemplateFetcher) *BidChecker {
	return NewPreTradeChecker(balance, whitelist, permissions, paymentSetup, chatTemplates)
}

func ParseBankAccountDetails(raw json.RawMessage) map[string]string {
	return pretrade.ParseBankAccountDetails(raw)
}
