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
	"os"
	"strings"
	"time"
)

// Client is the LND REST client.
type Client struct {
	host       string
	macaroon   string
	httpClient *http.Client
}

type stateResponse struct {
	State string `json:"state"`
}

type unlockRequest struct {
	WalletPassword string `json:"wallet_password"` // base64
}

type initWalletRequest struct {
	WalletPassword                     string   `json:"wallet_password"` // base64
	StatelessInit                      bool     `json:"stateless_init"`
	CipherSeedMnemonic                 []string `json:"cipher_seed_mnemonic,omitempty"`
	AEZEEDPassphrase                   string   `json:"aezeed_passphrase,omitempty"` // base64
	RecoveryWindow                     int32    `json:"recovery_window,omitempty"`
	ExtendedMasterKey                  string   `json:"extended_master_key,omitempty"`
	ExtendedMasterKeyBirthdayTimestamp uint64   `json:"extended_master_key_birthday_timestamp,omitempty"`
}

type InitConfig struct {
	CipherSeedMnemonic                 []string
	AEZEEDPassphrase                   string
	RecoveryWindow                     int32
	ExtendedMasterKey                  string
	ExtendedMasterKeyBirthdayTimestamp uint64
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

	// 3. Wait until fully active.
	fmt.Println("lnd: waiting for SERVER_ACTIVE...")
	if err := c.waitState(ctx, "SERVER_ACTIVE"); err != nil {
		return nil, fmt.Errorf("waiting for SERVER_ACTIVE: %w", err)
	}

	fmt.Println("lnd: ready")
	return c, nil
}

// ── startup ───────────────────────────────────────────────────────────────────

func (c *Client) handleWalletState(
	ctx context.Context,
	macaroonPath, password string,
	initCfg InitConfig,
) error {
	for {
		state, err := c.getState(ctx)
		if err != nil {
			return fmt.Errorf("getting state: %w", err)
		}

		switch state {
		case "NON_EXISTING":
			fmt.Println("lnd: no wallet found, restoring from supplied wallet secret...")
			if err := initCfg.Validate(); err != nil {
				return err
			}
			if err := c.initWallet(ctx, password, initCfg); err != nil {
				return fmt.Errorf("initialising wallet: %w", err)
			}
			if err := c.loadMacaroon(macaroonPath); err != nil {
				return fmt.Errorf("loading macaroon after init: %w", err)
			}
			return nil

		case "LOCKED":
			fmt.Println("lnd: wallet locked, unlocking...")
			if err := c.unlockWallet(ctx, password); err != nil {
				return fmt.Errorf("unlocking wallet: %w", err)
			}
			if err := c.loadMacaroon(macaroonPath); err != nil {
				return fmt.Errorf("loading macaroon after unlock: %w", err)
			}
			return nil

		case "WAITING_TO_START":
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				continue
			}

		case "SERVER_ACTIVE", "RPC_ACTIVE":
			if err := c.loadMacaroon(macaroonPath); err != nil {
				return fmt.Errorf("loading macaroon: %w", err)
			}
			return nil

		default:
			return fmt.Errorf("unexpected LND state: %s", state)
		}
	}
}

func (cfg InitConfig) Validate() error {
	hasMnemonic := len(cfg.CipherSeedMnemonic) > 0
	hasMasterKey := strings.TrimSpace(cfg.ExtendedMasterKey) != ""
	if hasMnemonic == hasMasterKey {
		return errors.New("exactly one of cipher seed mnemonic or extended master key must be configured")
	}
	if hasMnemonic && len(cfg.CipherSeedMnemonic) != 24 {
		return fmt.Errorf("cipher seed mnemonic must contain 24 words, got %d", len(cfg.CipherSeedMnemonic))
	}
	return nil
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
	if cfg.ExtendedMasterKey != "" {
		req.ExtendedMasterKey = cfg.ExtendedMasterKey
		if cfg.ExtendedMasterKeyBirthdayTimestamp > 0 {
			req.ExtendedMasterKeyBirthdayTimestamp = cfg.ExtendedMasterKeyBirthdayTimestamp
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

func (c *Client) waitState(ctx context.Context, target string) error {
	for {
		state, err := c.getState(ctx)
		if err != nil {
			return err
		}
		if state == target {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			fmt.Printf("lnd: state=%s, waiting for %s...\n", state, target)
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

// ── on-chain ──────────────────────────────────────────────────────────────────

type NewAddressResponse struct {
	Address string `json:"address"`
}

type OnChainBalanceResponse struct {
	TotalBalance       string `json:"total_balance"`
	ConfirmedBalance   string `json:"confirmed_balance"`
	UnconfirmedBalance string `json:"unconfirmed_balance"`
}

type SendOnChainRequest struct {
	Addr        string `json:"addr"`
	Amount      int64  `json:"amount"`
	SatPerVbyte int64  `json:"sat_per_vbyte"`
}

type SendOnChainResponse struct {
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

type CreateInvoiceRequest struct {
	ValueMsat int64  `json:"value_msat"`
	Memo      string `json:"memo"`
	Expiry    int64  `json:"expiry"`
}

type CreateInvoiceResponse struct {
	PaymentRequest string `json:"payment_request"`
	RHash          string `json:"r_hash"`
}

type PayInvoiceRequest struct {
	PaymentRequest string `json:"payment_request"`
	FeeLimitSat    int64  `json:"fee_limit_sat"`
}

type PayInvoiceResponse struct {
	PaymentError    string `json:"payment_error"`
	PaymentPreimage string `json:"payment_preimage"`
}

type ChannelBalanceResponse struct {
	LocalBalance  BalanceAmount `json:"local_balance"`
	RemoteBalance BalanceAmount `json:"remote_balance"`
}

type BalanceAmount struct {
	Sat  string `json:"sat"`
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
		return fmt.Errorf("LND %s %s → %d: %s",
			req.Method, req.URL.Path, resp.StatusCode, rawBody)
	}

	if out != nil {
		if err := json.Unmarshal(rawBody, out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}

	return nil
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
