package lnd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TrackHoldPayment returns the first current router status without waiting for
// settlement. NOT_FOUND is distinct from an unreachable or unavailable node.
func (c *Client) TrackHoldPayment(ctx context.Context, hash string) (string, error) {
	raw, err := hex.DecodeString(hash)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("invalid payment hash")
	}
	path := "/v2/router/track/" + url.PathEscape(base64.URLEncoding.EncodeToString(raw)) + "?no_inflight_updates=false"
	status, err := c.routerPayment(ctx, http.MethodGet, path, nil, hash)
	var api *LNDAPIError
	if errors.As(err, &api) && api.Code == 5 {
		return "NOT_FOUND", nil
	}
	return status, err
}

// HoldPayment reconciles by hash before submitting. Closing the REST observer
// does not cancel LND's payment; future calls track the same HTLC. FAILED is not
// automatically retried, and an unknown RPC result is never treated as absent.
func (c *Client) HoldPayment(ctx context.Context, invoice, hash string, feeSat int64) (string, error) {
	status, err := c.TrackHoldPayment(ctx, hash)
	if err != nil || status != "NOT_FOUND" {
		return status, err
	}
	if feeSat < 0 {
		return "", fmt.Errorf("negative routing fee limit")
	}
	raw, err := json.Marshal(map[string]any{"payment_request": invoice, "fee_limit_sat": strconv.FormatInt(feeSat, 10), "timeout_seconds": 60, "no_inflight_updates": false})
	if err != nil {
		return "", err
	}
	return c.routerPayment(ctx, http.MethodPost, "/v2/router/send", raw, hash)
}
func (c *Client) routerPayment(ctx context.Context, method, path string, body []byte, hash string) (string, error) {
	if c == nil || c.httpClient == nil {
		return "", fmt.Errorf("LND client is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.host, "/")+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Grpc-Metadata-macaroon", c.macaroon)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", newLNDAPIError(method, path, resp.StatusCode, b)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	for {
		var update struct {
			Result struct {
				Status string `json:"status"`
				Hash   string `json:"payment_hash"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		}
		if err := decoder.Decode(&update); err != nil {
			return "", fmt.Errorf("read LND payment status: %w", err)
		}
		if len(update.Error) > 0 && string(update.Error) != "null" {
			return "", newLNDAPIError(method, path, resp.StatusCode, update.Error)
		}
		if update.Result.Hash != hash {
			return "", fmt.Errorf("LND payment hash mismatch")
		}
		switch update.Result.Status {
		case "IN_FLIGHT", "SUCCEEDED", "FAILED":
			return update.Result.Status, nil
		case "INITIATED", "UNKNOWN":
			continue
		default:
			return "", fmt.Errorf("unexpected LND payment status")
		}
	}
}
