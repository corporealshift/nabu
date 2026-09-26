package verify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// turn builds a session whose log is a person's message and then the given
// tool calls, each of which succeeded.
func turn(t *testing.T, ws, prompt string, calls ...protocol.ToolCallData) *fakeSession {
	t.Helper()
	s := &fakeSession{workspace: ws}
	s.events = append(s.events, logged(t, "U1", protocol.EventMessage, protocol.MessageData{Role: "user", Content: prompt}))
	for _, c := range calls {
		s.events = append(s.events,
			logged(t, c.CallID+"c", protocol.EventToolCall, c),
			logged(t, c.CallID+"r", protocol.EventToolResult, protocol.ToolResultData{CallID: c.CallID, Tool: c.Tool, Status: "ok"}))
	}
	return s
}

// strict gives a session an active goal: the mode with repeated vetoes, for a
// run nobody is watching.
func strict(s *fakeSession) *fakeSession {
	s.state.Goal = &protocol.GoalData{Condition: "the work is done", State: "set"}
	return s
}

// worked is a session whose turn wrote a file, so the gate applies to it.
func worked(t *testing.T, ws string) *fakeSession {
	t.Helper()
	return turn(t, ws, "fix the build", protocol.ToolCallData{
		CallID: "w1", Tool: "write", Arguments: json.RawMessage(`{"path":"a.txt","content":"x"}`), Source: "model"})
}

// 01M3DPQ6: asked for a README, the model answered without calling a tool.
// The gate failed on an Android build the session never touched, and the
// session blocked twice. A turn that changed nothing is not gated.
func TestATurnThatChangedNothingIsNotGated(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls []protocol.ToolCallData
	}{
		{"no tool calls at all", nil},
		{"only reading", []protocol.ToolCallData{
			{CallID: "r1", Tool: "read", Arguments: json.RawMessage(`{"path":"README.md"}`), Source: "model"},
			{CallID: "b1", Tool: "bash", Arguments: json.RawMessage(`{"command":"ls android"}`), Source: "model"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newVerify(t, module.Config{"command": "exit 127", "require_clean_tree": false})
			s := turn(t, t.TempDir(), "create a README for this project and a PR for it", tc.calls...)
			if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
				t.Errorf("a turn that changed nothing was gated: %q", v.Reason)
			}
			if got := s.checkEvents(); len(got) != 0 {
				t.Errorf("the gate should not have run, recorded %+v", got)
			}
		})
	}
}

func TestATurnThatChangedSomethingIsGated(t *testing.T) {
	m := newVerify(t, module.Config{"command": "exit 127", "require_clean_tree": false})
	s := turn(t, t.TempDir(), "create a README", protocol.ToolCallData{
		CallID: "b1", Tool: "bash", Arguments: json.RawMessage(`{"command":"echo hi > README.md"}`), Source: "model"})
	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); v.Allow || !strings.Contains(v.Reason, "gate failed") {
		t.Errorf("a turn that wrote a file must be gated, got allow=%v %q", v.Allow, v.Reason)
	}
}

// A failure the session caused still holds a later turn that changes nothing:
// the recorded check is what stops it, not a fresh run of the gate.
func TestAFailureTheSessionCausedStillHoldsTheNextTurn(t *testing.T) {
	m := newVerify(t, module.Config{"command": "exit 3", "require_clean_tree": false})
	s := strict(worked(t, t.TempDir()))
	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); v.Allow {
		t.Fatal("setup: the gate should fail")
	}
	s.events = append(s.events, logged(t, "U2", protocol.EventMessage, protocol.MessageData{Role: "user", Content: "carry on"}))
	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow || !strings.Contains(v.Reason, "verify.command") {
		t.Errorf("the recorded failure should still veto, got allow=%v %q", v.Allow, v.Reason)
	}
}
