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

// MaxMessageBytes is the largest message either side will read. A session log
// replay is the big one: events_after returns everything since the cursor in a
// single message, so this has to clear a long session's catch-up, not just one
// event.
const MaxMessageBytes = 64 << 20

// Client is a connection to the daemon.
//
// One goroutine owns the socket. The websocket library forbids concurrent
// reads and concurrent writes, and a TUI does both: it streams events on one
// goroutine while sending prompts and permission answers from another. Two
// readers or two writers corrupt the frame stream rather than failing
// cleanly, so reads happen in readLoop alone and writes hold writeMu.
type Client struct {
	conn *websocket.Conn

	// writeMu serialises writes. Every write goes through it.
	writeMu sync.Mutex

	mu     sync.Mutex
	nextID int
	// waiting maps a call id to the channel its response is delivered on.
	waiting map[int]chan Message
	// pending holds notifications and daemon requests until Stream takes
	// them. Dropping them loses events and permission prompts.
	pending []Message

	// signal wakes Stream when pending grows. Buffered, so readLoop never
	// blocks on a Stream that is busy.
	signal chan struct{}
	// done closes when the reader stops; readErr says why.
	done    chan struct{}
	readErr error
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
	// The library defaults to 32 KiB, which is exactly the bash tool's output
	// cap, so a maximum-size tool result plus its envelope always exceeded it
	// and took the connection down with it. Bounded rather than unlimited: a
	// buggy peer should not be able to exhaust memory.
	conn.SetReadLimit(MaxMessageBytes)

	c := &Client{
		conn:    conn,
		waiting: map[int]chan Message{},
		signal:  make(chan struct{}, 1),
		done:    make(chan struct{}),
	}

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

	// The handshake is the last read anyone else does; from here the reader
	// owns the socket.
	go c.readLoop()
	return c, nil
}

// readLoop is the only reader. It routes responses to the call waiting for
// them and queues everything else for Stream.
func (c *Client) readLoop() {
	for {
		var m Message
		if err := wsjson.Read(context.Background(), c.conn, &m); err != nil {
			c.mu.Lock()
			c.readErr = err
			for _, ch := range c.waiting {
				close(ch)
			}
			c.waiting = map[int]chan Message{}
			c.mu.Unlock()
			close(c.done)
			return
		}
		if m.Method == "" {
			c.deliver(m)
			continue
		}
		c.queue(m)
	}
}

// deliver hands a response to the call waiting on its id. A response nobody
// is waiting for is dropped: the caller gave up, which is not an error.
func (c *Client) deliver(m Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.waiting {
		if sameID(m.ID, id) {
			ch <- m
			delete(c.waiting, id)
			return
		}
	}
}

// err reports why the reader stopped.
func (c *Client) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return c.readErr
	}
	return fmt.Errorf("the connection closed")
}

// Close ends the connection.
func (c *Client) Close() {
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) write(ctx context.Context, m Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return wsjson.Write(ctx, c.conn, m)
}

// Call makes one request and waits for its response. Anything arriving first —
// a notification, or a request from the daemon — is handed to onOther, so a
// caller never loses a permission prompt while waiting for a reply.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch := make(chan Message, 1)
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.waiting[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
	}()

	m := Message{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		m.Params = mustJSON(params)
	}
	if err := c.write(ctx, m); err != nil {
		return nil, err
	}

	select {
	case in, ok := <-ch:
		if !ok {
			return nil, c.err()
		}
		if in.Error != nil {
			return nil, in.Error
		}
		return in.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.err()
	}
}

// queue holds a message for Stream and wakes it.
func (c *Client) queue(m Message) {
	c.mu.Lock()
	c.pending = append(c.pending, m)
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
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
	raw, err := c.Call(ctx, method, params)
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
	for {
		// Anything that arrived while an earlier call was waiting comes
		// first, in order. Without this, subscribing and then streaming loses
		// every event that landed in between, including the state change that
		// ends a run.
		batch := c.drain()
		for _, m := range batch {
			if !onMessage(m) {
				return nil
			}
		}
		if len(batch) > 0 {
			continue
		}
		select {
		case <-c.signal:
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			// Deliver whatever the reader queued before it stopped.
			for _, m := range c.drain() {
				if !onMessage(m) {
					return nil
				}
			}
			return c.err()
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
		map[string]any{"session_id": sessionID})
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

// ParseThinking reads a thinking notification. It carries the same shape as a
// delta because it is the same kind of thing: ephemeral, superseded by the
// thinking event that follows it.
func ParseThinking(m Message) (SessionDelta, bool) {
	if m.Method != "nabu.session.thinking" {
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
