package api

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/corporealshift/nabu/protocol"
)

// eventBuffer is how many events a subscription may fall behind before the
// session store drops it. Events are replayable from the log by cursor, so a
// client that falls behind resyncs rather than losing anything.
const eventBuffer = 64

// connState is one client connection. A WebSocket has a single writer, so
// every write goes through mu: the read loop, the event pump and the delta
// sink all share it.
type connState struct {
	conn Conn
	ctx  context.Context

	mu sync.Mutex
}

func (c *connState) write(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(c.ctx, v)
}

func (c *connState) notify(method string, params any) {
	_ = c.write(jsonrpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// fanout pumps one session's events to every connection subscribed to it.
// It exists only while at least one connection is subscribed.
type fanout struct {
	conns  map[*connState]bool
	cancel func()
}

// subscribe attaches cs to a session's event stream, starting the pump on the
// first subscriber.
func (h *Handler) subscribe(sessionID string, cs *connState) *protocol.RPCError {
	s, rpcErr := h.getSession(sessionID)
	if rpcErr != nil {
		return rpcErr
	}

	h.subMu.Lock()
	defer h.subMu.Unlock()

	if f, ok := h.fanouts[sessionID]; ok {
		f.conns[cs] = true
		return nil
	}

	ch, cancel := s.Subscribe(eventBuffer)
	f := &fanout{conns: map[*connState]bool{cs: true}, cancel: cancel}
	h.fanouts[sessionID] = f
	go h.pump(sessionID, ch)
	return nil
}

// pump forwards events to every current subscriber until the channel closes.
func (h *Handler) pump(sessionID string, ch <-chan protocol.Event) {
	for ev := range ch {
		for _, cs := range h.subscribers(sessionID) {
			cs.notify("nabu.session.event", map[string]any{
				"session_id": sessionID,
				"event":      ev,
			})
		}
	}
}

// subscribers snapshots the connections subscribed to a session, so writes
// happen without holding the registry lock.
func (h *Handler) subscribers(sessionID string) []*connState {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	f, ok := h.fanouts[sessionID]
	if !ok {
		return nil
	}
	out := make([]*connState, 0, len(f.conns))
	for cs := range f.conns {
		out = append(out, cs)
	}
	return out
}

// unsubscribe detaches cs, stopping the pump once the last subscriber leaves.
func (h *Handler) unsubscribe(sessionID string, cs *connState) {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	h.dropLocked(sessionID, cs)
}

func (h *Handler) dropLocked(sessionID string, cs *connState) {
	f, ok := h.fanouts[sessionID]
	if !ok {
		return
	}
	delete(f.conns, cs)
	if len(f.conns) == 0 {
		f.cancel()
		delete(h.fanouts, sessionID)
	}
}

// dropConn removes a connection from every session it was subscribed to. It
// runs when the connection ends, so a disconnect never leaks a pump.
func (h *Handler) dropConn(cs *connState) {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	for sessionID := range h.fanouts {
		h.dropLocked(sessionID, cs)
	}
}

// Deltas returns the DeltaSink to hand the agent. Deltas are ephemeral: they
// are broadcast to current subscribers and never logged or replayed.
func (h *Handler) Deltas() func(sessionID, turnID, text string) {
	return func(sessionID, turnID, text string) {
		for _, cs := range h.subscribers(sessionID) {
			cs.notify("nabu.session.delta", map[string]any{
				"session_id": sessionID,
				"turn_id":    turnID,
				"text":       text,
			})
		}
	}
}

// handleSessionSubscribe implements nabu.session.subscribe (spec 7.6).
func (h *Handler) handleSessionSubscribe(_ context.Context, cs *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if cs == nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, "subscribe requires a connection")
	}
	if rpcErr := h.subscribe(p.SessionID, cs); rpcErr != nil {
		return nil, rpcErr
	}
	return map[string]any{"subscribed": true}, nil
}

// handleSessionUnsubscribe implements nabu.session.unsubscribe (spec 7.6).
func (h *Handler) handleSessionUnsubscribe(_ context.Context, cs *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if cs != nil {
		h.unsubscribe(p.SessionID, cs)
	}
	return map[string]any{"subscribed": false}, nil
}
