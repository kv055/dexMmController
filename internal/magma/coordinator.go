package magma

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"dexTakerController/internal/lsp"
	lnd "dexTakerController/internal/wallethub"

	"github.com/google/uuid"
)

const providerLabel = "Amboss Magma"

// OrderRecord is the API-facing persisted Magma order view.
type OrderRecord struct {
	ID                  string    `json:"id"`
	Pool                string    `json:"pool"`
	OrderID             string    `json:"order_id"`
	PublicKey           string    `json:"public_key"`
	LSPBalanceSat       int64     `json:"lsp_balance_sat"`
	AnnounceChannel     bool      `json:"announce_channel"`
	ChannelExpiryBlocks int       `json:"channel_expiry_blocks"`
	OrderState          string    `json:"order_state"`
	PaymentState        string    `json:"payment_state"`
	Bolt11Invoice       string    `json:"bolt11_invoice"`
	OrderTotalSat       int64     `json:"order_total_sat"`
	FeeTotalSat         int64     `json:"fee_total_sat"`
	FundingOutpoint     string    `json:"funding_outpoint"`
	RemotePubkey        string    `json:"remote_pubkey"`
	PaymentHash         string    `json:"payment_hash"`
	AutoRegistered      bool      `json:"auto_registered"`
	FailureReason       string    `json:"failure_reason,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// CreateParams are operator inputs for a new Magma order.
type CreateParams struct {
	Pool                Pool
	LSPBalanceSat       int64
	AnnounceChannel     bool
	ChannelExpiryBlocks int
}

// LNDWallet is the subset of LND operations the coordinator needs.
type LNDWallet interface {
	NodeInfo(ctx context.Context) (*lnd.NodeInfoResponse, error)
	OnChainBalance(ctx context.Context) (*lnd.OnChainBalanceResponse, error)
	ListChannels(ctx context.Context) ([]lnd.Channel, error)
	ListPeers(ctx context.Context) ([]lnd.Peer, error)
	ConnectPeer(ctx context.Context, pubkey, host string) error
	PayInvoice(ctx context.Context, paymentRequest string, feeLimitSat int64) (*lnd.PayInvoiceResponse, error)
	DecodeInvoice(ctx context.Context, paymentRequest string) (*lnd.DecodedInvoice, error)
}

// OrderStore persists Magma order lifecycle state.
type OrderStore interface {
	Create(ctx context.Context, row OrderRecord) error
	GetByID(ctx context.Context, id string) (OrderRecord, error)
	List(ctx context.Context, limit int) ([]OrderRecord, error)
	Incomplete(ctx context.Context) ([]OrderRecord, error)
	Update(ctx context.Context, row OrderRecord) error
	MarkPayment(ctx context.Context, id, paymentHash string) error
}

// LSPRegistrar updates the singleton LSP channel configuration.
type LSPRegistrar interface {
	Get(ctx context.Context) (lsp.Config, error)
	Update(ctx context.Context, cfg lsp.Config) (lsp.Config, error)
}

// ErrLSPConfigNotFound means no LSP channel has been configured yet.
var ErrLSPConfigNotFound = errors.New("LSP channel configuration not found")

// Coordinator orchestrates Magma BLIP-0051 purchases against LND.
type Coordinator struct {
	Client             *Client
	Offers             *OffersClient
	Store              OrderStore
	LSPRegistrar       LSPRegistrar
	Wallet             func(context.Context) LNDWallet
	DefaultPool        Pool
	DefaultFeeLimitSat int64
	PollInterval       time.Duration
}

// InfoSummary combines Magma capabilities with the connected node identity.
type InfoSummary struct {
	Pool                  string `json:"pool"`
	PublicKey             string `json:"public_key"`
	SyncedToChain         bool   `json:"synced_to_chain"`
	SyncedToGraph         bool   `json:"synced_to_graph"`
	BlockHeight           uint32 `json:"block_height"`
	ChainReady            bool   `json:"chain_ready"`
	ConfirmedBalanceSat   int64  `json:"confirmed_balance_sat"`
	UnconfirmedBalanceSat int64  `json:"unconfirmed_balance_sat"`
	OutboundSat           int64  `json:"outbound_sat"`
	WalletReady           bool   `json:"wallet_ready"`
	Info                  Info   `json:"info"`
}

func (c *Coordinator) wallet(ctx context.Context) (LNDWallet, error) {
	if c == nil || c.Wallet == nil {
		return nil, fmt.Errorf("LND wallet client is not configured")
	}
	client := c.Wallet(ctx)
	if client == nil {
		return nil, fmt.Errorf("LND wallet client is not configured")
	}
	return client, nil
}

func (c *Coordinator) poolOrDefault(pool Pool) Pool {
	if err := pool.Validate(); err == nil {
		return pool
	}
	if c != nil && c.DefaultPool != "" {
		if err := c.DefaultPool.Validate(); err == nil {
			return c.DefaultPool
		}
	}
	return PoolRecommended
}

// GetInfo loads Magma capabilities and the local node pubkey.
func (c *Coordinator) GetInfo(ctx context.Context, pool Pool) (InfoSummary, error) {
	if c == nil || c.Client == nil {
		return InfoSummary{}, fmt.Errorf("magma client is not configured")
	}
	pool = c.poolOrDefault(pool)
	wallet, err := c.wallet(ctx)
	if err != nil {
		return InfoSummary{}, err
	}
	node, err := wallet.NodeInfo(ctx)
	if err != nil {
		return InfoSummary{}, fmt.Errorf("lnd node info: %w", err)
	}
	if node == nil || strings.TrimSpace(node.IdentityPubkey) == "" {
		return InfoSummary{}, fmt.Errorf("LND identity pubkey is unavailable")
	}
	info, err := c.Client.GetInfo(ctx, pool)
	if err != nil {
		return InfoSummary{}, err
	}
	confirmed, unconfirmed, outbound, _ := walletSpendable(ctx, wallet)
	return InfoSummary{
		Pool:                  string(pool),
		PublicKey:             node.IdentityPubkey,
		SyncedToChain:         node.SyncedToChain,
		SyncedToGraph:         node.SyncedToGraph,
		BlockHeight:           node.BlockHeight,
		ChainReady:            node.SyncedToChain,
		ConfirmedBalanceSat:   confirmed,
		UnconfirmedBalanceSat: unconfirmed,
		OutboundSat:           outbound,
		WalletReady:           outbound > 0,
		Info:                  info,
	}, nil
}

// CreateOrder connects to Magma, creates an order, and persists it.
func (c *Coordinator) CreateOrder(ctx context.Context, params CreateParams) (OrderRecord, error) {
	if c == nil || c.Client == nil || c.Store == nil {
		return OrderRecord{}, fmt.Errorf("magma order flow is not configured")
	}
	pool := c.poolOrDefault(params.Pool)
	if params.LSPBalanceSat <= 0 {
		return OrderRecord{}, fmt.Errorf("lsp_balance_sat must be positive")
	}
	if params.ChannelExpiryBlocks <= 0 {
		return OrderRecord{}, fmt.Errorf("channel_expiry_blocks must be positive")
	}

	wallet, err := c.wallet(ctx)
	if err != nil {
		return OrderRecord{}, err
	}
	node, err := wallet.NodeInfo(ctx)
	if err != nil {
		return OrderRecord{}, fmt.Errorf("lnd node info: %w", err)
	}
	if node == nil || strings.TrimSpace(node.IdentityPubkey) == "" {
		return OrderRecord{}, fmt.Errorf("LND identity pubkey is unavailable")
	}
	if !node.SyncedToChain {
		return OrderRecord{}, chainSyncError(node)
	}
	if err := requireLightningOutbound(ctx, wallet, 1); err != nil {
		return OrderRecord{}, err
	}

	info, err := c.Client.GetInfo(ctx, pool)
	if err != nil {
		return OrderRecord{}, err
	}
	if err := validateBalance(params.LSPBalanceSat, info); err != nil {
		return OrderRecord{}, err
	}
	if params.ChannelExpiryBlocks > info.MaxChannelExpiryBlocks {
		return OrderRecord{}, fmt.Errorf("channel_expiry_blocks exceeds Magma maximum %d", info.MaxChannelExpiryBlocks)
	}
	if err := connectToLSP(ctx, wallet, info.URIs); err != nil {
		return OrderRecord{}, err
	}

	requiredConfirms := info.MinRequiredChannelConfirmations
	if requiredConfirms <= 0 {
		requiredConfirms = 6
	}
	fundingConfirms := info.MinFundingConfirmsWithinBlocks
	if fundingConfirms <= 0 {
		fundingConfirms = 6
	}

	remote, err := c.Client.CreateOrder(ctx, pool, CreateOrderRequest{
		LSPBalanceSat:                strconv.FormatInt(params.LSPBalanceSat, 10),
		ClientBalanceSat:             "0",
		RequiredChannelConfirmations: requiredConfirms,
		FundingConfirmsWithinBlocks:  fundingConfirms,
		ChannelExpiryBlocks:          params.ChannelExpiryBlocks,
		Token:                        "",
		AnnounceChannel:              params.AnnounceChannel,
		PublicKey:                    node.IdentityPubkey,
	})
	if err != nil {
		return OrderRecord{}, err
	}
	if strings.TrimSpace(remote.OrderID) == "" {
		return OrderRecord{}, fmt.Errorf("magma returned an empty order_id")
	}

	record := orderFromRemote(uuid.NewString(), string(pool), node.IdentityPubkey, params, remote)
	if err := c.Store.Create(ctx, record); err != nil {
		return OrderRecord{}, err
	}
	return record, nil
}

// PayOrder pays the stored BOLT11 invoice from LND.
func (c *Coordinator) PayOrder(ctx context.Context, id string, feeLimitSat int64) (OrderRecord, error) {
	if c == nil || c.Store == nil {
		return OrderRecord{}, fmt.Errorf("magma order flow is not configured")
	}
	record, err := c.Store.GetByID(ctx, id)
	if err != nil {
		return OrderRecord{}, err
	}
	if TerminalOrderState(record.OrderState) {
		return record, nil
	}
	state := strings.ToUpper(strings.TrimSpace(record.PaymentState))
	if state == "HOLD" || state == "PAID" {
		return record, nil
	}
	if strings.TrimSpace(record.Bolt11Invoice) == "" {
		return OrderRecord{}, fmt.Errorf("order has no BOLT11 invoice to pay")
	}

	wallet, err := c.wallet(ctx)
	if err != nil {
		return OrderRecord{}, err
	}
	decoded, err := wallet.DecodeInvoice(ctx, record.Bolt11Invoice)
	if err != nil {
		return OrderRecord{}, fmt.Errorf("decode invoice: %w", err)
	}
	if feeLimitSat <= 0 {
		feeLimitSat = c.DefaultFeeLimitSat
	}
	if feeLimitSat <= 0 {
		feeLimitSat = 1000
	}
	amountSat, _ := ParseSatString(decoded.NumSatoshis)
	if amountSat <= 0 {
		amountSat = record.OrderTotalSat
	}
	if err := requireLightningOutbound(ctx, wallet, amountSat+feeLimitSat); err != nil {
		return OrderRecord{}, err
	}
	if _, err := wallet.PayInvoice(ctx, record.Bolt11Invoice, feeLimitSat); err != nil {
		return OrderRecord{}, fmt.Errorf("pay invoice: %w", err)
	}
	if err := c.Store.MarkPayment(ctx, id, decoded.PaymentHash); err != nil {
		return OrderRecord{}, err
	}
	record.PaymentHash = decoded.PaymentHash
	if refreshed, syncErr := c.syncOrder(ctx, record); syncErr == nil {
		record = refreshed
	}
	return record, nil
}

// GetOrder returns the latest persisted order, refreshing from Magma first.
func (c *Coordinator) GetOrder(ctx context.Context, id string) (OrderRecord, error) {
	record, err := c.Store.GetByID(ctx, id)
	if err != nil {
		return OrderRecord{}, err
	}
	return c.syncOrder(ctx, record)
}

// ListOffers returns the current Amboss Magma marketplace offer book.
func (c *Coordinator) ListOffers(ctx context.Context) ([]Offer, error) {
	if c == nil || c.Offers == nil {
		return nil, fmt.Errorf("magma offer book is not configured")
	}
	return c.Offers.ListOffers(ctx)
}

// ListOrders returns recent persisted orders without remote refresh.
func (c *Coordinator) ListOrders(ctx context.Context, limit int) ([]OrderRecord, error) {
	if c == nil || c.Store == nil {
		return nil, fmt.Errorf("magma order flow is not configured")
	}
	return c.Store.List(ctx, limit)
}

// PollIncomplete refreshes all non-terminal orders from Magma.
func (c *Coordinator) PollIncomplete(ctx context.Context) error {
	if c == nil || c.Store == nil || c.Client == nil {
		return nil
	}
	rows, err := c.Store.Incomplete(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := c.syncOrder(ctx, row); err != nil && ctx.Err() == nil {
			log.Printf("magma order poll %s: %v", row.ID, err)
		}
	}
	return nil
}

// RunPoller periodically refreshes incomplete Magma orders.
func (c *Coordinator) RunPoller(ctx context.Context) {
	interval := c.PollInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := c.PollIncomplete(ctx); err != nil && ctx.Err() == nil {
			log.Printf("magma order poller: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) syncOrder(ctx context.Context, record OrderRecord) (OrderRecord, error) {
	if c == nil || c.Client == nil {
		return record, nil
	}
	pool := Pool(record.Pool)
	if err := pool.Validate(); err != nil {
		return record, err
	}
	remote, err := c.Client.GetOrder(ctx, pool, record.OrderID)
	if err != nil {
		return record, err
	}
	updated := orderFromRemote(record.ID, record.Pool, record.PublicKey, CreateParams{
		Pool:                pool,
		LSPBalanceSat:       record.LSPBalanceSat,
		AnnounceChannel:     record.AnnounceChannel,
		ChannelExpiryBlocks: record.ChannelExpiryBlocks,
	}, remote)
	updated.PaymentHash = record.PaymentHash
	updated.CreatedAt = record.CreatedAt
	if remote.OrderState == "FAILED" && updated.FailureReason == "" {
		updated.FailureReason = "magma order failed"
	}
	if remote.Channel != nil && strings.TrimSpace(remote.Channel.FundingOutpoint) != "" {
		updated.FundingOutpoint = remote.Channel.FundingOutpoint
	}
	if updated.OrderState == "COMPLETED" && !record.AutoRegistered && c.LSPRegistrar != nil {
		if registered, regErr := c.autoRegister(ctx, updated); regErr != nil {
			log.Printf("magma auto-register %s: %v", updated.ID, regErr)
		} else {
			updated = registered
		}
	}
	if err := c.Store.Update(ctx, updated); err != nil {
		return record, err
	}
	return updated, nil
}

func (c *Coordinator) autoRegister(ctx context.Context, record OrderRecord) (OrderRecord, error) {
	wallet, err := c.wallet(ctx)
	if err != nil {
		return record, err
	}
	channelPoint := strings.TrimSpace(record.FundingOutpoint)
	if channelPoint == "" {
		return record, fmt.Errorf("completed order has no funding outpoint")
	}
	channels, err := wallet.ListChannels(ctx)
	if err != nil {
		return record, err
	}
	var remotePubkey string
	for _, ch := range channels {
		if strings.EqualFold(strings.TrimSpace(ch.ChannelPoint), channelPoint) {
			remotePubkey = ch.RemotePubkey
			break
		}
	}
	if remotePubkey == "" {
		return record, fmt.Errorf("funded channel %s is not visible in LND yet", channelPoint)
	}
	record.RemotePubkey = remotePubkey

	current, err := c.LSPRegistrar.Get(ctx)
	if err != nil && !errors.Is(err, ErrLSPConfigNotFound) {
		return record, err
	}
	cfg := lsp.Config{
		ChannelPoint:  channelPoint,
		RemotePubkey:  remotePubkey,
		ProviderLabel: providerLabel,
		Enabled:       true,
		HeadroomSat:   current.HeadroomSat,
		MaxSwapFeeSat: current.MaxSwapFeeSat,
		MaxSwapFeePPM: current.MaxSwapFeePPM,
	}
	if cfg.MaxSwapFeeSat <= 0 {
		cfg.MaxSwapFeeSat = 5000
	}
	if cfg.MaxSwapFeePPM <= 0 {
		cfg.MaxSwapFeePPM = 10000
	}
	if _, err := c.LSPRegistrar.Update(ctx, cfg); err != nil {
		return record, err
	}
	record.AutoRegistered = true
	return record, nil
}

func requireLightningOutbound(ctx context.Context, wallet LNDWallet, minSat int64) error {
	confirmed, _, outbound, err := walletSpendable(ctx, wallet)
	if err != nil {
		return err
	}
	if outbound < minSat {
		return fmt.Errorf(
			"LND Lightning outbound is too low to pay a Magma invoice (outbound %d sats, need at least %d, confirmed wallet %d sats)",
			outbound, minSat, confirmed,
		)
	}
	return nil
}

func walletSpendable(ctx context.Context, wallet LNDWallet) (confirmed, unconfirmed, outbound int64, err error) {
	if wallet == nil {
		return 0, 0, 0, fmt.Errorf("LND wallet client is not configured")
	}
	if bal, balErr := wallet.OnChainBalance(ctx); balErr == nil && bal != nil {
		confirmed, _ = ParseSatString(bal.ConfirmedBalance)
		unconfirmed, _ = ParseSatString(bal.UnconfirmedBalance)
	}
	channels, chErr := wallet.ListChannels(ctx)
	if chErr != nil {
		return confirmed, unconfirmed, 0, fmt.Errorf("lnd list channels: %w", chErr)
	}
	for _, ch := range channels {
		if !ch.Active {
			continue
		}
		local, _ := ParseSatString(ch.LocalBalance)
		outbound += local
	}
	return confirmed, unconfirmed, outbound, nil
}

func chainSyncError(node *lnd.NodeInfoResponse) error {
	if node == nil {
		return fmt.Errorf("LND must finish syncing to chain before opening an LSP channel")
	}
	return fmt.Errorf(
		"LND must finish syncing to chain before opening an LSP channel (synced_to_chain=false, block_height=%d, synced_to_graph=%t)",
		node.BlockHeight,
		node.SyncedToGraph,
	)
}

func validateBalance(amount int64, info Info) error {
	min, err := ParseSatString(info.MinInitialLSPBalanceSat)
	if err != nil {
		return err
	}
	max, err := ParseSatString(info.MaxInitialLSPBalanceSat)
	if err != nil {
		return err
	}
	if min > 0 && amount < min {
		return fmt.Errorf("lsp_balance_sat must be at least %d", min)
	}
	if max > 0 && amount > max {
		return fmt.Errorf("lsp_balance_sat must be at most %d", max)
	}
	return nil
}

func connectToLSP(ctx context.Context, wallet LNDWallet, uris []string) error {
	if len(uris) == 0 {
		return nil
	}
	peers, err := wallet.ListPeers(ctx)
	if err != nil {
		return fmt.Errorf("list peers: %w", err)
	}
	connected := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		connected[strings.ToLower(strings.TrimSpace(peer.PubKey))] = struct{}{}
	}
	var lastErr error
	for _, uri := range uris {
		pubkey, host, err := ParseURI(uri)
		if err != nil {
			lastErr = err
			continue
		}
		if _, ok := connected[strings.ToLower(pubkey)]; ok {
			return nil
		}
		if err := wallet.ConnectPeer(ctx, pubkey, host); err != nil {
			lastErr = fmt.Errorf("connect peer %s: %w", uri, err)
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no usable Magma LSP URIs were provided")
}

func orderFromRemote(id, pool, publicKey string, params CreateParams, remote Order) OrderRecord {
	orderTotal, _ := ParseSatString(remote.Payment.Bolt11.OrderTotalSat)
	feeTotal, _ := ParseSatString(remote.Payment.Bolt11.FeeTotalSat)
	lspBalance, _ := ParseSatString(remote.LSPBalanceSat)
	if params.LSPBalanceSat > 0 {
		lspBalance = params.LSPBalanceSat
	}
	record := OrderRecord{
		ID:                  id,
		Pool:                pool,
		OrderID:             remote.OrderID,
		PublicKey:           publicKey,
		LSPBalanceSat:       lspBalance,
		AnnounceChannel:     params.AnnounceChannel,
		ChannelExpiryBlocks: params.ChannelExpiryBlocks,
		OrderState:          remote.OrderState,
		PaymentState:        remote.Payment.Bolt11.State,
		Bolt11Invoice:       remote.Payment.Bolt11.Invoice,
		OrderTotalSat:       orderTotal,
		FeeTotalSat:         feeTotal,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	if remote.Channel != nil {
		record.FundingOutpoint = remote.Channel.FundingOutpoint
	}
	return record
}
