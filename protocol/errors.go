package protocol

// JSON-RPC error codes (spec §8). Names travel in error.data.name.
const (
	CodeParseError         = -32700
	CodeInvalidRequest     = -32600
	CodeMethodNotFound     = -32601
	CodeInvalidParams      = -32602
	CodeInternalError      = -32603
	CodeSessionNotFound    = -32001
	CodeInvalidTransition  = -32002
	CodePermissionDenied   = -32003
	CodeAlreadyResolved    = -32004
	CodeCursorUnknown      = -32005
	CodeProtocolMismatch   = -32006
	CodeUnauthorized       = -32007
	CodeWorkspaceUntrusted = -32008
)

// ErrorNames maps each code to its spec name.
var ErrorNames = map[int]string{
	CodeParseError:         "parse_error",
	CodeInvalidRequest:     "invalid_request",
	CodeMethodNotFound:     "method_not_found",
	CodeInvalidParams:      "invalid_params",
	CodeInternalError:      "internal_error",
	CodeSessionNotFound:    "rho_session_not_found",
	CodeInvalidTransition:  "rho_invalid_transition",
	CodePermissionDenied:   "rho_permission_denied",
	CodeAlreadyResolved:    "rho_already_resolved",
	CodeCursorUnknown:      "rho_cursor_unknown",
	CodeProtocolMismatch:   "rho_protocol_mismatch",
	CodeUnauthorized:       "rho_unauthorized",
	CodeWorkspaceUntrusted: "rho_workspace_untrusted",
}

// RPCError is a JSON-RPC error object with rho's name in data.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    *struct {
		Name string `json:"name"`
	} `json:"data,omitempty"`
}

// NewRPCError builds an RPCError for a known code.
func NewRPCError(code int, message string) *RPCError {
	e := &RPCError{Code: code, Message: message}
	if name, ok := ErrorNames[code]; ok {
		e.Data = &struct {
			Name string `json:"name"`
		}{Name: name}
	}
	return e
}

func (e *RPCError) Error() string {
	if e.Data != nil {
		return e.Data.Name + ": " + e.Message
	}
	return e.Message
}

// Methods lists every JSON-RPC method name (spec §7), including notifications
// and daemon-to-client requests.
var Methods = []string{
	"rho.hello",
	"rho.session.list",
	"rho.workspace.browse",
	"rho.workspace.create_directory",
	"rho.session.create",
	"rho.session.send_prompt",
	"rho.session.events_after",
	"rho.session.subscribe",
	"rho.session.unsubscribe",
	"rho.session.interrupt",
	"rho.session.stop",
	"rho.session.resume",
	"rho.session.state",
	"rho.session.set_goal",
	"rho.session.clear_goal",
	"rho.session.set_option",
	"rho.session.update_tasks",
	"rho.session.compact",
	"rho.session.archive",
	"rho.session.restore",
	"rho.session.stats",
	"rho.usage",
	"rho.stats",
	"rho.stats.calls",
	"rho.device.register",
	"rho.device.unregister",
	"rho.session.set_status",
	"rho.session.event",
	"rho.session.delta",
	"rho.session.thinking",
	"rho.rpc.permission.request",
	"rho.rpc.ui.ask",
}
