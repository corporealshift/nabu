// Package goclient is the Go client for the nabu daemon: JSON-RPC 2.0 over
// WebSocket, speaking the protocol in protocol/spec.md. The CLI and the TUI
// both use it, so there is one implementation rather than two that drift.
package goclient

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

// DialTimeout bounds a connection attempt, so a wedged daemon cannot hang a
// caller indefinitely.
const DialTimeout = 15 * time.Second

// RPCError is a JSON-RPC error returned by the daemon.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// Message is anything on the wire: a response to a call, a notification with
// no id, or a request the daemon wants answered.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsNotification reports whether m is a notification: a method with no id,
// which expects no reply.
func (m Message) IsNotification() bool { return m.Method != "" && m.ID == nil }

// IsRequest reports whether m is a request from the daemon, which must be
// answered with Respond.
func (m Message) IsRequest() bool { return m.Method != "" && m.ID != nil }

// Client is a connection to the daemon.
type Client struct {
	conn *websocket.Conn

	mu     sync.Mutex
	nextID int
	// pending holds notifications and daemon requests that arrived while a
	// call was waiting for its response. Dropping them loses events and
	// permission prompts, so they are queued and delivered by Stream.
	pending []Message
}

// Dial connects to addr and completes the nabu.hello handshake. A non-empty
// token is presented as a bearer credential, which the daemon requires from
// non-loopback addresses.
func Dial(ctx context.Context, addr, token, clientName, version string) (*Client, error) {
	opts := &websocket.DialOptions{}
	if token != "" {
		opts.HTTPHeader = map[string][]string{"Authorization": {"Bearer " + token}}
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+addr, opts)
	if err != nil {
		return nil, fmt.Errorf("connecting to the daemon at %s: %w", addr, err)
	}
	c := &Client{conn: conn}

	if err := c.write(ctx, Message{
		JSONRPC: "2.0", ID: 0, Method: "nabu.hello",
		Params: mustJSON(map[string]any{
			"client":           clientName,
			"client_version":   version,
			"protocol_version": protocol.Version,
		}),
	}); err != nil {
		c.Close()
		return nil, err
	}
	var hello Message
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		c.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if hello.Error != nil {
		c.Close()
		return nil, fmt.Errorf("handshake refused: %w", hello.Error)
	}
	return c, nil
}

// Close ends the connection.
func (c *Client) Close() {
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) write(ctx context.Context, m Message) error {
	return wsjson.Write(ctx, c.conn, m)
}

// Read returns the next message from the daemon. Callers that also make calls
// should use Call instead, which routes responses for them.
func (c *Client) Read(ctx context.Context) (Message, error) {
	var m Message
	err := wsjson.Read(ctx, c.conn, &m)
	return m, err
}

// Call makes one request and waits for its response. Anything arriving first —
// a notification, or a request from the daemon — is handed to onOther, so a
// caller never loses a permission prompt while waiting for a reply.
func (c *Client) Call(ctx context.Context, method string, params any, onOther func(Message)) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	m := Message{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		m.Params = mustJSON(params)
	}
	if err := c.write(ctx, m); err != nil {
		return nil, err
	}

	for {
		in, err := c.Read(ctx)
		if err != nil {
			return nil, err
		}
		if in.Method != "" {
			// Not the response we are waiting for. Hand it over if the caller
			// wants it now, and queue it either way so Stream still sees it.
			if onOther != nil {
				onOther(in)
			} else {
				c.queue(in)
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

// queue holds a message for Stream to deliver later.
func (c *Client) queue(m Message) {
	c.mu.Lock()
	c.pending = append(c.pending, m)
	c.mu.Unlock()
}

// drain takes everything queued during earlier calls.
func (c *Client) drain() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.pending
	c.pending = nil
	return out
}

// CallInto makes a call and decodes its result into out, which may be nil.
func (c *Client) CallInto(ctx context.Context, method string, params, out any) error {
	raw, err := c.Call(ctx, method, params, nil)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Stream reads messages until the context ends, onMessage returns false, or
// the connection drops.
func (c *Client) Stream(ctx context.Context, onMessage func(Message) bool) error {
	// Anything that arrived while an earlier call was waiting comes first, in
	// order. Without this, subscribing and then streaming loses every event
	// that landed in between — including the state change that ends a run.
	for _, m := range c.drain() {
		if !onMessage(m) {
			return nil
		}
	}
	for {
		in, err := c.Read(ctx)
		if err != nil {
			return err
		}
		if in.Method == "" {
			continue // a response to a call nobody is waiting for
		}
		if !onMessage(in) {
			return nil
		}
	}
}

// Respond answers a request the daemon sent, such as a permission prompt. The
// id must be the one from the request.
func (c *Client) Respond(ctx context.Context, id any, result any) error {
	return c.write(ctx, Message{JSONRPC: "2.0", ID: id, Result: mustJSON(result)})
}

// Subscribe asks for a session's events and deltas.
func (c *Client) Subscribe(ctx context.Context, sessionID string) error {
	_, err := c.Call(ctx, "nabu.session.subscribe",
		map[string]any{"session_id": sessionID}, nil)
	return err
}

// EventsAfter fetches the events a client has not seen. A nil cursor returns
// the whole log. This is how a reconnecting client catches up.
func (c *Client) EventsAfter(ctx context.Context, sessionID string, lastEventID *string) ([]protocol.Event, bool, error) {
	params := map[string]any{"session_id": sessionID}
	if lastEventID != nil {
		params["last_event_id"] = *lastEventID
	}
	var out struct {
		Events []protocol.Event `json:"events"`
		Synced bool             `json:"synced"`
	}
	if err := c.CallInto(ctx, "nabu.session.events_after", params, &out); err != nil {
		return nil, false, err
	}
	return out.Events, out.Synced, nil
}

// State fetches a session's current projection.
func (c *Client) State(ctx context.Context, sessionID string) (protocol.State, error) {
	var st protocol.State
	err := c.CallInto(ctx, "nabu.session.state",
		map[string]any{"session_id": sessionID}, &st)
	return st, err
}

// SessionSummary is one row of nabu.session.list.
type SessionSummary struct {
	SessionID string `json:"session_id"`
	Workspace string `json:"workspace"`
	State     string `json:"state"`
}

// List fetches the sessions the daemon knows about.
func (c *Client) List(ctx context.Context) ([]SessionSummary, error) {
	var out struct {
		Sessions []SessionSummary `json:"sessions"`
	}
	err := c.CallInto(ctx, "nabu.session.list", nil, &out)
	return out.Sessions, err
}

// PermissionRequest is the payload of nabu.rpc.permission.request.
type PermissionRequest struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Tool      string `json:"tool"`
	Summary   string `json:"summary"`
	Risk      string `json:"risk"`
}

// AskRequest is the payload of nabu.rpc.ui.ask.
type AskRequest struct {
	SessionID string   `json:"session_id"`
	RequestID string   `json:"request_id"`
	Question  string   `json:"question"`
	Choices   []string `json:"choices,omitempty"`
}

// ParsePermission reads a permission request out of a daemon message.
func ParsePermission(m Message) (PermissionRequest, bool) {
	if m.Method != "nabu.rpc.permission.request" {
		return PermissionRequest{}, false
	}
	var p PermissionRequest
	if json.Unmarshal(m.Params, &p) != nil {
		return PermissionRequest{}, false
	}
	return p, true
}

// ParseAsk reads a ui.ask request out of a daemon message.
func ParseAsk(m Message) (AskRequest, bool) {
	if m.Method != "nabu.rpc.ui.ask" {
		return AskRequest{}, false
	}
	var p AskRequest
	if json.Unmarshal(m.Params, &p) != nil {
		return AskRequest{}, false
	}
	return p, true
}

// AnswerPermission replies to a permission request. A denial should say why;
// the model sees the reason as the tool result.
func (c *Client) AnswerPermission(ctx context.Context, id any, approve bool, reason string) error {
	verdict := "deny"
	if approve {
		verdict = "approve"
	}
	return c.Respond(ctx, id, map[string]any{"verdict": verdict, "reason": reason})
}

// AnswerAsk replies to a ui.ask request.
func (c *Client) AnswerAsk(ctx context.Context, id any, answer string) error {
	return c.Respond(ctx, id, map[string]any{"answer": answer})
}

// SessionEvent is the payload of a nabu.session.event notification.
type SessionEvent struct {
	SessionID string         `json:"session_id"`
	Event     protocol.Event `json:"event"`
}

// SessionDelta is the payload of a nabu.session.delta notification. Deltas are
// ephemeral: never logged, never replayed.
type SessionDelta struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
	Text      string `json:"text"`
}

// ParseEvent reads a session event notification.
func ParseEvent(m Message) (SessionEvent, bool) {
	if m.Method != "nabu.session.event" {
		return SessionEvent{}, false
	}
	var p SessionEvent
	if json.Unmarshal(m.Params, &p) != nil {
		return SessionEvent{}, false
	}
	return p, true
}

// ParseDelta reads a delta notification.
func ParseDelta(m Message) (SessionDelta, bool) {
	if m.Method != "nabu.session.delta" {
		return SessionDelta{}, false
	}
	var p SessionDelta
	if json.Unmarshal(m.Params, &p) != nil {
		return SessionDelta{}, false
	}
	return p, true
}

// sameID compares a response id to the one sent. JSON numbers decode as
// float64, so an int never equals its own echo without this.
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
