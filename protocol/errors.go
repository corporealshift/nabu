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
	CodeSessionNotFound:    "nabu_session_not_found",
	CodeInvalidTransition:  "nabu_invalid_transition",
	CodePermissionDenied:   "nabu_permission_denied",
	CodeAlreadyResolved:    "nabu_already_resolved",
	CodeCursorUnknown:      "nabu_cursor_unknown",
	CodeProtocolMismatch:   "nabu_protocol_mismatch",
	CodeUnauthorized:       "nabu_unauthorized",
	CodeWorkspaceUntrusted: "nabu_workspace_untrusted",
}

// RPCError is a JSON-RPC error object with nabu's name in data.
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
	"nabu.hello",
	"nabu.session.list",
	"nabu.session.create",
	"nabu.session.send_prompt",
	"nabu.session.events_after",
	"nabu.session.subscribe",
	"nabu.session.unsubscribe",
	"nabu.session.interrupt",
	"nabu.session.stop",
	"nabu.session.resume",
	"nabu.session.state",
	"nabu.session.set_goal",
	"nabu.session.clear_goal",
	"nabu.session.set_option",
	"nabu.session.update_tasks",
	"nabu.session.event",
	"nabu.session.delta",
	"nabu.session.thinking",
	"nabu.rpc.permission.request",
	"nabu.rpc.ui.ask",
}
