package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/corporealshift/nabu/protocol"
)

// rpcErrOf converts a manager error into an RPC error, preserving one the
// manager already chose (invalid transition, session not found) and treating
// anything else as internal.
func rpcErrOf(err error) *protocol.RPCError {
	if err == nil {
		return nil
	}
	var known *protocol.RPCError
	if errors.As(err, &known) {
		return known
	}
	return protocol.NewRPCError(protocol.CodeInternalError, err.Error())
}

// sessionParams is the shape of every method that names only a session.
type sessionParams struct {
	SessionID string `json:"session_id"`
}

// sessionID decodes and validates the session id shared by most methods.
func (h *Handler) sessionID(params json.RawMessage) (string, *protocol.RPCError) {
	var p sessionParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return "", rpcErr
	}
	if _, rpcErr := h.getSession(p.SessionID); rpcErr != nil {
		return "", rpcErr
	}
	return p.SessionID, nil
}

// registerMutators adds the methods that change a session.
func (h *Handler) registerMutators() {
	h.register("nabu.session.send_prompt", h.handleSendPrompt)
	h.register("nabu.session.interrupt", h.handleInterrupt)
	h.register("nabu.session.stop", h.handleStop)
	h.register("nabu.session.resume", h.handleResume)
	h.register("nabu.session.set_goal", h.handleSetGoal)
	h.register("nabu.session.clear_goal", h.handleClearGoal)
	h.register("nabu.session.set_option", h.handleSetOption)
	h.register("nabu.session.update_tasks", h.handleUpdateTasks)
}

// handleSendPrompt implements nabu.session.send_prompt (spec 7.4). A prompt
// sent while running is not queued: the message is appended immediately and
// the loop picks it up at the next turn boundary, because every request is
// assembled from the log.
//
// An optional client_id makes the call idempotent, which is what an offline
// outbox needs: a retry after a dropped connection returns the original event
// rather than appending the prompt a second time.
func (h *Handler) handleSendPrompt(ctx context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
		Content   string `json:"content"`
		// ClientID lets a client with an outbox retry safely: the same id
		// returns the event the first attempt produced.
		ClientID string `json:"client_id"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Content == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "content is required")
	}
	if _, rpcErr := h.getSession(p.SessionID); rpcErr != nil {
		return nil, rpcErr
	}
	ev, err := h.manager.PromptWithID(ctx, p.SessionID, p.Content, p.ClientID)
	if err != nil {
		return nil, rpcErrOf(err)
	}
	return map[string]any{"event_id": ev.ID}, nil
}

// handleInterrupt implements nabu.session.interrupt (spec 7.7).
func (h *Handler) handleInterrupt(ctx context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	id, rpcErr := h.sessionID(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if err := h.manager.Interrupt(ctx, id); err != nil {
		return nil, rpcErrOf(err)
	}
	return map[string]any{}, nil
}

// handleStop implements nabu.session.stop (spec 7.8).
func (h *Handler) handleStop(ctx context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	id, rpcErr := h.sessionID(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if err := h.manager.Stop(ctx, id); err != nil {
		return nil, rpcErrOf(err)
	}
	return map[string]any{}, nil
}

// handleResume implements nabu.session.resume (spec 7.9).
func (h *Handler) handleResume(ctx context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string               `json:"session_id"`
		Budget    *protocol.BudgetData `json:"budget,omitempty"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if _, rpcErr := h.getSession(p.SessionID); rpcErr != nil {
		return nil, rpcErr
	}
	if err := h.manager.Resume(ctx, p.SessionID, p.Budget); err != nil {
		return nil, rpcErrOf(err)
	}
	return map[string]any{}, nil
}

// handleSetGoal implements nabu.session.set_goal (spec 7.11).
func (h *Handler) handleSetGoal(ctx context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
		Condition string `json:"condition"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Condition == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "condition is required")
	}
	if _, rpcErr := h.getSession(p.SessionID); rpcErr != nil {
		return nil, rpcErr
	}
	ev, err := h.manager.SetGoal(ctx, p.SessionID, p.Condition)
	if err != nil {
		return nil, rpcErrOf(err)
	}
	return map[string]any{"event_id": ev.ID}, nil
}

// handleClearGoal implements nabu.session.clear_goal (spec 7.12).
func (h *Handler) handleClearGoal(ctx context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	id, rpcErr := h.sessionID(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ev, err := h.manager.ClearGoal(ctx, id)
	if err != nil {
		return nil, rpcErrOf(err)
	}
	return map[string]any{"event_id": ev.ID}, nil
}

// handleSetOption implements nabu.session.set_option (spec 7.13).
func (h *Handler) handleSetOption(ctx context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
		Key       string `json:"key"`
		Value     any    `json:"value"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Key == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "key is required")
	}
	if _, rpcErr := h.getSession(p.SessionID); rpcErr != nil {
		return nil, rpcErr
	}
	ev, err := h.manager.SetOption(ctx, p.SessionID, p.Key, p.Value)
	if err != nil {
		return nil, rpcErrOf(err)
	}
	return map[string]any{"event_id": ev.ID}, nil
}

// handleUpdateTasks implements nabu.session.update_tasks (spec 7.14). Edits
// from a client are appended with source "client".
func (h *Handler) handleUpdateTasks(ctx context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string          `json:"session_id"`
		Tasks     []protocol.Task `json:"tasks"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if _, rpcErr := h.getSession(p.SessionID); rpcErr != nil {
		return nil, rpcErr
	}
	if _, err := h.manager.UpdateTasksByID(ctx, p.SessionID, p.Tasks); err != nil {
		return nil, rpcErrOf(err)
	}
	s, rpcErr := h.getSession(p.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return map[string]any{"event_id": s.State().LastEventID}, nil
}

// handleDaemonStop implements nabu.daemon.stop.
//
// This method is NOT in protocol/spec.md 7. Spec 8 requires that
// "nabu daemon stop performs the graceful shutdown" and defines no mechanism
// for it, and signalling by pid is not gracefully portable to Windows, which
// is nabu's primary target. The spec's method list needs this added.
func (h *Handler) handleDaemonStop(context.Context, *connState, json.RawMessage) (any, *protocol.RPCError) {
	if h.OnShutdown == nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, "this daemon cannot stop itself")
	}
	// Shut down after replying, so the caller sees the acknowledgement rather
	// than a dropped connection.
	go h.OnShutdown()
	return map[string]any{"stopping": true}, nil
}
