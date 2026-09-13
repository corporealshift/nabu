package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/corporealshift/nabu/daemon/agent"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

// methodFunc handles one JSON-RPC method. The context is the connection's, so
// a method that reaches the agent or the store is cancelled when the client
// goes away.
type methodFunc func(ctx context.Context, params json.RawMessage) (any, *protocol.RPCError)

// Handler implements ConnHandler and dispatches JSON-RPC method calls.
type Handler struct {
	manager *agent.Manager
	store   *session.Store
	log     *slog.Logger

	mu      sync.RWMutex
	methods map[string]methodFunc
}

// NewHandler creates a Handler with the read-only session methods registered.
func NewHandler(m *agent.Manager, st *session.Store, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{manager: m, store: st, log: log, methods: make(map[string]methodFunc)}
	h.register("nabu.session.list", h.handleSessionList)
	h.register("nabu.session.create", h.handleSessionCreate)
	h.register("nabu.session.events_after", h.handleSessionEventsAfter)
	h.register("nabu.session.state", h.handleSessionState)
	return h
}

// register adds a method to the dispatch table. Later tasks extend the table
// through this same seam.
func (h *Handler) register(name string, fn methodFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.methods[name] = fn
}

// ServeConn implements ConnHandler: read a message, dispatch it, reply.
func (h *Handler) ServeConn(ctx context.Context, conn Conn) error {
	for {
		var raw json.RawMessage
		if err := conn.ReadJSON(ctx, &raw); err != nil {
			return err
		}
		if resp := h.dispatch(ctx, raw); resp != nil {
			if err := conn.WriteJSON(ctx, resp); err != nil {
				return err
			}
		}
	}
}

// dispatch parses one JSON-RPC request and returns the response envelope, or
// nil for a notification, which by JSON-RPC 2.0 gets no reply.
func (h *Handler) dispatch(ctx context.Context, raw json.RawMessage) *jsonrpcResponse {
	var req jsonrpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return errorResp(nil, protocol.CodeParseError, "parse error")
	}
	if req.JSONRPC != "2.0" {
		return errorResp(req.ID, protocol.CodeInvalidRequest, "jsonrpc must be \"2.0\"")
	}
	if req.Method == "" {
		return errorResp(req.ID, protocol.CodeInvalidRequest, "method is required")
	}

	h.mu.RLock()
	fn, ok := h.methods[req.Method]
	h.mu.RUnlock()
	if !ok {
		return errorResp(req.ID, protocol.CodeMethodNotFound, req.Method)
	}

	result, rpcErr := fn(ctx, req.Params)

	// A request without an id is a notification: act on it, answer nothing.
	if req.ID == nil {
		return nil
	}
	if rpcErr != nil {
		return errorResp(req.ID, rpcErr.Code, rpcErr.Message)
	}
	return &jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

// jsonrpcRequest is an incoming JSON-RPC request or notification.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC response envelope.
type jsonrpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

// jsonrpcNotification is a server-to-client message with no id and no reply.
type jsonrpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func errorResp(id any, code int, message string) *jsonrpcResponse {
	return &jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
}

// decodeParams unmarshals params into v, reporting invalid params as an RPC
// error rather than an internal one.
func decodeParams(params json.RawMessage, v any) *protocol.RPCError {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return protocol.NewRPCError(protocol.CodeInvalidParams, "invalid params: "+err.Error())
	}
	return nil
}

// getSession resolves a session id, mapping an unknown id to the spec's
// session-not-found code.
func (h *Handler) getSession(id string) (*session.Session, *protocol.RPCError) {
	if strings.TrimSpace(id) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "session_id is required")
	}
	s, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, protocol.NewRPCError(protocol.CodeSessionNotFound, id)
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}
	return s, nil
}

// handleSessionList implements nabu.session.list (spec 7.2).
func (h *Handler) handleSessionList(context.Context, json.RawMessage) (any, *protocol.RPCError) {
	summaries, err := h.store.List()
	if err != nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}
	if summaries == nil {
		summaries = []session.Summary{}
	}
	return map[string]any{"sessions": summaries}, nil
}

// CreateSessionOptions are the optional parameters of nabu.session.create.
type CreateSessionOptions struct {
	Model             string `json:"model,omitempty"`
	PermissionMode    string `json:"permission_mode,omitempty"`
	CompactionEnabled *bool  `json:"compaction_enabled,omitempty"`
}

// handleSessionCreate implements nabu.session.create (spec 7.3).
func (h *Handler) handleSessionCreate(ctx context.Context, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		Workspace string                `json:"workspace"`
		Options   *CreateSessionOptions `json:"options,omitempty"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(p.Workspace) == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "workspace is required")
	}

	var co agent.CreateOptions
	if p.Options != nil {
		co.Model = p.Options.Model
		co.PermissionMode = protocol.PermissionMode(p.Options.PermissionMode)
		co.CompactionEnabled = p.Options.CompactionEnabled
	}

	s, err := h.manager.Create(ctx, p.Workspace, co)
	if err != nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}

	events := s.Events()
	if len(events) == 0 {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, "created session has no events")
	}
	return map[string]any{"session_id": s.ID(), "event": events[0]}, nil
}

// handleSessionEventsAfter implements nabu.session.events_after (spec 7.5, 4).
func (h *Handler) handleSessionEventsAfter(_ context.Context, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID   string  `json:"session_id"`
		LastEventID *string `json:"last_event_id"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	s, rpcErr := h.getSession(p.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}

	events, synced, err := s.EventsAfter(p.LastEventID)
	if err != nil {
		var known *protocol.RPCError
		if errors.As(err, &known) {
			return nil, known
		}
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}
	if events == nil {
		events = []protocol.Event{}
	}
	return map[string]any{"events": events, "synced": synced}, nil
}

// handleSessionState implements nabu.session.state (spec 7.10, 5). The State
// projection carries the spec's field names in its own JSON tags, so it is
// returned whole rather than copied into a map that could drift from it.
func (h *Handler) handleSessionState(_ context.Context, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	s, rpcErr := h.getSession(p.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return s.State(), nil
}
