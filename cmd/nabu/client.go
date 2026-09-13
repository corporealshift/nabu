package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/corporealshift/nabu/protocol"
)

// rpcError is a JSON-RPC error object as it arrives from the daemon.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// message is anything the daemon sends: a response to a call, or a
// notification with no id.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// client is a thin JSON-RPC-over-WebSocket client. It speaks the protocol in
// protocol/spec.md and nothing more.
type client struct {
	conn *websocket.Conn

	mu     sync.Mutex
	nextID int
}

// dial connects to addr and completes the nabu.hello handshake.
func dial(ctx context.Context, addr, token string) (*client, error) {
	opts := &websocket.DialOptions{}
	if token != "" {
		opts.HTTPHeader = map[string][]string{"Authorization": {"Bearer " + token}}
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+addr, opts)
	if err != nil {
		return nil, fmt.Errorf("connecting to the daemon at %s: %w", addr, err)
	}
	c := &client{conn: conn}

	if err := c.send(ctx, message{
		JSONRPC: "2.0", ID: 0, Method: "nabu.hello",
		Params: mustJSON(map[string]any{
			"client":           "nabu-cli",
			"client_version":   version,
			"protocol_version": protocol.Version,
		}),
	}); err != nil {
		c.close()
		return nil, err
	}
	var hello message
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		c.close()
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if hello.Error != nil {
		c.close()
		return nil, fmt.Errorf("handshake refused: %w", hello.Error)
	}
	return c, nil
}

func (c *client) close() {
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *client) send(ctx context.Context, m message) error {
	return wsjson.Write(ctx, c.conn, m)
}

// call makes one request and waits for its response, passing any notification
// that arrives first to onNotify.
func (c *client) call(ctx context.Context, method string, params any, onNotify func(message)) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	m := message{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		m.Params = mustJSON(params)
	}
	if err := c.send(ctx, m); err != nil {
		return nil, err
	}

	for {
		var in message
		if err := wsjson.Read(ctx, c.conn, &in); err != nil {
			return nil, err
		}
		if in.Method != "" {
			if onNotify != nil {
				onNotify(in)
			}
			continue
		}
		if !sameID(in.ID, id) {
			continue
		}
		if in.Error != nil {
			return nil, in.Error
		}
		return in.Result, nil
	}
}

// stream reads notifications until the context ends or onNotify returns false.
func (c *client) stream(ctx context.Context, onNotify func(message) bool) error {
	for {
		var in message
		if err := wsjson.Read(ctx, c.conn, &in); err != nil {
			return err
		}
		if in.Method == "" {
			continue
		}
		if !onNotify(in) {
			return nil
		}
	}
}

// sameID compares a response id to the one sent. JSON numbers decode as
// float64, so an int is never equal to its own echo without this.
func sameID(got any, want int) bool {
	switch v := got.(type) {
	case float64:
		return int(v) == want
	case int:
		return v == want
	default:
		return false
	}
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return raw
}

// dialTimeout bounds a connection attempt so a wedged daemon cannot hang a
// command indefinitely.
const dialTimeout = 15 * time.Second
