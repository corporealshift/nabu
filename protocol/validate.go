package protocol

import (
	"encoding/json"
	"fmt"
)

// ValidateLog checks a whole log against spec §3.2: ULID ids, an unbroken
// parent chain, a leading `session` event, known types, and per-type payload
// rules. It returns the first violation found.
func ValidateLog(events []Event) error {
	if len(events) == 0 {
		return fmt.Errorf("log is empty")
	}
	for i, e := range events {
		if err := ValidateULID(e.ID); err != nil {
			return fmt.Errorf("event %d: id %q: %w", i, e.ID, err)
		}
		if i == 0 {
			if e.ParentID != nil {
				return fmt.Errorf("event %s: first event must have parent_id null", e.ID)
			}
			if e.Type != EventSession {
				return fmt.Errorf("event %s: first event must be type session, got %s", e.ID, e.Type)
			}
		} else {
			if e.ParentID == nil || *e.ParentID != events[i-1].ID {
				return fmt.Errorf("event %s: parent_id must be %s (broken chain)", e.ID, events[i-1].ID)
			}
			if e.Type == EventSession {
				return fmt.Errorf("event %s: session event only allowed first", e.ID)
			}
		}
		if err := ValidateEvent(e); err != nil {
			return err
		}
	}
	return nil
}

// ValidateEvent checks one event's type and payload in isolation (no chain
// checks). Unknown extra fields in data are permitted.
func ValidateEvent(e Event) error {
	if len(e.Data) == 0 {
		return fmt.Errorf("event %s: data missing", e.ID)
	}
	v, err := DecodeData(e)
	if err != nil {
		return err
	}
	fail := func(msg string) error { return fmt.Errorf("event %s (%s): %s", e.ID, e.Type, msg) }
	switch d := v.(type) {
	case *SessionData:
		if d.Workspace == "" || d.WorkspaceKey == "" {
			return fail("workspace and workspace_key are required")
		}
		if err := validateOptions(d.Options); err != nil {
			return fail(err.Error())
		}
	case *MessageData:
		if d.Role != "user" && d.Role != "assistant" {
			return fail(fmt.Sprintf("role %q invalid", d.Role))
		}
		if d.Role == "user" && (d.Usage != nil || d.Interrupted) {
			return fail("usage and interrupted are assistant-only")
		}
	case *ToolCallData:
		if d.CallID == "" || d.Tool == "" {
			return fail("call_id and tool are required")
		}
		if !isSource(d.Source, false) {
			return fail(fmt.Sprintf("source %q invalid (model or module:<name>)", d.Source))
		}
		if len(d.Arguments) == 0 || d.Arguments[0] != '{' {
			return fail("arguments must be an object")
		}
	case *ToolResultData:
		if d.CallID == "" || d.Tool == "" {
			return fail("call_id and tool are required")
		}
		if d.Status != "ok" && d.Status != "error" {
			return fail(fmt.Sprintf("status %q invalid", d.Status))
		}
		if d.Kind != "" && !IsToolErrorKind(d.Kind) {
			return fail(fmt.Sprintf("kind %q invalid", d.Kind))
		}
		// A kind describes a failure. On a result that says it succeeded it is
		// a contradiction, and silently keeping it would let the two fields
		// disagree in the log forever.
		if d.Status == "ok" && (d.Kind != "" || d.ExitCode != nil) {
			return fail("kind and exit_code belong to a failure, not to status ok")
		}
	case *OptionsChangeData:
		switch d.Key {
		case "model", "compaction_enabled", "permission_mode":
		default:
			return fail(fmt.Sprintf("key %q invalid", d.Key))
		}
		if len(d.From) == 0 || len(d.To) == 0 {
			return fail("from and to are required")
		}
		if !isSource(d.Source, true) {
			return fail(fmt.Sprintf("source %q invalid", d.Source))
		}
	case *StateChangeData:
		if !isState(d.To) {
			return fail(fmt.Sprintf("to %q invalid", d.To))
		}
		if d.From != nil && !isState(*d.From) {
			return fail(fmt.Sprintf("from %q invalid", *d.From))
		}
	case *CompactionData:
		if d.Mode != CompactionClearResults && d.Mode != CompactionSummarize {
			return fail(fmt.Sprintf("mode %q invalid", d.Mode))
		}
		if ValidateULID(d.RangeStart) != nil || ValidateULID(d.RangeEnd) != nil {
			return fail("range_start and range_end must be ULIDs")
		}
		if d.Mode == CompactionSummarize && d.Summary == "" {
			return fail("summary required for summarize")
		}
	case *ContextData:
		if !isSource(d.Source, true) {
			return fail(fmt.Sprintf("source %q invalid", d.Source))
		}
		if d.Slot != "prefix" && d.Slot != "suffix" {
			return fail(fmt.Sprintf("slot %q invalid", d.Slot))
		}
	case *TasksData:
		if d.Revision < 1 {
			return fail("revision must be >= 1")
		}
		if !isSource(d.Source, true) {
			return fail(fmt.Sprintf("source %q invalid", d.Source))
		}
		seen := map[string]bool{}
		for _, t := range d.Tasks {
			if t.ID == "" || t.Title == "" {
				return fail("task id and title are required")
			}
			if seen[t.ID] {
				return fail(fmt.Sprintf("duplicate task id %q", t.ID))
			}
			seen[t.ID] = true
			if !isTaskStatus(t.Status) {
				return fail(fmt.Sprintf("task %s: status %q invalid", t.ID, t.Status))
			}
			if t.BlockedBy == nil {
				return fail(fmt.Sprintf("task %s: blocked_by must be present", t.ID))
			}
			if t.Evidence != "" && ValidateULID(t.Evidence) != nil {
				return fail(fmt.Sprintf("task %s: evidence must be a ULID", t.ID))
			}
		}
	case *GoalData:
		if d.Condition == "" {
			return fail("condition is required")
		}
		switch d.State {
		case "set", "met", "unmet", "impossible", "cleared":
		default:
			return fail(fmt.Sprintf("state %q invalid", d.State))
		}
		if !isSource(d.Source, true) {
			return fail(fmt.Sprintf("source %q invalid", d.Source))
		}
	case *CheckData:
		if d.Name == "" {
			return fail("name is required")
		}
		switch d.Kind {
		case "command", "judge", "module":
		default:
			return fail(fmt.Sprintf("kind %q invalid", d.Kind))
		}
		switch d.Status {
		case "pass", "fail", "error":
		default:
			return fail(fmt.Sprintf("status %q invalid", d.Status))
		}
	case *StopVetoData:
		if d.Module == "" || d.Reason == "" {
			return fail("module and reason are required")
		}
	case *BudgetData:
		if d.MaxTurns < 0 || d.MaxTokens < 0 || d.MaxUSD < 0 {
			return fail("budget values must be >= 0")
		}
		if !isSource(d.Source, true) {
			return fail(fmt.Sprintf("source %q invalid", d.Source))
		}
	case *NoticeData:
		if !isSource(d.Source, true) {
			return fail(fmt.Sprintf("source %q invalid", d.Source))
		}
		switch d.Level {
		case "info", "warn", "error":
		default:
			return fail(fmt.Sprintf("level %q invalid", d.Level))
		}
	case *ReportData:
		switch d.ExitStatus {
		case StateCompleted, StateBlocked, StatePaused, StateError:
		default:
			return fail(fmt.Sprintf("exit_status %q invalid", d.ExitStatus))
		}
		if d.Checks == nil {
			return fail("checks must be present (may be empty)")
		}
		if d.Tasks.Open == nil {
			return fail("tasks.open must be present (may be empty)")
		}
	}
	return nil
}

func validateOptions(o Options) error {
	if o.Model == "" {
		return fmt.Errorf("options.model is required")
	}
	switch o.PermissionMode {
	case PermissionAsk, PermissionAuto, PermissionBypass:
		return nil
	}
	return fmt.Errorf("options.permission_mode %q invalid", o.PermissionMode)
}

// isSource accepts daemon|client|model|module:<name>; when allowInfra is false
// only model and module:<name> are accepted (tool_call sources).
func isSource(s string, allowInfra bool) bool {
	switch s {
	case "model":
		return true
	case "daemon", "client":
		return allowInfra
	}
	if len(s) > len("module:") && s[:len("module:")] == "module:" {
		name := s[len("module:"):]
		if name[0] < 'a' || name[0] > 'z' {
			return false
		}
		for i := 1; i < len(name); i++ {
			c := name[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return false
			}
		}
		return true
	}
	return false
}

func isState(s SessionState) bool {
	switch s {
	case StateIdle, StateRunning, StateBlocked, StatePaused, StateCompleted, StateError:
		return true
	}
	return false
}

func isTaskStatus(s TaskStatus) bool {
	switch s {
	case TaskPending, TaskInProgress, TaskBlocked, TaskDone, TaskFailed, TaskCancelled:
		return true
	}
	return false
}

// ParseLog decodes a JSON array of events without validating it.
func ParseLog(b []byte) ([]Event, error) {
	var events []Event
	if err := json.Unmarshal(b, &events); err != nil {
		return nil, err
	}
	return events, nil
}
