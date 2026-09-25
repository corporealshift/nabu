package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/modules/answer"
	"github.com/corporealshift/nabu/daemon/modules/verify"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// Issue 76, end to end, with the real modules. Work is under way, the person
// asks how it is going, and the model does what the logs show it doing: starts
// fixing. The edit waits for a yes nobody gives, the model answers instead,
// and the turn ends there rather than being sent back to the open task.
func TestAQuestionIsAnsweredNotTakenAsWork(t *testing.T) {
	h := newHarness(t, []module.Module{&answer.Module{}, &verify.Module{}}, []provider.Response{
		{Content: "3 failures left. Let me fix them.", ToolCalls: []provider.ToolCall{{
			ID: "c1", Name: "edit", Arguments: json.RawMessage(`{"path":"engine.rs","old":"a","new":"b"}`)}}},
		{Content: "Three tests fail, all in the suppression guard. Want me to fix them?"},
	})
	s := h.create(t)
	if _, err := h.m.UpdateTasksByID(context.Background(), s.ID(), []protocol.Task{
		{ID: "t1", Title: "port the engine", Status: protocol.TaskInProgress},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.m.Prompt(context.Background(), s.ID(), "what's the status here?"); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())

	var suffixes, vetoes int
	var edit protocol.ToolResultData
	for _, e := range s.Events() {
		switch e.Type {
		case protocol.EventContext:
			if d := protocol.MustData[protocol.ContextData](e); d.Source == "module:answer" && d.Slot == "suffix" {
				suffixes++
			}
		case protocol.EventStopVeto:
			vetoes++
		case protocol.EventToolResult:
			edit = *protocol.MustData[protocol.ToolResultData](e)
		}
	}
	if edit.Status != "error" || edit.Kind != protocol.ToolErrorDenied {
		t.Fatalf("the edit should have waited for approval and, with nobody attached, been refused: %+v", edit)
	}
	if vetoes != 0 {
		t.Fatalf("an answered question was vetoed %d times", vetoes)
	}
	if suffixes == 0 {
		t.Fatal("the reminder never reached the model")
	}
	if st := s.State().State; st != protocol.StateIdle {
		t.Fatalf("state = %s, want idle", st)
	}
	if n := h.fake.CallCount(); n != 2 {
		t.Fatalf("model calls = %d, want 2: the answer should have been the end", n)
	}
}
