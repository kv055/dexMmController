package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type Client struct {
	url string

	connMu sync.Mutex
	conn   *websocket.Conn

	idSeq uint64

	pendingMu sync.Mutex
	pending   map[string]chan resMsg
}

func NewClient(url string) *Client {
	return &Client{
		url:     url,
		pending: make(map[string]chan resMsg),
	}
}

func (c *Client) Connect(ctx context.Context) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn != nil {
		return nil
	}

	conn, _, err := websocket.Dial(ctx, c.url, nil)
	if err != nil {
		return err
	}
	c.conn = conn

	go c.readLoop()
	return nil
}

func (c *Client) Close() error {
	c.connMu.Lock()
	conn := c.conn
	c.conn = nil
	c.connMu.Unlock()

	if conn == nil {
		return nil
	}

	// Close the websocket; readLoop will clean up pending requests.
	return conn.Close(websocket.StatusNormalClosure, "client close")
}

func (c *Client) readLoop() {
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn == nil {
		return
	}

	// If the connection dies, fail all pending callers.
	defer func() {
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			delete(c.pending, id)
			close(ch)
		}
		c.pendingMu.Unlock()
	}()

	for {
		var raw map[string]json.RawMessage
		err := wsjson.Read(context.Background(), conn, &raw)
		if err != nil {
			return
		}

		var typ string
		_ = json.Unmarshal(raw["type"], &typ)
		if typ != "res" {
			// This minimal client ignores "event" messages for now.
			continue
		}

		b, _ := json.Marshal(raw)
		var res resMsg
		if err := json.Unmarshal(b, &res); err != nil {
			continue
		}

		c.pendingMu.Lock()
		ch := c.pending[res.ID]
		if ch != nil {
			delete(c.pending, res.ID)
		}
		c.pendingMu.Unlock()
		if ch != nil {
			// Buffered channel; should never block.
			ch <- res
			close(ch)
		}
	}
}

func (c *Client) nextID() string {
	n := atomic.AddUint64(&c.idSeq, 1)
	return fmt.Sprintf("req-%d", n)
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn == nil {
		return errors.New("openclaw: not connected")
	}

	id := c.nextID()

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		paramsRaw = b
	}

	ch := make(chan resMsg, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	req := reqMsg{
		Type:   "req",
		ID:     id,
		Method: method,
		Params: paramsRaw,
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		// Remove from pending to avoid leaking. If a late response arrives, it is ignored.
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return ctx.Err()
	case res, ok := <-ch:
		if !ok {
			return errors.New("openclaw: connection closed")
		}
		if !res.OK {
			if res.Error != nil && res.Error.Message != "" {
				return fmt.Errorf("openclaw: %s", res.Error.Message)
			}
			return errors.New("openclaw: request failed")
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(res.Payload, out)
	}
}

// BankTxHasBeenSent asks the gateway once whether it has already sent the bank
// transaction with the given code to the CEX. Returns false (not an error) when it hasn't.
func (c *Client) BankTxHasBeenSentToCex(ctx context.Context, code string) (bool, error) {
	var payload BankTxArrivedPayload
	if err := c.Call(ctx, "bank.tx.arrived", BankTxArrivedParams{Code: code}, &payload); err != nil {
		return false, err
	}
	return payload.Arrived, nil
}

// PollBankTxArrivedFromDex periodically asks the (mock) claw gateway whether the bank
// transaction with the given code has arrived yet.
func (c *Client) PollBankTxArrivedFromDex(ctx context.Context, code string, interval time.Duration) (bool, error) {
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		var payload BankTxArrivedPayload
		err := c.Call(ctx, "bank.tx.arrived", BankTxArrivedParams{Code: code}, &payload)
		if err != nil {
			return false, err
		}
		if payload.Arrived {
			return true, nil
		}

		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-t.C:
		}
	}
}
