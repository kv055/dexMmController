package cexaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"commons/paymentmethods"
	"dexMmController/internal/db"
)

// Trade mode identifiers (aligned with commons/tradecost).
const (
	TradeModeHedgefullSingleCollateral = "hedgefull_single_collateral"
	TradeModeHedgelessDoubleCollateral = "hedgeless_double_collateral"
)

// PreTradeParams carries every input for bid-side pre-trade checks.
type PreTradeParams struct {
	Provider     string
	FiatCurrency string
	// RequiredCapital is the fiat amount to check on the exchange, in FiatCurrency units.
	RequiredCapital float64
	PaymentMethod   string
	PaymentMethods  []string
	Dex             string
	// TradeMode selects permission / capital semantics. Empty defaults to
	// hedgefull_single_collateral.
	TradeMode string
	// HedgeVenue is the futures venue for hedgefull mode (e.g. coinbase_futures).
	// When it maps to a different exchange than Provider, hedge permissions are
	// checked on that exchange separately (decoupled hedge).
	HedgeVenue string
	// RequiredBTCLiquidity is position_btc + security_deposit_btc. Required for
	// the hedgeless liquidity-pool wallet check; ignored in hedgefull mode.
	RequiredBTCLiquidity float64
}

// BidChecker runs all pre-trade checks for the bid side (buying BTC on a CEX).
type BidChecker struct {
	balance          BalanceFetcher
	whitelist        WhitelistFetcher
	permissions      PermissionChecker
	paymentSetup     PaymentSetupFetcher
	chatTemplates    ChatTemplateFetcher
	communication    CommunicationGateway
	fiatController   FiatControllerChecker
	walletMu         sync.RWMutex
	wallet           WalletSource
	btcBalance       BTCBalanceSource
	lightningBalance LightningBalanceSource
	lndOnline        LNDOnlineSource
}

// NormalizeTradeMode returns a canonical trade mode, defaulting empty to hedgefull.
func NormalizeTradeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case TradeModeHedgelessDoubleCollateral, "hedgeless":
		return TradeModeHedgelessDoubleCollateral
	default:
		return TradeModeHedgefullSingleCollateral
	}
}

// ParseTradeMode validates an API-supplied mode while retaining the legacy
// empty-default behavior.
func ParseTradeMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", TradeModeHedgefullSingleCollateral, "hedgefull":
		return TradeModeHedgefullSingleCollateral, nil
	case TradeModeHedgelessDoubleCollateral, "hedgeless":
		return TradeModeHedgelessDoubleCollateral, nil
	default:
		return "", fmt.Errorf("unsupported trade_mode %q", raw)
	}
}

// ProviderForHedgeVenue maps a futures venue id to its spot/account provider slug.
func ProviderForHedgeVenue(venue string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(venue)) {
	case "kraken_futures", "kraken":
		return "kraken", nil
	case "coinbase_futures", "coinbase":
		return "coinbase", nil
	case "binance_futures", "binance":
		return "binance", nil
	case "":
		return "", fmt.Errorf("hedge venue is required")
	default:
		return "", fmt.Errorf("unknown hedge venue %q", venue)
	}
}

// NewBidChecker creates a BidChecker. The wallet source is set lazily via SetWallet
// because the LND client initialises asynchronously after startup.
func NewBidChecker(balance BalanceFetcher, whitelist WhitelistFetcher, permissions PermissionChecker, paymentSetup PaymentSetupFetcher, chatTemplates ChatTemplateFetcher) *BidChecker {
	return &BidChecker{
		balance:       balance,
		whitelist:     whitelist,
		permissions:   permissions,
		paymentSetup:  paymentSetup,
		chatTemplates: chatTemplates,
	}
}

// SetWallet injects (or replaces) the wallet source. Safe to call concurrently.
func (c *BidChecker) SetWallet(ws WalletSource) {
	c.walletMu.Lock()
	c.wallet = ws
	c.walletMu.Unlock()
}

// SetBTCBalance injects (or replaces) the LND on-chain BTC balance source used
// by the hedgeless liquidity-pool check. Safe to call concurrently.
func (c *BidChecker) SetBTCBalance(src BTCBalanceSource) {
	c.walletMu.Lock()
	c.btcBalance = src
	c.walletMu.Unlock()
}

// SetLightningBalance injects spendable local channel liquidity for RoboSats.
func (c *BidChecker) SetLightningBalance(src LightningBalanceSource) {
	c.walletMu.Lock()
	c.lightningBalance = src
	c.walletMu.Unlock()
}

// SetLNDOnline injects (or replaces) the LND reachability check used by the
// pre-trade "LND wallet online" gate. Safe to call concurrently.
func (c *BidChecker) SetLNDOnline(src LNDOnlineSource) {
	c.walletMu.Lock()
	c.lndOnline = src
	c.walletMu.Unlock()
}

// SetCommunicationGateway injects the operator communication channel state.
// Safe to call with hot-reloadable stores such as telegram.Store.
func (c *BidChecker) SetCommunicationGateway(gateway CommunicationGateway) {
	c.communication = gateway
}

// SetFiatControllerChecker injects the fiat-arrival safety check.
func (c *BidChecker) SetFiatControllerChecker(checker FiatControllerChecker) {
	c.fiatController = checker
}

// CheckResult is the outcome of a single named pre-trade check.
type CheckResult struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Report is the full result of running every pre-trade check. OK is true only when
// every check passed.
type Report struct {
	OK                 bool          `json:"ok"`
	Checks             []CheckResult `json:"checks"`
	LNDWithdrawAddress string        `json:"lnd_withdraw_address,omitempty"`
	// ResolvedPaymentMethod is the catalog method the payment check validated —
	// either the explicitly requested one or the highest-priority linked+active
	// method among the offer's accepted list.
	ResolvedPaymentMethod string `json:"resolved_payment_method,omitempty"`
}

// Run executes every pre-trade check to completion — it never short-circuits on the
// first failure — and returns a per-check Report.
//
// Checks are ordered by logical dependency groups so the UI can render them
// coherently:
//  1. Capital   — exchange fiat (+ BTC liquidity pool in hedgeless)
//  2. Exchange  — API permissions, LND online, withdrawal whitelist
//  3. Payment   — account setup, message template
//  4. Ops       — communication gateway, Fiat Controller service + channels
//
// PaymentMethod names the single method to validate; when it is empty and
// PaymentMethods (the offer's accepted list) is non-empty, the linked+active
// catalog row with the highest priority is picked. Dex is the DEX venue slug
// (e.g. "bisq2", "robosats") the offer belongs to — used only to scope the
// message-template check to chat-style venues.
func (c *BidChecker) Run(ctx context.Context, p PreTradeParams) Report {
	provider := p.Provider
	tradeMode := NormalizeTradeMode(p.TradeMode)
	isHedgeless := tradeMode == TradeModeHedgelessDoubleCollateral

	type namedCheck struct {
		name, label string
		run         func() error
	}
	appendResult := func(report *Report, chk namedCheck) {
		res := CheckResult{Name: chk.name, Label: chk.label, Passed: true}
		if err := chk.run(); err != nil {
			res.Passed = false
			res.Message = err.Error()
			report.OK = false
		}
		report.Checks = append(report.Checks, res)
	}

	report := Report{OK: true, Checks: make([]CheckResult, 0, 10)}

	// --- Capital ---
	capitalLabel := "Exchange capital"
	if isHedgeless {
		capitalLabel = "Exchange fiat"
	}
	appendResult(&report, namedCheck{"capital", capitalLabel, func() error {
		return c.checkCapital(ctx, provider, p.FiatCurrency, p.RequiredCapital, tradeMode)
	}})
	if isHedgeless {
		appendResult(&report, namedCheck{"liquidity_pool", "BTC liquidity pool", func() error {
			return c.checkLiquidityPool(ctx, p.Dex, p.RequiredBTCLiquidity)
		}})
	}

	// --- Exchange ---
	appendResult(&report, namedCheck{"permissions", "API key permissions", func() error {
		return c.checkPermissions(ctx, provider, tradeMode, p.HedgeVenue)
	}})
	appendResult(&report, namedCheck{"lnd_wallet", "LND wallet online", func() error {
		return c.checkLNDOnline(ctx)
	}})

	whitelistRes := CheckResult{Name: "whitelist", Label: "Withdrawal whitelist", Passed: true}
	lndAddr, err := c.checkWhitelist(ctx, provider)
	if err != nil {
		whitelistRes.Passed = false
		whitelistRes.Message = err.Error()
		report.OK = false
	} else {
		report.LNDWithdrawAddress = lndAddr
	}
	report.Checks = append(report.Checks, whitelistRes)

	// --- Payment ---
	paymentRes := CheckResult{Name: "payment_setup", Label: "Payment account configured", Passed: true}
	resolved, err := c.resolvePaymentMethod(ctx, p.PaymentMethod, p.PaymentMethods)
	if err == nil {
		report.ResolvedPaymentMethod = resolved
		err = c.checkPaymentSetup(ctx, resolved)
	}
	if err != nil {
		paymentRes.Passed = false
		paymentRes.Message = err.Error()
		report.OK = false
	}
	report.Checks = append(report.Checks, paymentRes)

	templateRes := CheckResult{Name: "message_template", Label: "Payment message template", Passed: true}
	if err := c.checkMessageTemplate(ctx, p.Dex, resolved); err != nil {
		templateRes.Passed = false
		templateRes.Message = err.Error()
		report.OK = false
	}
	report.Checks = append(report.Checks, templateRes)

	// --- Ops ---
	appendResult(&report, namedCheck{"communication_gateway", "Communication gateway", func() error {
		return c.checkCommunicationGateway(ctx)
	}})

	serviceRes := CheckResult{Name: "fiat_controller_service", Label: "Fiat Controller service", Passed: true}
	if err := c.checkFiatControllerService(ctx); err != nil {
		serviceRes.Passed = false
		serviceRes.Message = err.Error()
		report.OK = false
	}
	report.Checks = append(report.Checks, serviceRes)

	notificationRes := CheckResult{Name: "fiat_controller", Label: "Payment notification channels", Passed: true}
	if err := c.checkFiatControllerNotifications(ctx, resolved); err != nil {
		notificationRes.Passed = false
		notificationRes.Message = err.Error()
		report.OK = false
	}
	report.Checks = append(report.Checks, notificationRes)

	return report
}

// RunAll runs every check and returns an aggregated error listing all failures,
// or nil when all checks pass.
func (c *BidChecker) RunAll(ctx context.Context, p PreTradeParams) error {
	report := c.Run(ctx, p)
	if report.OK {
		return nil
	}
	msgs := make([]string, 0, len(report.Checks))
	for _, chk := range report.Checks {
		if !chk.Passed {
			msgs = append(msgs, chk.Message)
		}
	}
	return fmt.Errorf("pre-trade checks failed: %s", strings.Join(msgs, "; "))
}

func (c *BidChecker) loadPermissions(ctx context.Context, provider string) (map[string]string, error) {
	perms, err := c.permissions.CEXPermissions(ctx, provider)
	if err != nil {
		return nil, fmt.Errorf("reading %s API key permissions for pre-trade check: %w", provider, err)
	}
	if len(perms) == 0 {
		return nil, fmt.Errorf("no permission data available for %s; run the account health check first", provider)
	}
	return perms, nil
}

// checkPermissions validates spot permissions on the spot provider. For hedgefull
// mode it also requires hedge_positions on the hedge venue's exchange (same
// account when coupled, a second account when decoupled). Hedgeless mode skips
// the hedge permission requirement entirely.
func (c *BidChecker) checkPermissions(ctx context.Context, provider, tradeMode, hedgeVenue string) error {
	spotPerms, err := c.loadPermissions(ctx, provider)
	if err != nil {
		return err
	}
	if spotPerms[PermSpotTrading] != PermGranted {
		return fmt.Errorf(
			"pre-trade check failed: %s API key does not have spot trading permission (status: %q); check account health",
			provider, spotPerms[PermSpotTrading],
		)
	}

	if tradeMode == TradeModeHedgelessDoubleCollateral {
		return nil
	}

	hedgeProvider := provider
	if strings.TrimSpace(hedgeVenue) != "" {
		hp, err := ProviderForHedgeVenue(hedgeVenue)
		if err != nil {
			return fmt.Errorf("pre-trade check failed: %v", err)
		}
		hedgeProvider = hp
	}

	hedgePerms := spotPerms
	if !strings.EqualFold(hedgeProvider, provider) {
		hedgePerms, err = c.loadPermissions(ctx, hedgeProvider)
		if err != nil {
			return err
		}
	}

	if hedgeStatus := hedgePerms[PermHedgePositions]; hedgeStatus != PermGranted {
		return fmt.Errorf(
			"pre-trade check failed: %s API key does not have hedge position permission (status: %q); check account health",
			hedgeProvider, hedgeStatus,
		)
	}
	return nil
}

func (c *BidChecker) checkCapital(ctx context.Context, provider, currency string, required float64, tradeMode string) error {
	available, err := c.balance.FiatAvailable(ctx, provider, currency)
	if err != nil {
		return fmt.Errorf("checking account balance before market buy: %w", err)
	}
	fiat := strings.ToUpper(strings.TrimSpace(currency))
	if fiat == "" {
		fiat = "EUR"
	}
	if available < required {
		requirement := "spot buy + hedge collateral + security deposit"
		if tradeMode == TradeModeHedgelessDoubleCollateral {
			requirement = "exchange fiat collateral only (hedgeless; BTC pool checked separately)"
		}
		return fmt.Errorf(
			"insufficient %s balance on %s: available %.2f, capital requirement %.2f (%s)",
			fiat, provider, available, required, requirement,
		)
	}
	return nil
}

// checkLiquidityPool verifies that the form of BTC the DEX can spend immediately
// covers position plus security deposit. Bisq/HodlHodl require confirmed
// on-chain BTC; RoboSats requires outbound Lightning channel liquidity.
func (c *BidChecker) checkLiquidityPool(ctx context.Context, dex string, requiredBTC float64) error {
	if requiredBTC <= 0 {
		return fmt.Errorf("pre-trade check failed: required BTC liquidity pool size is missing or zero")
	}
	c.walletMu.RLock()
	src := c.btcBalance
	lightning := c.lightningBalance
	c.walletMu.RUnlock()
	if strings.EqualFold(strings.TrimSpace(dex), "robosats") {
		// Total wallet BTC is not enough for an immediate Lightning payment: only
		// our local side of open channels is spendable without another rebalance.
		if lightning == nil {
			return fmt.Errorf("pre-trade check failed: LND outbound Lightning balance is not available")
		}
		available, err := lightning.OutboundLightningBTC(ctx)
		if err != nil {
			return fmt.Errorf("reading LND outbound Lightning balance: %w", err)
		}
		if available+1e-12 < requiredBTC {
			return fmt.Errorf("insufficient LND outbound Lightning liquidity: available %.8f, required %.8f", available, requiredBTC)
		}
		return nil
	}
	if src == nil {
		return fmt.Errorf("pre-trade check failed: LND wallet balance is not available; initialise the LND wallet before running hedgeless trades")
	}
	available, err := src.ConfirmedOnChainBTC(ctx)
	if err != nil {
		return fmt.Errorf("reading LND on-chain balance for liquidity pool check: %w", err)
	}
	if available+1e-12 < requiredBTC {
		return fmt.Errorf(
			"insufficient LND on-chain BTC for liquidity pool: available %.8f, required %.8f (DEX position + security deposit)",
			available, requiredBTC,
		)
	}
	return nil
}

// checkLNDOnline verifies the LND wallet RPC is reachable and unlocked.
func (c *BidChecker) checkLNDOnline(ctx context.Context) error {
	c.walletMu.RLock()
	src := c.lndOnline
	c.walletMu.RUnlock()
	if src == nil {
		return fmt.Errorf("pre-trade check failed: LND wallet is not online; initialise the LND wallet before trading")
	}
	if err := src.CheckOnline(ctx); err != nil {
		return fmt.Errorf("pre-trade check failed: LND wallet is not online: %w", err)
	}
	return nil
}

func (c *BidChecker) checkCommunicationGateway(ctx context.Context) error {
	if c.communication == nil || !c.communication.Enabled() {
		return fmt.Errorf("pre-trade check failed: no active Telegram communication channel is configured; configure the Telegram bot token and chat ID before trading")
	}
	return nil
}

func (c *BidChecker) checkFiatControllerService(ctx context.Context) error {
	if c.fiatController == nil {
		return fmt.Errorf("pre-trade check failed: Fiat Controller checker is not configured")
	}
	if checker, ok := c.fiatController.(FiatControllerHealthChecker); ok {
		return checker.CheckService(ctx)
	}
	return nil
}

func (c *BidChecker) checkFiatControllerNotifications(ctx context.Context, paymentMethod string) error {
	if c.fiatController == nil {
		return fmt.Errorf("pre-trade check failed: Fiat Controller checker is not configured")
	}
	if strings.TrimSpace(paymentMethod) == "" {
		return fmt.Errorf("pre-trade check failed: cannot validate notification channels without a resolved payment method")
	}
	if checker, ok := c.fiatController.(FiatControllerHealthChecker); ok {
		return checker.CheckNotificationHealth(ctx, paymentMethod)
	}
	return c.fiatController.Check(ctx, paymentMethod)
}

func (c *BidChecker) checkWhitelist(ctx context.Context, provider string) (string, error) {
	c.walletMu.RLock()
	wallet := c.wallet
	c.walletMu.RUnlock()
	if wallet == nil {
		return "", fmt.Errorf("LND wallet not available; cannot verify that %s whitelist contains an LND wallet address", provider)
	}

	cexAddresses, err := c.whitelist.WhitelistedBTCAddresses(ctx, provider)
	if err != nil {
		if message := whitelistCredentialErrorMessage(provider, err); message != "" {
			log.Printf("pre-trade whitelist check: %s: %v", provider, err)
			return "", fmt.Errorf("%s", message)
		}
		return "", fmt.Errorf("fetching %s whitelist for pre-trade check: %w", provider, err)
	}

	walletAddresses, err := wallet.OnChainAddresses(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching LND wallet addresses for pre-trade check: %w", err)
	}
	if addr := MatchWhitelistedLNDAddress(walletAddresses, cexAddresses); addr != "" {
		return addr, nil
	}

	return "", fmt.Errorf(
		"pre-trade check failed: %s whitelist contains no address from the current LND wallet; add an LND on-chain address to the %s withdrawal whitelist before trading",
		provider, provider,
	)
}

func whitelistCredentialErrorMessage(provider string, err error) string {
	if err == nil || !strings.EqualFold(strings.TrimSpace(provider), "binance") {
		return ""
	}
	detail := err.Error()
	if !strings.Contains(detail, `"code":-2015`) &&
		!strings.Contains(detail, "Invalid API-key, IP, or permissions for action") {
		return ""
	}
	return "Binance API access denied. Check the API key, allowed IP, and withdrawal permissions."
}

// resolvePaymentMethod picks the single method to validate: the explicitly
// requested one wins; otherwise the highest-priority linked+active catalog row
// among the offer's accepted methods.
func (c *BidChecker) resolvePaymentMethod(ctx context.Context, paymentMethod string, paymentMethods []string) (string, error) {
	if method := strings.TrimSpace(paymentMethod); method != "" {
		return method, nil
	}
	names := make([]string, 0, len(paymentMethods))
	for _, m := range paymentMethods {
		if m = strings.TrimSpace(m); m != "" {
			names = append(names, m)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("pre-trade check failed: payment method is required; select one on the offer or in the dashboard payment filter")
	}
	if c.paymentSetup == nil {
		return "", fmt.Errorf("payment setup checker not configured")
	}
	row, err := c.paymentSetup.PickPaymentMethod(ctx, names)
	if err != nil {
		return "", fmt.Errorf("picking payment method for pre-trade check: %w", err)
	}
	if row == nil {
		return "", fmt.Errorf(
			"pre-trade check failed: none of the offer's payment methods (%s) is linked and active; configure one in the payment method categorization panel",
			strings.Join(names, ", "),
		)
	}
	return row.Name, nil
}

// checkPaymentSetup validates one catalog method against the payment solution
// its payment_methods row links to: Fiat Transmitter → accounts row with the
// provider's required fields, Bank → the linked bank_accounts row
// (active, iban_bic, IBAN present), Crypto → the linked wallet_addresses row.
// Cash, Gift Cards and Others are never supported for automated trading.
func (c *BidChecker) checkPaymentSetup(ctx context.Context, paymentMethod string) error {
	method := strings.TrimSpace(paymentMethod)
	if method == "" {
		return fmt.Errorf("pre-trade check failed: payment method is required; select one on the offer or in the dashboard payment filter")
	}
	if c.paymentSetup == nil {
		return fmt.Errorf("payment setup checker not configured")
	}

	row, err := c.paymentSetup.PaymentMethodByName(ctx, method)
	if err != nil {
		log.Printf("pre-trade payment setup: load payment method %q: %v", method, err)
		return fmt.Errorf(
			"pre-trade check failed: %s payment configuration could not be read; retry, then check the mm-controller logs if the problem persists",
			method,
		)
	}
	if row == nil {
		return fmt.Errorf(
			"pre-trade check failed: %s is not in the payment methods catalog; run a snapshot or add it in the categorization panel",
			method,
		)
	}
	if !row.IsActive {
		return fmt.Errorf(
			"pre-trade check failed: %s is disabled; activate it in the payment method categorization panel",
			method,
		)
	}

	switch paymentmethods.CategoryIDFromDisplay(row.Category) {
	case paymentmethods.CategoryFiatTransmitter:
		return c.checkFiatTransmitterSetup(ctx, method, row)
	case paymentmethods.CategoryBankTransfer:
		return c.checkBankAccountSetup(ctx, method, row)
	case paymentmethods.CategoryCrypto:
		return c.checkWalletAddressSetup(ctx, method, row)
	default:
		return fmt.Errorf(
			"pre-trade check failed: %s (%s) is not supported for automated trading",
			method, row.Category,
		)
	}
}

func (c *BidChecker) checkFiatTransmitterSetup(ctx context.Context, method string, row *PaymentMethodSnapshot) error {
	if row.AccountID == nil {
		return fmt.Errorf(
			"pre-trade check failed: %s is not linked to a Fiat Transmitter account; link one in the payment method categorization panel",
			method,
		)
	}
	account, err := c.paymentSetup.FiatAccountByID(ctx, *row.AccountID)
	if err != nil {
		return fmt.Errorf("loading account for %s pre-trade check: %w", method, err)
	}
	if account == nil {
		return fmt.Errorf(
			"pre-trade check failed: the account linked to %s no longer exists; re-link it in the payment method categorization panel",
			method,
		)
	}

	fieldValues := map[string]string{
		"email":       strings.TrimSpace(account.Email),
		"phone":       strings.TrimSpace(account.Phone),
		"payment_tag": strings.TrimSpace(account.PaymentTag),
	}
	for _, field := range paymentmethods.RequiredAccountFields[account.Provider] {
		if fieldValues[field] == "" {
			label := field
			if field == "payment_tag" {
				label = "payment tag"
			}
			return fmt.Errorf(
				"pre-trade check failed: %s account is missing %s",
				account.Provider, label,
			)
		}
	}
	return nil
}

func (c *BidChecker) checkBankAccountSetup(ctx context.Context, method string, row *PaymentMethodSnapshot) error {
	if row.BankAccountID == nil {
		return fmt.Errorf(
			"pre-trade check failed: %s is not linked to a bank account; link one in the payment method categorization panel",
			method,
		)
	}
	acct, err := c.paymentSetup.BankAccountByID(ctx, *row.BankAccountID)
	if err != nil {
		return fmt.Errorf("loading bank account for %s pre-trade check: %w", method, err)
	}
	if acct == nil {
		return fmt.Errorf(
			"pre-trade check failed: the bank account linked to %s no longer exists; re-link it in the payment method categorization panel",
			method,
		)
	}
	if !acct.IsActive {
		return fmt.Errorf(
			"pre-trade check failed: the bank account linked to %s is inactive; activate it under Trading Accounts or link another",
			method,
		)
	}
	if !strings.EqualFold(acct.Scheme, "iban_bic") {
		return fmt.Errorf(
			"pre-trade check failed: the bank account linked to %s uses scheme %q, expected iban_bic",
			method, acct.Scheme,
		)
	}
	if strings.TrimSpace(acct.Details["iban"]) == "" {
		return fmt.Errorf(
			"pre-trade check failed: the bank account linked to %s has no IBAN; complete it under Trading Accounts",
			method,
		)
	}
	return nil
}

// checkMessageTemplate confirms a chat-style DEX venue (RoboSats, Bisq2) has a
// saved message template for the resolved payment method. Structured venues
// (Bisq, HodlHodl) exchange payment details via the trade contract instead of
// chat, so the check does not apply there. A missing template still lets the
// trade proceed with a generic default request message — this only flags it
// so the operator can personalize it beforehand.
func (c *BidChecker) checkMessageTemplate(ctx context.Context, dex, paymentMethod string) error {
	if strings.TrimSpace(paymentMethod) == "" {
		return nil
	}
	platform := db.DexPlatformFromSlug(dex)
	if platform != db.DexBisq2 && platform != db.DexRoboSats {
		return nil
	}
	if c.chatTemplates == nil {
		return fmt.Errorf("chat template checker not configured")
	}
	template, err := c.chatTemplates.GetChatTemplate(ctx, string(platform), paymentMethod)
	if err != nil {
		return fmt.Errorf("loading message template for %s pre-trade check: %w", paymentMethod, err)
	}
	if strings.TrimSpace(template) == "" {
		return fmt.Errorf(
			"pre-trade check failed: no message template is saved for %s on %s; add one in the payment method categorization panel or the generic default request will be sent",
			paymentMethod, platform,
		)
	}
	return nil
}

func (c *BidChecker) checkWalletAddressSetup(ctx context.Context, method string, row *PaymentMethodSnapshot) error {
	if row.WalletAddressID == nil {
		return fmt.Errorf(
			"pre-trade check failed: %s is not linked to a wallet address; link one in the payment method categorization panel",
			method,
		)
	}
	wallet, err := c.paymentSetup.WalletAddressByID(ctx, *row.WalletAddressID)
	if err != nil {
		return fmt.Errorf("loading wallet address for %s pre-trade check: %w", method, err)
	}
	if wallet == nil {
		return fmt.Errorf(
			"pre-trade check failed: the wallet address linked to %s no longer exists; re-link it in the payment method categorization panel",
			method,
		)
	}
	if strings.TrimSpace(wallet.Address) == "" {
		return fmt.Errorf(
			"pre-trade check failed: the wallet address linked to %s is empty; re-link it in the payment method categorization panel",
			method,
		)
	}
	return nil
}

// ParseBankAccountDetails unmarshals bank account details JSON for repository adapters.
func ParseBankAccountDetails(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var details map[string]string
	if err := json.Unmarshal(raw, &details); err != nil {
		return nil
	}
	return details
}
