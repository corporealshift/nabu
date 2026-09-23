package answer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

type fakeSession struct {
	log   []protocol.Event
	state protocol.State
}

func (f *fakeSession) ID() string                               { return "01ARZ3NDEKTSV4RRFFQ69G5FAV" }
func (f *fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: "/w"} }
func (f *fakeSession) State() protocol.State                    { return f.state }
func (f *fakeSession) Events(*string) ([]protocol.Event, error) { return f.log, nil }
func (f *fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

// said is a session whose person's last message is text.
func said(text string) *fakeSession {
	raw, _ := json.Marshal(protocol.MessageData{Role: "user", Content: text})
	return &fakeSession{log: []protocol.Event{{ID: "E1", Type: protocol.EventMessage, Data: raw}}}
}

func enabled(t *testing.T) *Module {
	t.Helper()
	m := &Module{}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatal(err)
	}
	return m
}

func call(tool, args string) protocol.ToolCallData {
	return protocol.ToolCallData{CallID: "c1", Tool: tool, Arguments: json.RawMessage(args)}
}

// The reminder goes at the end of the request, where it is read last, and only
// on a turn that asked something.
func TestTheReminderRidesOnlyOnAQuestion(t *testing.T) {
	m := enabled(t)

	blocks, err := m.BeforeRequest(context.Background(), said("what's the status here?"))
	if err != nil || len(blocks) != 1 || blocks[0].Slot != "suffix" || !strings.Contains(blocks[0].Content, "Answer it, then stop") {
		t.Fatalf("a question should get the suffix reminder, got %+v (%v)", blocks, err)
	}

	if blocks, _ := m.BeforeRequest(context.Background(), said("fix the failing test")); len(blocks) != 0 {
		t.Fatalf("a request should get nothing, got %+v", blocks)
	}
}

// Issue 76: "what's the status here?" became "3 failures left. Let me fix
// them." and an edit. Changing the workspace while answering waits for a yes.
func TestAChangeWhileAnsweringWaitsForTheirSay(t *testing.T) {
	m := enabled(t)
	question := said("what's the status here?")

	cases := []struct {
		name string
		s    *fakeSession
		call protocol.ToolCallData
		want module.Decision
	}{
		{"edit while answering", question, call("edit", `{"path":"engine.rs"}`), module.Ask},
		{"commit while answering", question, call("bash", `{"command":"git add . && git commit -m wip"}`), module.Ask},
		{"reading to answer", question, call("read", `{"path":"engine.rs"}`), module.Allow},
		{"running the tests to answer", question, call("bash", `{"command":"cargo test 2>&1"}`), module.Allow},
		{"edit on request", said("fix the failing test"), call("edit", `{"path":"engine.rs"}`), module.Allow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := m.GateTool(context.Background(), tc.s, tc.call)
			if v.Decision != tc.want {
				t.Fatalf("decision = %v, want %v", v.Decision, tc.want)
			}
			if v.Decision == module.Ask && !strings.Contains(v.Summary, "engine.rs") && !strings.Contains(v.Summary, "git commit") {
				t.Errorf("the prompt should say what would change, got %q", v.Summary)
			}
		})
	}
}

// A goal is a standing instruction to work; a question inside it is not a
// reason to stop working.
func TestARunWithAGoalIsLeftAlone(t *testing.T) {
	m := enabled(t)
	s := said("is the migration done?")
	s.state.Goal = &protocol.GoalData{Condition: "the migration runs", State: "set"}

	if v := m.GateTool(context.Background(), s, call("edit", `{"path":"a.go"}`)); v.Decision != module.Allow {
		t.Fatalf("under a goal an edit should be allowed, got %v", v.Decision)
	}
	if blocks, _ := m.BeforeRequest(context.Background(), s); len(blocks) != 0 {
		t.Fatalf("under a goal there should be no reminder, got %+v", blocks)
	}
}

func TestDisabledDoesNothing(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	if v := m.GateTool(context.Background(), said("why?"), call("edit", `{"path":"a.go"}`)); v.Decision != module.Allow {
		t.Fatalf("disabled should allow, got %v", v.Decision)
	}
}
