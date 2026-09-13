package lnd

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Client is the LND REST client.
type Client struct {
	host                        string
	macaroon                    string
	httpClient                  *http.Client
	walletJustInitialized       bool
	generatedCipherSeedMnemonic []string
	lastExportedChannelBackup   []byte
	lastExportedChannelCount    int
}

type stateResponse struct {
	State string `json:"state"`
}

type unlockRequest struct {
	WalletPassword string `json:"wallet_password"` // base64
}

type initWalletRequest struct {
	WalletPassword     string   `json:"wallet_password"` // base64
	StatelessInit      bool     `json:"stateless_init"`
	CipherSeedMnemonic []string `json:"cipher_seed_mnemonic,omitempty"`
	AEZEEDPassphrase   string   `json:"aezeed_passphrase,omitempty"` // base64
	RecoveryWindow     int32    `json:"recovery_window,omitempty"`
}

// InitConfig contains optional wallet initialization material for LND.
type InitConfig struct {
	// CipherSeedMnemonic is the cipher seed mnemonic value for the init config.
	CipherSeedMnemonic []string
	// AEZEEDPassphrase is the aezeedpassphrase value for the init config.
	AEZEEDPassphrase string
	// RecoveryWindow is the recovery window value for the init config.
	RecoveryWindow int32
	// ChannelBackupRestore is an optional static channel backup blob from Postgres.
	ChannelBackupRestore []byte
}

// New initialises the LND client, waits for LND to be reachable,
// handles first-boot wallet creation or unlock, then waits until
// LND is fully synced and ready.
//
// host:          e.g. "https://localhost:8080"
// tlsCertPath:   path to LND's tls.cert file
// macaroonPath:  path to admin.macaroon file
// password:      plaintext wallet password
func New(
	ctx context.Context,
	host, tlsCertPath, macaroonPath, password string,
	initCfg InitConfig,
) (*Client, error) {
	httpClient, err := buildHTTPClient(tlsCertPath)
	if err != nil {
		return nil, fmt.Errorf("building HTTP client: %w", err)
	}

	c := &Client{host: host, httpClient: httpClient}

	// Load macaroon if it exists already (won't exist on very first boot
	// until after initwallet completes).
	_ = c.loadMacaroon(macaroonPath)

	// 1. Wait for LND REST to be reachable.
	fmt.Println("lnd: waiting for REST API...")
	if err := c.waitReachable(ctx); err != nil {
		return nil, fmt.Errorf("waiting for LND: %w", err)
	}

	// 2. Handle wallet state.
	if err := c.handleWalletState(ctx, macaroonPath, password, initCfg); err != nil {
		return nil, err
	}

	// 3. Wait until RPC is available. SERVER_ACTIVE requires full chain
	// backend sync, which can lag when neutrino peers are slow, but wallet RPCs
	// are already available in RPC_ACTIVE.
	fmt.Println("lnd: waiting for RPC_ACTIVE or SERVER_ACTIVE...")
	if err := c.waitState(ctx, "RPC_ACTIVE", "SERVER_ACTIVE"); err != nil {
		return nil, fmt.Errorf("waiting for LND RPC readiness: %w", err)
	}
	if err := c.waitMacaroon(ctx, macaroonPath); err != nil {
		return nil, fmt.Errorf("loading macaroon after LND RPC readiness: %w", err)
	}

	if err := c.syncChannelBackups(ctx, initCfg.ChannelBackupRestore); err != nil {
		return nil, fmt.Errorf("syncing channel backups: %w", err)
	}

	fmt.Println("lnd: ready")
	return c, nil
}

// WalletJustInitialized reports whether this client run created a new wallet.
func (c *Client) WalletJustInitialized() bool {
	if c == nil {
		return false
	}
	return c.walletJustInitialized
}

// GeneratedCipherSeedMnemonic returns the aezeed LND generated during fresh wallet init.
func (c *Client) GeneratedCipherSeedMnemonic() string {
	if c == nil || len(c.generatedCipherSeedMnemonic) == 0 {
		return ""
	}
	return strings.Join(c.generatedCipherSeedMnemonic, " ")
}

// LastExportedChannelBackup returns the latest export from syncChannelBackups or ExportChannelBackup.
func (c *Client) LastExportedChannelBackup() ([]byte, int) {
	if c == nil {
		return nil, 0
	}
	return c.lastExportedChannelBackup, c.lastExportedChannelCount
}

// ExportChannelBackup refreshes the static channel backup snapshot from LND.
func (c *Client) ExportChannelBackup(ctx context.Context) error {
	return c.exportChannelBackup(ctx)
}

// UnlockIfLocked restores wallet RPC access after an LND restart. It is safe to
// call during an account health check: active wallets are left unchanged.
func (c *Client) UnlockIfLocked(ctx context.Context, password string) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("LND client is not configured")
	}
	if strings.TrimSpace(password) == "" {
		return false, fmt.Errorf("LND wallet password is empty")
	}

	state, err := c.getState(ctx)
	if err != nil {
		return false, fmt.Errorf("getting LND wallet state: %w", err)
	}
	if state != "LOCKED" {
		return false, nil
	}
	if err := c.unlockWallet(ctx, password); err != nil {
		return true, fmt.Errorf("unlocking wallet: %w", err)
	}
	if err := c.waitState(ctx, "RPC_ACTIVE", "SERVER_ACTIVE"); err != nil {
		return true, fmt.Errorf("waiting for LND RPC readiness after unlock: %w", err)
	}
	return true, nil
}

// ── static channel backups ──────────────────────────────────────────────────────

// ChannelPoint identifies a channel funding output.
type ChannelPoint struct {
	FundingTxidStr string `json:"funding_txid_str"`
	OutputIndex    uint32 `json:"output_index"`
}

// ChannelBackup is a single encrypted static channel backup.
type ChannelBackup struct {
	ChanPoint  ChannelPoint `json:"chan_point"`
	ChanBackup string       `json:"chan_backup"`
}

// ChannelBackups is a list of single-channel backups.
type ChannelBackups struct {
	ChanBackups []ChannelBackup `json:"chan_backups"`
}

// MultiChanBackup is the multi-channel encrypted backup blob.
type MultiChanBackup struct {
	ChanPoints      []ChannelPoint `json:"chan_points"`
	MultiChanBackup string         `json:"multi_chan_backup"`
}

// ChanBackupSnapshot is the export-all-channel-backups response.
type ChanBackupSnapshot struct {
	SingleChanBackups *ChannelBackups  `json:"single_chan_backups"`
	MultiChanBackup   *MultiChanBackup `json:"multi_chan_backup"`
}

type restoreChanBackupRequest struct {
	MultiChanBackup string `json:"multi_chan_backup"`
}

type restoreChanBackupResponse struct {
	NumRestored uint32 `json:"num_restored"`
}

func (c *Client) syncChannelBackups(ctx context.Context, restoreFrom []byte) error {
	if c.walletJustInitialized && len(restoreFrom) > 0 {
		restored, err := c.RestoreChannelBackups(ctx, restoreFrom)
		if err != nil {
			return fmt.Errorf("restore channel backups: %w", err)
		}
		fmt.Printf("lnd: restored %d channel backup(s) from database\n", restored)
	}
	return c.exportChannelBackup(ctx)
}

func (c *Client) exportChannelBackup(ctx context.Context) error {
	snapshot, err := c.ExportAllChannelBackups(ctx)
	if err != nil {
		return err
	}
	raw, channelCount, err := multiBackupBytes(snapshot)
	if err != nil {
		return err
	}
	c.lastExportedChannelBackup = raw
	c.lastExportedChannelCount = channelCount
	return nil
}

// ExportAllChannelBackups returns static channel backups for all known channels.
func (c *Client) ExportAllChannelBackups(ctx context.Context) (*ChanBackupSnapshot, error) {
	var resp ChanBackupSnapshot
	if err := c.get(ctx, "/v1/channels/backup", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RestoreChannelBackups restores channel funds from a multi-channel backup blob.
func (c *Client) RestoreChannelBackups(ctx context.Context, multiChanBackup []byte) (uint32, error) {
	if len(multiChanBackup) == 0 {
		return 0, fmt.Errorf("multi-channel backup is empty")
	}
	req := restoreChanBackupRequest{
		MultiChanBackup: base64.StdEncoding.EncodeToString(multiChanBackup),
	}
	var resp restoreChanBackupResponse
	if err := c.post(ctx, "/v1/channels/backup/restore", req, &resp); err != nil {
		return 0, err
	}
	return resp.NumRestored, nil
}

// MultiBackupBytes decodes the multi-channel backup payload from an export snapshot.
func MultiBackupBytes(snapshot *ChanBackupSnapshot) ([]byte, int, error) {
	return multiBackupBytes(snapshot)
}

func multiBackupBytes(snapshot *ChanBackupSnapshot) ([]byte, int, error) {
	if snapshot == nil || snapshot.MultiChanBackup == nil {
		return nil, 0, nil
	}
	encoded := snapshot.MultiChanBackup.MultiChanBackup
	if encoded == "" {
		return nil, len(snapshot.MultiChanBackup.ChanPoints), nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, 0, fmt.Errorf("decode multi_chan_backup: %w", err)
	}
	channelCount := len(snapshot.MultiChanBackup.ChanPoints)
	if channelCount == 0 && snapshot.SingleChanBackups != nil {
		channelCount = len(snapshot.SingleChanBackups.ChanBackups)
	}
	return raw, channelCount, nil
}

// ── startup ───────────────────────────────────────────────────────────────────

func (c *Client) handleWalletState(
	ctx context.Context,
	macaroonPath, password string,
	initCfg InitConfig,
) error {
	_ = macaroonPath
	for {
		state, err := c.getState(ctx)
		if err != nil {
			return fmt.Errorf("getting state: %w", err)
		}

		switch state {
		case "NON_EXISTING":
			cfg := initCfg
			if cfg.HasRestoreSecret() {
				fmt.Println("lnd: no wallet found, restoring from supplied wallet secret...")
				if err := cfg.Validate(); err != nil {
					return err
				}
			} else {
				fmt.Println("lnd: no wallet found, generating new aezeed cipher seed...")
				mnemonic, err := c.genSeed(ctx, cfg.AEZEEDPassphrase)
				if err != nil {
					return fmt.Errorf("generating cipher seed: %w", err)
				}
				cfg.CipherSeedMnemonic = mnemonic
				c.generatedCipherSeedMnemonic = append([]string(nil), mnemonic...)
			}
			if err := c.initWallet(ctx, password, cfg); err != nil {
				return fmt.Errorf("initialising wallet: %w", err)
			}
			c.walletJustInitialized = true
			return nil

		case "LOCKED":
			fmt.Println("lnd: wallet locked, unlocking...")
			if err := c.unlockWallet(ctx, password); err != nil {
				return fmt.Errorf("unlocking wallet: %w", err)
			}
			return nil

		case "WAITING_TO_START", "UNLOCKED":
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				continue
			}

		case "SERVER_ACTIVE", "RPC_ACTIVE":
			return nil

		default:
			return fmt.Errorf("unexpected LND state: %s", state)
		}
	}
}

// HasRestoreSecret reports whether init should recover an existing wallet root.
func (cfg InitConfig) HasRestoreSecret() bool {
	return len(cfg.CipherSeedMnemonic) > 0
}

// Validate validates restore configuration when a wallet secret is supplied.
func (cfg InitConfig) Validate() error {
	if len(cfg.CipherSeedMnemonic) > 0 && len(cfg.CipherSeedMnemonic) != 24 {
		return fmt.Errorf("cipher seed mnemonic must contain 24 words, got %d", len(cfg.CipherSeedMnemonic))
	}
	return nil
}

type genSeedResponse struct {
	CipherSeedMnemonic []string `json:"cipher_seed_mnemonic"`
}

func (c *Client) genSeed(ctx context.Context, aezeedPassphrase string) ([]string, error) {
	path := "/v1/genseed"
	if strings.TrimSpace(aezeedPassphrase) != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(aezeedPassphrase))
		path += "?aezeed_passphrase=" + url.QueryEscape(encoded)
	}
	var resp genSeedResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	if len(resp.CipherSeedMnemonic) != 24 {
		return nil, fmt.Errorf("genseed returned %d words, want 24", len(resp.CipherSeedMnemonic))
	}
	return resp.CipherSeedMnemonic, nil
}

func (c *Client) initWallet(ctx context.Context, password string, cfg InitConfig) error {
	req := initWalletRequest{
		WalletPassword: base64.StdEncoding.EncodeToString([]byte(password)),
		StatelessInit:  false,
	}
	if len(cfg.CipherSeedMnemonic) > 0 {
		req.CipherSeedMnemonic = append([]string(nil), cfg.CipherSeedMnemonic...)
		if cfg.AEZEEDPassphrase != "" {
			req.AEZEEDPassphrase = base64.StdEncoding.EncodeToString([]byte(cfg.AEZEEDPassphrase))
		}
		if cfg.RecoveryWindow > 0 {
			req.RecoveryWindow = cfg.RecoveryWindow
		}
	}
	return c.post(ctx, "/v1/initwallet", req, nil)
}

func (c *Client) waitReachable(ctx context.Context) error {
	for {
		_, err := c.getState(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			fmt.Printf("lnd: not reachable yet (%v), retrying...\n", err)
		}
	}
}

func (c *Client) waitState(ctx context.Context, targets ...string) error {
	targetSet := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		targetSet[target] = struct{}{}
	}
	for {
		state, err := c.getState(ctx)
		if err != nil {
			return err
		}
		if _, ok := targetSet[state]; ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			fmt.Printf("lnd: state=%s, waiting for one of %s...\n", state, strings.Join(targets, ", "))
		}
	}
}

func (c *Client) getState(ctx context.Context) (string, error) {
	var resp stateResponse
	if err := c.get(ctx, "/v1/state", &resp); err != nil {
		return "", err
	}
	return resp.State, nil
}

// ProbeState reports LND's wallet state (NON_EXISTING, LOCKED, RPC_ACTIVE, …) without
// needing a macaroon or an initialized client. The /v1/state endpoint is served by the
// WalletUnlocker and is available before any wallet exists, so this works on first boot.
func ProbeState(ctx context.Context, host, tlsCertPath string) (string, error) {
	httpClient, err := buildHTTPClient(tlsCertPath)
	if err != nil {
		return "", fmt.Errorf("building HTTP client: %w", err)
	}
	c := &Client{host: host, httpClient: httpClient}
	return c.getState(ctx)
}

func (c *Client) unlockWallet(ctx context.Context, password string) error {
	req := unlockRequest{
		WalletPassword: base64.StdEncoding.EncodeToString([]byte(password)),
	}
	return c.post(ctx, "/v1/unlockwallet", req, nil)
}

func (c *Client) loadMacaroon(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c.macaroon = hex.EncodeToString(data)
	return nil
}

func (c *Client) waitMacaroon(ctx context.Context, path string) error {
	for {
		err := c.loadMacaroon(path)
		if err == nil {
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

// ── on-chain ──────────────────────────────────────────────────────────────────

// NewAddressResponse represents a new address response.
type NewAddressResponse struct {
	// Address is the wallet or peer address used by the new address response.
	Address string `json:"address"`
}

// OnChainBalanceResponse contains LND wallet balance totals.
type OnChainBalanceResponse struct {
	// TotalBalance is the total balance value for the on chain balance response.
	TotalBalance string `json:"total_balance"`
	// ConfirmedBalance is the confirmed balance value for the on chain balance response.
	ConfirmedBalance string `json:"confirmed_balance"`
	// UnconfirmedBalance is the unconfirmed balance value for the on chain balance response.
	UnconfirmedBalance string `json:"unconfirmed_balance"`
}

// SendOnChainRequest represents a send on chain request.
type SendOnChainRequest struct {
	// Addr is the wallet or peer address used by the send on chain request.
	Addr string `json:"addr"`
	// Amount is the amount value for the send on chain request.
	Amount int64 `json:"amount"`
	// SatPerVbyte is the sat per vbyte value for the send on chain request.
	SatPerVbyte int64 `json:"sat_per_vbyte"`
}

// SendOnChainResponse represents a send on chain response.
type SendOnChainResponse struct {
	// Txid is the txid value for the send on chain response.
	Txid string `json:"txid"`
}

// NewAddress generates a new on-chain bech32 address.
func (c *Client) NewAddress(ctx context.Context) (string, error) {
	var resp NewAddressResponse
	if err := c.get(ctx, "/v1/newaddress?type=0", &resp); err != nil {
		return "", err
	}
	return resp.Address, nil
}

// OnChainBalance returns the on-chain wallet balance.
func (c *Client) OnChainBalance(ctx context.Context) (*OnChainBalanceResponse, error) {
	var resp OnChainBalanceResponse
	if err := c.get(ctx, "/v1/balance/blockchain", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Utxo is a single unspent output owned by the LND wallet.
type Utxo struct {
	// Address is the on-chain address paying to this output.
	Address string `json:"address"`
	// AmountSat is the output value in satoshis (LND REST encodes int64 as a string).
	AmountSat string `json:"amount_sat"`
	// Confirmations is the confirmation depth (LND REST encodes int64 as a string).
	Confirmations string `json:"confirmations"`
}

// ListUtxosResponse is the LND /v1/utxos response.
type ListUtxosResponse struct {
	// Utxos is the list of unspent outputs.
	Utxos []Utxo `json:"utxos"`
}

// ConfirmedBalanceForAddress returns the confirmed satoshi balance held by a single
// wallet address, summing the wallet's confirmed UTXOs that pay to it. Used by the
// second-leg worker to watch the hub address for an incoming CEX withdrawal.
func (c *Client) ConfirmedBalanceForAddress(ctx context.Context, address string) (int64, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return 0, errors.New("address is required")
	}
	var resp ListUtxosResponse
	// min_confs=1 → confirmed only; max_confs caps the range (LND requires it set).
	if err := c.get(ctx, "/v1/utxos?min_confs=1&max_confs=2147483647", &resp); err != nil {
		return 0, err
	}
	var total int64
	for _, u := range resp.Utxos {
		if !strings.EqualFold(strings.TrimSpace(u.Address), address) {
			continue
		}
		sat, err := strconv.ParseInt(strings.TrimSpace(u.AmountSat), 10, 64)
		if err != nil {
			continue
		}
		total += sat
	}
	return total, nil
}

// HealthCheck verifies that the wallet client can reach LND.
func (c *Client) HealthCheck(ctx context.Context) error {
	if c == nil {
		return errors.New("LND client is not initialized")
	}
	if _, err := c.OnChainBalance(ctx); err != nil {
		return fmt.Errorf("LND wallet health check failed: %w", err)
	}
	return nil
}

// SendOnChain sends BTC on-chain.
func (c *Client) SendOnChain(
	ctx context.Context,
	toAddress string,
	amountSat, satPerVbyte int64,
) (string, error) {
	req := SendOnChainRequest{
		Addr:        toAddress,
		Amount:      amountSat,
		SatPerVbyte: satPerVbyte,
	}
	var resp SendOnChainResponse
	if err := c.post(ctx, "/v1/transactions", req, &resp); err != nil {
		return "", err
	}
	return resp.Txid, nil
}

// ── lightning ─────────────────────────────────────────────────────────────────

// CreateInvoiceRequest represents a create invoice request.
type CreateInvoiceRequest struct {
	// ValueMsat is the amount in millisatoshis.
	ValueMsat int64 `json:"value_msat"`
	// Memo carries a human-readable message for the create invoice request.
	Memo string `json:"memo"`
	// Expiry is the expiry value for the create invoice request.
	Expiry int64 `json:"expiry"`
}

// CreateInvoiceResponse represents a create invoice response.
type CreateInvoiceResponse struct {
	// PaymentRequest is the payment request value for the create invoice response.
	PaymentRequest string `json:"payment_request"`
	// RHash is the rhash value for the create invoice response.
	RHash string `json:"r_hash"`
}

// PayInvoiceRequest represents a pay invoice request.
type PayInvoiceRequest struct {
	// PaymentRequest is the payment request value for the pay invoice request.
	PaymentRequest string `json:"payment_request"`
	// FeeLimitSat records the relevant time for the pay invoice request.
	FeeLimitSat int64 `json:"fee_limit_sat"`
}

// PayInvoiceResponse represents a pay invoice response.
type PayInvoiceResponse struct {
	// PaymentError is the payment error value for the pay invoice response.
	PaymentError string `json:"payment_error"`
	// PaymentPreimage is the payment preimage value for the pay invoice response.
	PaymentPreimage string `json:"payment_preimage"`
}

// DecodedInvoice is LND's decoded BOLT11 payment request.
type DecodedInvoice struct {
	Destination  string `json:"destination"`
	PaymentHash  string `json:"payment_hash"`
	NumSatoshis  string `json:"num_satoshis"`
	NumMSatoshis string `json:"num_msat"`
	Timestamp    string `json:"timestamp"`
	Expiry       string `json:"expiry"`
}

// ChannelBalanceResponse represents a channel balance response.
type ChannelBalanceResponse struct {
	// LocalBalance is the local balance value for the channel balance response.
	LocalBalance BalanceAmount `json:"local_balance"`
	// RemoteBalance is the remote balance value for the channel balance response.
	RemoteBalance BalanceAmount `json:"remote_balance"`
}

// BalanceAmount represents a balance amount.
type BalanceAmount struct {
	// Sat records the relevant time for the balance amount.
	Sat string `json:"sat"`
	// Msat is the amount in millisatoshis.
	Msat string `json:"msat"`
}

// CreateInvoice creates a Lightning invoice to receive a payment.
func (c *Client) CreateInvoice(
	ctx context.Context,
	amountMsat int64,
	memo string,
	expirySeconds int64,
) (*CreateInvoiceResponse, error) {
	if expirySeconds == 0 {
		expirySeconds = 3600
	}
	req := CreateInvoiceRequest{
		ValueMsat: amountMsat,
		Memo:      memo,
		Expiry:    expirySeconds,
	}
	var resp CreateInvoiceResponse
	if err := c.post(ctx, "/v1/invoices", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PayInvoice pays a BOLT11 Lightning invoice.
func (c *Client) PayInvoice(
	ctx context.Context,
	paymentRequest string,
	feeLimitSat int64,
) (*PayInvoiceResponse, error) {
	req := PayInvoiceRequest{
		PaymentRequest: paymentRequest,
		FeeLimitSat:    feeLimitSat,
	}
	var resp PayInvoiceResponse
	if err := c.post(ctx, "/v1/channels/transactions", req, &resp); err != nil {
		return nil, err
	}
	if resp.PaymentError != "" {
		return nil, fmt.Errorf("payment failed: %s", resp.PaymentError)
	}
	return &resp, nil
}

// DecodeInvoice asks LND to decode a BOLT11 without paying it.
func (c *Client) DecodeInvoice(ctx context.Context, paymentRequest string) (*DecodedInvoice, error) {
	paymentRequest = strings.TrimSpace(paymentRequest)
	if paymentRequest == "" {
		return nil, fmt.Errorf("payment request is required")
	}
	var resp DecodedInvoice
	if err := c.get(ctx, "/v1/payreq/"+url.PathEscape(paymentRequest), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ChannelBalance returns the Lightning channel balance.
func (c *Client) ChannelBalance(ctx context.Context) (*ChannelBalanceResponse, error) {
	var resp ChannelBalanceResponse
	if err := c.get(ctx, "/v1/balance/channels", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ── HTTP internals ────────────────────────────────────────────────────────────

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.host+path, bytes.NewReader(b),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) delete(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodDelete, c.host+path, nil,
	)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	if c.macaroon != "" {
		req.Header.Set("Grpc-Metadata-macaroon", c.macaroon)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return newLNDAPIError(req.Method, req.URL.Path, resp.StatusCode, rawBody)
	}

	if out != nil {
		if err := json.Unmarshal(rawBody, out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}

	return nil
}

// LNDAPIError is a failed LND REST call. It renders as a readable sentence
// (with a remediation hint for well-known failures) instead of dumping the
// raw JSON error body; the original LND message is kept in Detail and shown
// in brackets when it differs from the friendly message.
type LNDAPIError struct {
	Method     string
	Path       string
	StatusCode int
	Code       int
	Message    string // human-friendly description
	Detail     string // verbatim message from LND
}

func (e *LNDAPIError) Error() string {
	var b strings.Builder
	b.WriteString("LND request failed (")
	b.WriteString(e.Method)
	b.WriteString(" ")
	b.WriteString(e.Path)
	if e.StatusCode != 0 {
		fmt.Fprintf(&b, ", HTTP %d", e.StatusCode)
	}
	b.WriteString(")")
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Detail != "" && e.Detail != e.Message {
		fmt.Fprintf(&b, " [LND said: %s]", e.Detail)
	}
	return b.String()
}

// newLNDAPIError parses an LND REST error body ("code"/"message") and maps
// known failure modes to actionable operator guidance.
func newLNDAPIError(method, path string, status int, rawBody []byte) error {
	apiErr := &LNDAPIError{Method: method, Path: path, StatusCode: status}
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rawBody, &body); err == nil {
		apiErr.Code = body.Code
		apiErr.Detail = strings.TrimSpace(body.Message)
	} else if trimmed := strings.TrimSpace(string(rawBody)); trimmed != "" {
		apiErr.Detail = trimmed
	}

	switch {
	case apiErr.Detail == "":
		apiErr.Message = fmt.Sprintf("LND returned HTTP %d with no details", status)
	case strings.Contains(apiErr.Detail, "wallet locked"):
		apiErr.Message = "the LND wallet is locked; unlock it to enable RPC access — check that the wallet password is configured so LND can auto-unlock after a restart"
	case strings.Contains(strings.ToLower(apiErr.Detail), "permission") ||
		strings.Contains(strings.ToLower(apiErr.Detail), "macaroon"):
		apiErr.Message = "the macaroon was rejected; verify the configured macaroon grants the required permission"
	default:
		apiErr.Message = apiErr.Detail
	}
	return apiErr
}

func buildHTTPClient(tlsCertPath string) (*http.Client, error) {
	certData, err := os.ReadFile(tlsCertPath)
	if err != nil {
		return nil, fmt.Errorf("reading TLS cert: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certData) {
		return nil, errors.New("failed to parse LND TLS cert")
	}

	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}, nil
}
