package lnd

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Peer is a connected Lightning peer.
type Peer struct {
	PubKey   string `json:"pub_key"`
	Address  string `json:"address"`
	Inbound  bool   `json:"inbound"`
	SyncType string `json:"sync_type"`
}

type listPeersResponse struct {
	Peers []Peer `json:"peers"`
}

// ListPeers returns currently connected Lightning peers.
func (c *Client) ListPeers(ctx context.Context) ([]Peer, error) {
	var resp listPeersResponse
	if err := c.get(ctx, "/v1/peers", &resp); err != nil {
		return nil, err
	}
	return resp.Peers, nil
}

// WalletAddressProperty is an on-chain address returned by LND wallet kit.
type WalletAddressProperty struct {
	Address    string `json:"address"`
	IsInternal bool   `json:"is_internal"`
	Balance    string `json:"balance"`
}

// AccountWithAddresses groups addresses for a wallet account.
type AccountWithAddresses struct {
	Name      string                  `json:"name"`
	Addresses []WalletAddressProperty `json:"addresses"`
}

type listAddressesResponse struct {
	AccountWithAddresses []AccountWithAddresses `json:"account_with_addresses"`
}

// ListWalletAddresses returns on-chain addresses known to the LND wallet.
func (c *Client) ListWalletAddresses(ctx context.Context) ([]WalletAddressProperty, error) {
	var resp listAddressesResponse
	if err := c.get(ctx, "/v2/wallet/addresses", &resp); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var out []WalletAddressProperty
	for _, account := range resp.AccountWithAddresses {
		for _, addr := range account.Addresses {
			if addr.Address == "" {
				continue
			}
			if _, ok := seen[addr.Address]; ok {
				continue
			}
			seen[addr.Address] = struct{}{}
			out = append(out, addr)
		}
	}
	return out, nil
}

// GraphNode is public node metadata from the Lightning graph.
type GraphNode struct {
	Node struct {
		PubKey string `json:"pub_key"`
		Alias  string `json:"alias"`
		Color  string `json:"color"`
	} `json:"node"`
}

// GraphNode looks up a node in the Lightning graph by pubkey.
func (c *Client) GraphNode(ctx context.Context, pubKey string) (*GraphNode, error) {
	pubKey = strings.TrimSpace(pubKey)
	if pubKey == "" {
		return nil, fmt.Errorf("graph node pubkey is required")
	}
	var resp GraphNode
	path := "/v1/graph/node/" + url.PathEscape(pubKey)
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
