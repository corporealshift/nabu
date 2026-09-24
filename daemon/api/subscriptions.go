package api

import (
	"context"
	"encoding/json"

	"github.com/corporealshift/nabu/protocol"
)

// eventBuffer is how many events a subscription may fall behind before the
// session store drops it. Events are replayable from the log by cursor, so a
// client that falls behind resyncs rather than losing anything.
const eventBuffer = 64

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
	go h.pump(sessionID, f, ch)
	return nil
}

// pump forwards events to every current subscriber until the channel closes.
func (h *Handler) pump(sessionID string, f *fanout, ch <-chan protocol.Event) {
	for ev := range ch {
		for _, cs := range h.subscribers(sessionID) {
			cs.notify("nabu.session.event", map[string]any{
				"session_id": sessionID,
				"event":      ev,
			})
		}
	}
	h.pumpEnded(sessionID, f)
}

// pumpEnded forgets a fan-out whose channel the store closed while it still
// had subscribers. Left registered, it was a dead stream that every later
// subscriber joined and heard nothing from (issue 110).
//
// The store closes a channel for two reasons. The session was archived, and
// its subscriptions end with it (spec 7.19). Or the pump fell behind, and its
// subscribers have missed events: they are dropped, and resync from their
// cursors when they reconnect.
func (h *Handler) pumpEnded(sessionID string, f *fanout) {
	h.subMu.Lock()
	if h.fanouts[sessionID] != f {
		// The last subscriber left and cancelled it; nothing is registered.
		h.subMu.Unlock()
		return
	}
	delete(h.fanouts, sessionID)
	conns := f.conns
	h.subMu.Unlock()

	if _, rpcErr := h.getSession(sessionID); rpcErr != nil {
		return
	}
	h.log.Warn("a session's event stream fell behind; dropping its subscribers to resync",
		"session", sessionID, "subscribers", len(conns))
	for cs := range conns {
		cs.drop()
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

// Thinking returns the sink for the model's reasoning. Ephemeral like a
// delta: the thinking event is what persists.
func (h *Handler) Thinking() func(sessionID, turnID, text string) {
	return func(sessionID, turnID, text string) {
		for _, cs := range h.subscribers(sessionID) {
			cs.notify("nabu.session.thinking", map[string]any{
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
	if rpcErr := h.subscribeAndCatchUp(p.SessionID, cs); rpcErr != nil {
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
