package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

// Version is the protocol version this package implements (spec §1.1).
const Version = "1.0"

// EventType is the type discriminator of a session event.
type EventType string

// The fifteen event types (spec §3.1). Order here is documentation order.
const (
	EventSession       EventType = "session"
	EventMessage       EventType = "message"
	EventThinking      EventType = "thinking"
	EventToolCall      EventType = "tool_call"
	EventToolResult    EventType = "tool_result"
	EventOptionsChange EventType = "options_change"
	EventStateChange   EventType = "state_change"
	EventCompaction    EventType = "compaction"
	EventContext       EventType = "context"
	EventTasks         EventType = "tasks"
	EventGoal          EventType = "goal"
	EventCheck         EventType = "check"
	EventStopVeto      EventType = "stop_veto"
	EventBudget        EventType = "budget"
	EventNotice        EventType = "notice"
	EventReport        EventType = "report"
)

// EventTypes lists every valid event type in documentation order.
var EventTypes = []EventType{
	EventSession, EventMessage, EventThinking, EventToolCall, EventToolResult,
	EventOptionsChange,
	EventStateChange, EventCompaction, EventContext, EventTasks, EventGoal, EventCheck,
	EventStopVeto, EventBudget, EventNotice, EventReport,
}

// Event is one record in the append-only session log (spec §3).
type Event struct {
	ID        string          `json:"id"`
	ParentID  *string         `json:"parent_id"`
	Timestamp time.Time       `json:"timestamp"`
	Type      EventType       `json:"type"`
	Data      json.RawMessage `json:"data"`
}

// SessionState is a session's lifecycle state (spec §3.1 state_change).
type SessionState string

const (
	StateIdle      SessionState = "idle"
	StateRunning   SessionState = "running"
	StateBlocked   SessionState = "blocked"
	StatePaused    SessionState = "paused"
	StateCompleted SessionState = "completed"
	StateError     SessionState = "error"
)

// PermissionMode is the session option governing the tool gate.
type PermissionMode string

const (
	PermissionAsk    PermissionMode = "ask"
	PermissionAuto   PermissionMode = "auto"
	PermissionBypass PermissionMode = "bypass"
)

// Options are the mutable session options carried by `session` and changed by
// `options_change`.
type Options struct {
	Model             string         `json:"model"`
	CompactionEnabled bool           `json:"compaction_enabled"`
	PermissionMode    PermissionMode `json:"permission_mode"`
}

// SessionData is the payload of the first event of every log.
type SessionData struct {
	Workspace    string  `json:"workspace"`
	WorkspaceKey string  `json:"workspace_key"`
	Options      Options `json:"options"`
}

// Usage is the token accounting reported on assistant messages.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// MessageData is a user or assistant message.
type MessageData struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ClientID is the outbox item a user message came from. A client that
	// retries after a dropped connection sends the same one, and the daemon
	// answers with the event it already appended rather than appending twice.
	// The log is therefore its own deduplication table, which is what makes it
	// survive a restart.
	ClientID    string `json:"client_id,omitempty"`
	Usage       *Usage `json:"usage,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`
}

// ToolCallData records a tool invocation.
type ToolCallData struct {
	CallID    string          `json:"call_id"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Source    string          `json:"source"`
}

// ToolResultData records the outcome of a tool invocation.
type ToolResultData struct {
	CallID  string `json:"call_id"`
	Tool    string `json:"tool"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// OptionsChangeData records a mid-session change to one option.
type OptionsChangeData struct {
	Key    string          `json:"key"`
	From   json.RawMessage `json:"from"`
	To     json.RawMessage `json:"to"`
	Source string          `json:"source"`
}

// StateChangeData records a session lifecycle transition.
type StateChangeData struct {
	From   *SessionState `json:"from"`
	To     SessionState  `json:"to"`
	Reason string        `json:"reason,omitempty"`
}

// CompactionMode distinguishes the two compaction stages.
type CompactionMode string

const (
	CompactionClearResults CompactionMode = "clear_results"
	CompactionSummarize    CompactionMode = "summarize"
)

// CompactionData records a context reduction over an inclusive id range.
type CompactionData struct {
	Mode       CompactionMode `json:"mode"`
	RangeStart string         `json:"range_start"`
	RangeEnd   string         `json:"range_end"`
	Summary    string         `json:"summary,omitempty"`
}

// ContextData is a block injected into the model request outside of messages.
type ContextData struct {
	Source  string `json:"source"`
	Slot    string `json:"slot"`
	Content string `json:"content"`
}

// TaskStatus is a task's state within a snapshot.
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskBlocked    TaskStatus = "blocked"
	TaskDone       TaskStatus = "done"
	TaskFailed     TaskStatus = "failed"
	TaskCancelled  TaskStatus = "cancelled"
)

// Task is one entry in a tasks snapshot.
type Task struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    TaskStatus `json:"status"`
	DoneWhen  string     `json:"done_when,omitempty"`
	Check     string     `json:"check,omitempty"`
	BlockedBy []string   `json:"blocked_by"`
	Note      string     `json:"note,omitempty"`
	Evidence  string     `json:"evidence,omitempty"`
}

// TasksData is a full snapshot of the session's task list.
type TasksData struct {
	Revision int    `json:"revision"`
	Source   string `json:"source"`
	Tasks    []Task `json:"tasks"`
}

// GoalData records the run-level goal and its state.
type GoalData struct {
	Condition string `json:"condition"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	Source    string `json:"source"`
}

// CheckData records that a verification check ran.
type CheckData struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	TaskID  string `json:"task_id,omitempty"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Output  string `json:"output,omitempty"`
}

// StopVetoData records a module's objection to the loop stopping.
type StopVetoData struct {
	Module string `json:"module"`
	Reason string `json:"reason"`
}

// BudgetData records the loop bound. Zero means unlimited.
type BudgetData struct {
	MaxTurns  int     `json:"max_turns"`
	MaxTokens int     `json:"max_tokens,omitempty"`
	MaxUSD    float64 `json:"max_usd,omitempty"`
	Source    string  `json:"source"`
}

// ThinkingData is the model's reasoning for the turn that follows it. It is
// shown to the reader and never sent back to the model (spec 6.1).
type ThinkingData struct {
	Content string `json:"content"`
	Source  string `json:"source"`
}

// NoticeData is something a human should see.
type NoticeData struct {
	Source  string `json:"source"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ReportGoal is the goal summary inside a report.
type ReportGoal struct {
	Condition string `json:"condition"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

// ReportTaskRef is an open task listed in a report.
type ReportTaskRef struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Status TaskStatus `json:"status"`
}

// ReportTasks summarises the task list at report time.
type ReportTasks struct {
	Total int             `json:"total"`
	Done  int             `json:"done"`
	Open  []ReportTaskRef `json:"open"`
}

// ReportCheck is one check outcome listed in a report.
type ReportCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

// ReportData is the run report emitted at every terminal or paused transition.
type ReportData struct {
	ExitStatus   SessionState  `json:"exit_status"`
	Goal         *ReportGoal   `json:"goal,omitempty"`
	Tasks        ReportTasks   `json:"tasks"`
	Checks       []ReportCheck `json:"checks"`
	FilesTouched []string      `json:"files_touched,omitempty"`
	Commits      []string      `json:"commits,omitempty"`
	TreeDirty    *bool         `json:"tree_dirty,omitempty"`
}

// DecodeData unmarshals e.Data into the Go type for e.Type and returns it.
// It returns an error for unknown types or malformed payloads. Unknown extra
// fields are ignored, as the spec requires.
func DecodeData(e Event) (any, error) {
	var target any
	switch e.Type {
	case EventSession:
		target = &SessionData{}
	case EventMessage:
		target = &MessageData{}
	case EventToolCall:
		target = &ToolCallData{}
	case EventToolResult:
		target = &ToolResultData{}
	case EventOptionsChange:
		target = &OptionsChangeData{}
	case EventStateChange:
		target = &StateChangeData{}
	case EventThinking:
		target = &ThinkingData{}
	case EventCompaction:
		target = &CompactionData{}
	case EventContext:
		target = &ContextData{}
	case EventTasks:
		target = &TasksData{}
	case EventGoal:
		target = &GoalData{}
	case EventCheck:
		target = &CheckData{}
	case EventStopVeto:
		target = &StopVetoData{}
	case EventBudget:
		target = &BudgetData{}
	case EventNotice:
		target = &NoticeData{}
	case EventReport:
		target = &ReportData{}
	default:
		return nil, fmt.Errorf("event %s: unknown type %q", e.ID, e.Type)
	}
	if err := json.Unmarshal(e.Data, target); err != nil {
		return nil, fmt.Errorf("event %s (%s): %w", e.ID, e.Type, err)
	}
	return target, nil
}

// MustData is DecodeData for callers that have already validated the log.
func MustData[T any](e Event) *T {
	v, err := DecodeData(e)
	if err != nil {
		panic(err)
	}
	t, ok := v.(*T)
	if !ok {
		panic(fmt.Sprintf("event %s: wrong data type for %s", e.ID, e.Type))
	}
	return t
}
