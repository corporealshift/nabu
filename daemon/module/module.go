package module

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/corporealshift/nabu/protocol"
)

// Module is the one required interface. Everything else a module can do is an
// optional interface below, asserted at registration.
type Module interface {
	// Name is the module's stable identifier: lowercase, [a-z0-9_-], used in
	// event sources ("module:<name>"), config sections and data directories.
	Name() string
	// Init is called once at daemon start with the host and the module's
	// config section. Returning an error disables the module for the daemon's
	// lifetime and is reported as a notice on every session.
	Init(h Host, cfg Config) error
}

// Config is a module's decoded config section. A missing section is an empty
// Config; every accessor takes a default.
type Config map[string]any

// Bool returns the key as a bool, or def when absent or not a bool.
func (c Config) Bool(key string, def bool) bool {
	if v, ok := c[key].(bool); ok {
		return v
	}
	return def
}

// String returns the key as a string, or def.
func (c Config) String(key string, def string) string {
	if v, ok := c[key].(string); ok {
		return v
	}
	return def
}

// Int returns the key as an int, accepting int64 and float64 (TOML and JSON
// decoders disagree), or def.
func (c Config) Int(key string, def int) int {
	switch v := c[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

// Strings returns the key as a []string, or def.
func (c Config) Strings(key string, def []string) []string {
	switch v := c[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return def
}

// StringMap returns the key as a map of string to string, or nil. Entries whose
// value is not a string are dropped rather than coerced: a config that says
// something unreadable should lose that entry, not gain a stringified one.
func (c Config) StringMap(key string) map[string]string {
	raw, ok := c[key].(map[string]any)
	if !ok {
		if already, ok := c[key].(map[string]string); ok {
			return already
		}
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// Enabled reports the conventional `enabled` flag, default true.
func (c Config) Enabled() bool { return c.Bool("enabled", true) }

// ContextBlock is content a module wants in front of the model. The host
// records it as a `context` event (with the module's source) before it is ever
// sent, so the request stays a pure function of the log.
type ContextBlock struct {
	Slot    string // "prefix" or "suffix"
	Content string
}

// ---------------------------------------------------------------------------
// Optional hook interfaces (spec §14.1). A module implements any subset.

// SessionStarter runs when a session is created or first loaded after a daemon
// start. Returned blocks may use either slot; prefix blocks from here form the
// stable request prefix.
type SessionStarter interface {
	SessionStart(ctx context.Context, s Session) ([]ContextBlock, error)
}

// RequestHook runs before every request is assembled. It may return suffix
// blocks only; a prefix block returned here is rejected by the host, because
// the prefix must not change between compactions.
type RequestHook interface {
	BeforeRequest(ctx context.Context, s Session) ([]ContextBlock, error)
}

// Decision is a tool gate's answer.
type Decision int

const (
	// Allow lets the call proceed. If every gate allows, the tool runs.
	Allow Decision = iota
	// Deny blocks the call; the model sees Reason as an error tool result.
	Deny
	// Ask defers to the attached clients via permission.request.
	Ask
	// Halt refuses the call as Deny does, then stops the session as blocked:
	// the loop takes no further turn, and the person finds a session waiting
	// for them rather than one still running. It is for a gate that has seen
	// its refusals ignored; Summary is the one line recorded as the reason.
	Halt
)

// Verdict is what a ToolGate returns.
type Verdict struct {
	Decision Decision
	Reason   string // required for Deny and Halt; shown to the model
	Summary  string // for Ask: one line describing the action; for Halt: why the session stopped
	Risk     string // for Ask: "low" | "medium" | "high"
}

// ToolGate decides whether a tool call may run. Gates are asked in
// registration order; the first Deny or Halt short-circuits; any Ask (absent a Deny)
// results in one permission request.
type ToolGate interface {
	GateTool(ctx context.Context, s Session, call protocol.ToolCallData) Verdict
}

// Tool is a callable tool. Built-ins and module tools use the same type.
type Tool struct {
	Name        string
	Description string
	// Schema is the JSON Schema for Arguments, sent to the model.
	Schema json.RawMessage
	// Run executes the tool. The returned string is the tool_result content.
	Run func(ctx context.Context, s Session, args json.RawMessage) (string, error)
}

// ToolProvider contributes tools to the registry at daemon start.
type ToolProvider interface {
	Tools() []Tool
}

// ToolObserver is told about every tool result after it is logged.
type ToolObserver interface {
	ToolResult(ctx context.Context, s Session, call protocol.ToolCallData, result protocol.ToolResultData)
}

// TurnObserver runs after each assistant message is logged.
type TurnObserver interface {
	TurnEnd(ctx context.Context, s Session)
}

// StopInfo is what a StopGate receives (spec §10.1).
type StopInfo struct {
	Goal                 *protocol.GoalData
	Tasks                []protocol.Task
	LastAssistantMessage string
	TurnsSinceUser       int
	VetoCount            int
}

// StopVerdict is a StopGate's answer. Allow true means "no objection".
type StopVerdict struct {
	Allow  bool
	Reason string // required when Allow is false; becomes a stop_veto event
}

// StopGate is asked when the model produced no tool calls. All gates are
// asked; every veto is logged.
type StopGate interface {
	BeforeStop(ctx context.Context, s Session, info StopInfo) StopVerdict
}

// Range is an inclusive event-id range.
type Range struct {
	Start, End string
}

// CompactionHook lets a module protect state across a summarize compaction:
// BeforeCompaction returns strings the summary prompt must preserve;
// AfterCompaction re-injects prefix blocks the compaction retired. Both run
// under Options.CompactionTimeout rather than HookTimeout, because a hook here
// may make a model call of its own.
type CompactionHook interface {
	BeforeCompaction(ctx context.Context, s Session, r Range) []string
	AfterCompaction(ctx context.Context, s Session) ([]ContextBlock, error)
}

// ResumeHook runs when a paused session is resumed.
type ResumeHook interface {
	SessionResume(ctx context.Context, s Session) error
}

// SessionEnder runs at every terminal or paused transition, before the report.
type SessionEnder interface {
	SessionEnd(ctx context.Context, s Session)
}

// ReportFields are the workspace observations a Reporter contributes.
type ReportFields struct {
	FilesTouched []string
	Commits      []string
	TreeDirty    *bool
	Checks       []protocol.ReportCheck
}

// Reporter contributes to the run report.
type Reporter interface {
	Report(ctx context.Context, s Session) (ReportFields, error)
}

// ---------------------------------------------------------------------------
// The host API (spec §14.2): the only way a module sees the daemon.

// Host is handed to every module at Init.
type Host interface {
	Model() Model
	Tools() ToolCaller
	UI() UI
	// DataDir returns ~/.nabu/<name>/, created on first call.
	DataDir(module string) (string, error)
	Log() *slog.Logger
}

// Session is the per-session handle passed to every hook.
type Session interface {
	ID() string
	Workspace() Workspace
	// Events returns events after the given id (nil = all), like events_after.
	Events(after *string) ([]protocol.Event, error)
	// State is the current projection.
	State() protocol.State
	// Append records a module event. Permitted types: check, notice, and goal
	// with state met|unmet|impossible. Anything else is rejected.
	Append(t protocol.EventType, data any) (protocol.Event, error)
}

// Workspace identifies where a session runs.
type Workspace struct {
	Path string
	Key  string
}

// CompletionRequest is a model call made by a module (judge, curator).
type CompletionRequest struct {
	Model     string // empty = the session's model
	System    string
	Messages  []Message
	MaxTokens int
}

// Message is a chat message for CompletionRequest.
type Message struct {
	Role    string
	Content string
}

// CompletionResponse is the model's answer plus usage.
type CompletionResponse struct {
	Content string
	Usage   protocol.Usage
}

// Model routes module calls through the provider layer, honouring
// max_in_flight and usage accounting.
type Model interface {
	Complete(ctx context.Context, s Session, req CompletionRequest) (CompletionResponse, error)
}

// ToolCaller lets a module invoke any registered tool as itself. The call
// goes through the same gate as a model call and is logged with
// source module:<name>.
type ToolCaller interface {
	Call(ctx context.Context, s Session, tool string, args json.RawMessage) (protocol.ToolResultData, error)
}

// UI asks the attached clients a question; first responder wins.
type UI interface {
	Ask(ctx context.Context, s Session, question string, choices []string) (string, error)
}
