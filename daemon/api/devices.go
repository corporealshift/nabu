package api

import (
	"context"
	"encoding/json"

	"github.com/corporealshift/nabu/protocol"
)

// Notify is what the API tells the phone notifier
// (docs/specs/2026-10-06-phone-notifications-design.md): the devices that
// register, and each daemon-to-client request as it opens and closes. The
// interface keeps daemon/notify out of this package's imports.
type Notify interface {
	Register(token, name string) error
	Unregister(token string) error
	RequestOpened(sessionID, requestID, method string)
	RequestClosed(sessionID, requestID string)
}

// handleDeviceRegister implements nabu.device.register (spec 7.23).
func (h *Handler) handleDeviceRegister(_ context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Token == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "token is required")
	}
	if h.Notify == nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, "this daemon keeps no devices")
	}
	if err := h.Notify.Register(p.Token, p.Name); err != nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}
	return map[string]any{}, nil
}

// handleDeviceUnregister implements nabu.device.unregister (spec 7.24).
func (h *Handler) handleDeviceUnregister(_ context.Context, _ *connState, params json.RawMessage) (any, *protocol.RPCError) {
	var p struct {
		Token string `json:"token"`
	}
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Token == "" {
		return nil, protocol.NewRPCError(protocol.CodeInvalidParams, "token is required")
	}
	if h.Notify == nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, "this daemon keeps no devices")
	}
	if err := h.Notify.Unregister(p.Token); err != nil {
		return nil, protocol.NewRPCError(protocol.CodeInternalError, err.Error())
	}
	return map[string]any{}, nil
}
