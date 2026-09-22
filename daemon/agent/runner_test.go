package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

func TestLoopRunsToolsThenStopsIdle(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "write", Arguments: json.RawMessage(`{"path":"out.txt","content":"hi"}`)}}},
		{Content: "Wrote the file."},
	})
	s := h.create(t)
	if _, err := h.m.Prompt(context.Background(), s.ID(), "write out.txt"); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())

	if b, err := os.ReadFile(filepath.Join(h.dir, "out.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("tool did not run: %v %q", err, b)
	}
	st := s.State()
	if st.State != protocol.StateIdle || st.Turns != 2 {
		t.Fatalf("state=%s turns=%d", st.State, st.Turns)
	}
	var kinds []string
	for _, e := range s.Events() {
		kinds = append(kinds, string(e.Type))
	}
	got := strings.Join(kinds, ",")
	want := "session,message,state_change,message,tool_call,tool_result,message,state_change"
	if got != want {
		t.Fatalf("event order:\n%s\nwant:\n%s", got, want)
	}
	req := h.fake.Calls[1]
	tail := req.Messages[len(req.Messages)-1]
	if tail.Role != "tool" || tail.Content != "wrote 2 bytes to out.txt" {
		t.Fatalf("second request tail: %+v", tail)
	}
	if len(req.Tools) == 0 {
		t.Fatal("tools must be offered")
	}
}

func TestLoopRecordsUsageAndStreamsDeltas(t *testing.T) {
	var deltas []string
	h := newHarness(t, nil, []provider.Response{
		{Content: "Hello", Usage: protocol.Usage{InputTokens: 42, OutputTokens: 7, CachedTokens: 40}},
	})
	h.m.deps.Deltas = func(_, _, text string) { deltas = append(deltas, text) }
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "hi")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.Usage.InputTokens != 42 || st.Usage.OutputTokens != 7 || st.Usage.CachedTokens != 40 {
		t.Fatalf("usage: %+v", st.Usage)
	}
	if strings.Join(deltas, "") != "Hello" {
		t.Fatalf("deltas: %v", deltas)
	}
	for _, e := range s.Events() {
		if e.Type == protocol.EventContext {
			t.Fatal("deltas must not become events")
		}
	}
}

func TestLoopSurvivesToolErrors(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"missing.txt"}`)}}},
		{Content: "That file does not exist."},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "read missing.txt")
	h.m.WaitIdle(s.ID())

	if s.State().State != protocol.StateIdle {
		t.Fatalf("state: %s", s.State().State)
	}
	var res protocol.ToolResultData
	for _, e := range s.Events() {
		if e.Type == protocol.EventToolResult {
			res = *protocol.MustData[protocol.ToolResultData](e)
		}
	}
	if res.Status != "error" {
		t.Fatalf("result: %+v", res)
	}
}

func TestProviderErrorEndsTheSessionInError(t *testing.T) {
	h := newHarness(t, nil, nil) // empty script: the first call errors
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "hi")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StateError {
		t.Fatalf("state: %s", st.State)
	}
	ev := s.Events()
	if lastReport(t, ev).ExitStatus != protocol.StateError {
		t.Fatal("report must say error")
	}
	var sawNotice bool
	for _, e := range ev {
		if e.Type == protocol.EventNotice &&
			strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, "no scripted response") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("the provider error must be recorded as a notice")
	}
}

func TestInterruptCancelsTheTurnAndReturnsToIdle(t *testing.T) {
	release := make(chan struct{})
	h := newHarness(t, nil, []provider.Response{{Content: "never sent"}})
	h.fake.BlockOn = release
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "long job")

	deadline := time.Now().Add(2 * time.Second)
	for h.fake.CallCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := h.m.Interrupt(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	close(release)

	st := s.State()
	if st.State != protocol.StateIdle {
		t.Fatalf("state: %s", st.State)
	}
	ev := s.Events()
	last := ev[len(ev)-1]
	if last.Type != protocol.EventStateChange ||
		protocol.MustData[protocol.StateChangeData](last).Reason != "interrupted" {
		t.Fatalf("last event: %+v", last)
	}
}

func TestHistoryIsCarriedIntoLaterRequests(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}},
		{Content: "done"},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "first")
	h.m.WaitIdle(s.ID())
	if h.fake.CallCount() != 2 {
		t.Fatalf("calls: %d", h.fake.CallCount())
	}
	if h.fake.Calls[1].Messages[1].Content != "first" {
		t.Fatalf("history not carried: %+v", h.fake.Calls[1].Messages[1])
	}
}

// toolNamesIn is the tools a request offered the model.
func toolNamesIn(req provider.Request) []string {
	var out []string
	for _, t := range req.Tools {
		out = append(out, t.Name)
	}
	return out
}

// Regression: the field was parsed and read by nothing, so false did nothing.
func TestTasksEnabledFalseWithholdsTheTaskTool(t *testing.T) {
	off := false
	h := newHarnessWithProvider(t, provider.Config{
		Name: "frontier", ContextWindow: 8000, TasksEnabled: &off,
	}, []provider.Response{{Content: "done"}})
	s := h.create(t)

	h.m.Prompt(context.Background(), s.ID(), "hello")
	h.m.WaitIdle(s.ID())

	if len(h.fake.Calls) == 0 {
		t.Fatal("the model was never called")
	}
	for _, name := range toolNamesIn(h.fake.Calls[0]) {
		if strings.HasPrefix(name, "task.") {
			t.Errorf("offered %s to a provider with tasks disabled", name)
		}
	}
}

// The default is on.
func TestTasksAreOfferedByDefault(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "done"}})
	s := h.create(t)

	h.m.Prompt(context.Background(), s.ID(), "hello")
	h.m.WaitIdle(s.ID())

	if len(h.fake.Calls) == 0 {
		t.Fatal("the model was never called")
	}
	var found bool
	for _, name := range toolNamesIn(h.fake.Calls[0]) {
		if name == "task.update" {
			found = true
		}
	}
	if !found {
		t.Errorf("task.update was not offered by default: %v", toolNamesIn(h.fake.Calls[0]))
	}
}

// The setting is about task tools only.
func TestDisablingTasksLeavesTheOtherToolsAlone(t *testing.T) {
	off := false
	h := newHarnessWithProvider(t, provider.Config{
		Name: "frontier", ContextWindow: 8000, TasksEnabled: &off,
	}, []provider.Response{{Content: "done"}})
	s := h.create(t)

	h.m.Prompt(context.Background(), s.ID(), "hello")
	h.m.WaitIdle(s.ID())

	names := toolNamesIn(h.fake.Calls[0])
	if len(names) == 0 {
		t.Fatal("no tools were offered at all")
	}
	var sawRead bool
	for _, n := range names {
		if n == "read" {
			sawRead = true
		}
	}
	if !sawRead {
		t.Errorf("disabling tasks removed unrelated tools: %v", names)
	}
}

// Issue 80. A tool call the server reported as thinking is rebuilt by the
// provider; the agent's job is to run it and to say that it happened, rather
// than treating the empty answer as "the model is finished".
func TestARecoveredCallRunsAndIsReported(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		// What a recovered turn looks like: no content, a call the provider
		// rebuilt out of the reasoning.
		{Reasoning: "Let me look.", ToolCalls: globCall("c1"), Recovered: 1},
		{Content: "done"},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var ranTool, saidSo bool
	for _, e := range s.Events() {
		switch e.Type {
		case protocol.EventToolCall:
			ranTool = true
		case protocol.EventNotice:
			if strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, "into its reasoning") {
				saidSo = true
			}
		}
	}
	if !ranTool {
		t.Fatal("the recovered call must actually run")
	}
	if !saidSo {
		t.Fatal("rewriting what the model produced has to be stated in the log")
	}
	// The turn continued instead of ending on an empty answer, which is the
	// stall in issue 80.
	if st := s.State(); st.State != protocol.StateIdle || st.Turns < 2 {
		t.Fatalf("state=%s turns=%d", st.State, st.Turns)
	}
}

// Nothing recovered, nothing said: the notice must not become background noise.
func TestAnOrdinaryTurnSaysNothingAboutRecovery(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "done"}})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	for _, e := range s.Events() {
		if e.Type == protocol.EventNotice &&
			strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, "into its reasoning") {
			t.Fatal("a clean turn must not report a recovery")
		}
	}
}
