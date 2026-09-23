package stats

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

var t0 = time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

// ev builds an event a number of seconds into the session.
func ev(sec int, typ protocol.EventType, data any) protocol.Event {
	raw, _ := json.Marshal(data)
	return protocol.Event{Type: typ, Timestamp: t0.Add(time.Duration(sec) * time.Second), Data: raw}
}

func state(sec int, to protocol.SessionState) protocol.Event {
	return ev(sec, protocol.EventStateChange, protocol.StateChangeData{To: to})
}

func assistant(sec, in, out int) protocol.Event {
	return ev(sec, protocol.EventMessage, protocol.MessageData{Role: "assistant",
		Usage: &protocol.Usage{InputTokens: in, OutputTokens: out}})
}

func read(sec int, id, path string) protocol.Event {
	args, _ := json.Marshal(map[string]string{"path": path})
	return ev(sec, protocol.EventToolCall, protocol.ToolCallData{CallID: id, Tool: "read", Arguments: args})
}

func result(sec int, id, tool, status string) protocol.Event {
	return ev(sec, protocol.EventToolResult, protocol.ToolResultData{CallID: id, Tool: tool, Status: status})
}

func TestOfMeasuresTheWork(t *testing.T) {
	log := []protocol.Event{
		ev(0, protocol.EventSession, protocol.SessionData{Workspace: "/w", ContextWindow: 1000}),
		ev(1, protocol.EventMessage, protocol.MessageData{Role: "user", Content: "fix it"}),
		state(1, protocol.StateRunning),
		assistant(5, 300, 20),
		read(5, "c1", "a.go"), result(6, "c1", "read", "ok"),
		read(6, "c2", "a.go"), result(7, "c2", "read", "ok"),
		assistant(10, 500, 30),
		read(10, "c3", "a.go"), result(11, "c3", "read", "error"),
		ev(11, protocol.EventCompaction, protocol.CompactionData{Mode: protocol.CompactionClearResults}),
		assistant(15, 400, 10),
		ev(15, protocol.EventStopVeto, protocol.StopVetoData{Module: "verify", Reason: "x"}),
		state(21, protocol.StateIdle),
		ev(60, protocol.EventMessage, protocol.MessageData{Role: "user", Content: "and this?"}),
		state(60, protocol.StateRunning),
		ev(70, protocol.EventMessage, protocol.MessageData{Role: "assistant", Interrupted: true}),
		state(70, protocol.StateIdle),
	}
	s := Of("S1", log)

	if s.Turns != 4 || s.Prompts != 2 || s.Interruptions != 1 || s.Vetoes != 1 {
		t.Errorf("turns %d prompts %d interruptions %d vetoes %d", s.Turns, s.Prompts, s.Interruptions, s.Vetoes)
	}
	if s.Tokens != (Tokens{Input: 1200, Output: 60, PeakContext: 500, ContextWindow: 1000}) {
		t.Errorf("tokens = %+v", s.Tokens)
	}
	if len(s.PerTurn) != 3 || s.PerTurn[1].Input != 500 {
		t.Errorf("per turn = %+v", s.PerTurn)
	}
	if len(s.Tools) != 1 || s.Tools[0] != (Tool{Name: "read", Calls: 3, Errors: 1}) {
		t.Errorf("tools = %+v", s.Tools)
	}
	if s.Compactions.ClearResults != 1 {
		t.Errorf("compactions = %+v", s.Compactions)
	}
	if len(s.Rereads) != 1 || s.Rereads[0] != (Reread{Path: "a.go", Reads: 3}) {
		t.Errorf("rereads = %+v", s.Rereads)
	}
	// 1→21 and 60→70: the gap in between was waiting for the person.
	if s.WorkingSeconds != 30 {
		t.Errorf("working = %ds, want 30", s.WorkingSeconds)
	}
}

func TestAnEmptyLogIsZeroNotNull(t *testing.T) {
	s := Of("S1", nil)
	raw, _ := json.Marshal(s)
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	for _, k := range []string{"per_turn", "tools", "rereads"} {
		if back[k] == nil {
			t.Errorf("%s should be an empty array, not null: a client iterates it", k)
		}
	}
}

func TestUsageHasEveryDayInTheRange(t *testing.T) {
	logs := [][]protocol.Event{
		{assistant(0, 100, 10)}, // Sep 20
		{ev(2*86400, protocol.EventMessage, protocol.MessageData{ // Sep 22
			Role: "assistant", Usage: &protocol.Usage{InputTokens: 50, OutputTokens: 5}})},
		{assistant(-40*86400, 999, 9)}, // outside the range
	}
	days := Usage(logs, t0, t0.Add(2*24*time.Hour), time.UTC)

	want := []Day{
		{Date: "2026-09-20", Turns: 1, Input: 100, Output: 10},
		{Date: "2026-09-21"},
		{Date: "2026-09-22", Turns: 1, Input: 50, Output: 5},
	}
	if len(days) != len(want) {
		t.Fatalf("days = %+v", days)
	}
	for i := range want {
		if days[i] != want[i] {
			t.Errorf("day %d = %+v, want %+v", i, days[i], want[i])
		}
	}
}

// The daemon stopped overnight with the session marked running, and paused it
// on the next start. Those hours were not work.
func TestAStoppedDaemonIsNotWorkingTime(t *testing.T) {
	log := []protocol.Event{
		state(0, protocol.StateRunning),
		assistant(60, 100, 10),
		state(9*3600, protocol.StatePaused), // the next morning
	}
	if got := Of("S1", log).WorkingSeconds; got != 60+int64(maxGap/time.Second) {
		t.Errorf("working = %ds, want the minute plus one capped gap", got)
	}
}
