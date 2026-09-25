package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
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

// halter halts on a write to stuck.txt.
type halter struct{}

func (halter) Name() string                          { return "halter" }
func (halter) Init(module.Host, module.Config) error { return nil }
func (halter) GateTool(_ context.Context, _ module.Session, c protocol.ToolCallData) module.Verdict {
	if c.Tool == "write" && strings.Contains(string(c.Arguments), "stuck.txt") {
		return module.Verdict{Decision: module.Halt, Reason: "refused again", Summary: "halter: same write refused twice"}
	}
	return module.Verdict{Decision: module.Allow}
}

// A Halt refuses the call, skips the rest of the batch, and leaves the session
// blocked with the gate's reason, so the person finds it waiting for them.
func TestAHaltRefusesTheCallAndBlocksTheSession(t *testing.T) {
	h := newHarness(t, []module.Module{halter{}}, []provider.Response{
		{ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "write", Arguments: json.RawMessage(`{"path":"stuck.txt","content":"x"}`)},
			{ID: "c2", Name: "write", Arguments: json.RawMessage(`{"path":"after.txt","content":"x"}`)},
		}},
		{Content: "a turn that must not be taken"},
	})
	s := h.create(t)
	if _, err := h.m.Prompt(context.Background(), s.ID(), "go"); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())

	if st := s.State(); st.State != protocol.StateBlocked {
		t.Fatalf("state = %s, want blocked", st.State)
	}
	if len(h.fake.Calls) != 1 {
		t.Fatalf("no further turn may be taken after a halt, made %d requests", len(h.fake.Calls))
	}
	for _, name := range []string{"stuck.txt", "after.txt"} {
		if _, err := os.Stat(filepath.Join(h.dir, name)); err == nil {
			t.Fatalf("%s must not have been written", name)
		}
	}
	var refused, notice, change bool
	for _, e := range s.Events() {
		switch e.Type {
		case protocol.EventToolResult:
			d := protocol.MustData[protocol.ToolResultData](e)
			refused = refused || (d.CallID == "c1" && d.Status == "error" && d.Content == "denied: refused again")
			if d.CallID == "c2" {
				t.Fatal("the rest of the batch must not run")
			}
		case protocol.EventNotice:
			notice = notice || protocol.MustData[protocol.NoticeData](e).Message == "stopping: halter: same write refused twice"
		case protocol.EventStateChange:
			d := protocol.MustData[protocol.StateChangeData](e)
			change = change || (d.To == protocol.StateBlocked && d.Reason == "halter: same write refused twice")
		}
	}
	if !refused || !notice || !change {
		t.Fatalf("refused=%v notice=%v blocked-with-reason=%v", refused, notice, change)
	}
}

// A reply is capped by the provider, or the daemon-wide override, and never
// asks for more than the context has room for.
func TestReplyCap(t *testing.T) {
	usage := func(in int) []protocol.Event {
		data, _ := json.Marshal(protocol.MessageData{Role: "assistant", Usage: &protocol.Usage{InputTokens: in}})
		return []protocol.Event{{Type: protocol.EventMessage, Data: data}}
	}
	for _, tc := range []struct {
		name     string
		cap      int
		window   int
		used     int
		override int
		want     int
	}{
		{"the provider's cap", 16384, 128000, 50000, 0, 16384},
		{"the override wins", 16384, 128000, 50000, 4000, 4000},
		{"no window, no room check", 16384, 0, 50000, 0, 16384},
		{"little room left", 16384, 128000, 120000, 0, 8000},
		{"never below the floor", 16384, 128000, 127900, 0, minReplyCap},
		{"the session that timed out", 16384, 128000, 103512, 0, 16384},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := replyCap(usage(tc.used), provider.Config{MaxTokens: tc.cap, ContextWindow: tc.window}, tc.override)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// Issue: a reply ran for ten minutes, past 20,000 tokens, because nothing
// capped it. Every request now says how much it may write.
func TestEveryRequestCarriesAReplyCap(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"x"}`)}},
			Usage: protocol.Usage{InputTokens: 5000, OutputTokens: 10}},
		{Content: "done"},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	// The harness window is 8000 tokens: the first request has all of it,
	// the second only what the first left.
	for i, want := range []int{8000, 3000} {
		if got := h.fake.Calls[i].MaxTokens; got != want {
			t.Errorf("request %d: max tokens %d, want %d", i+1, got, want)
		}
	}
}

// A reply cut off by the cap ends inside its last call. Running that would
// write half a file, so it is refused, and the model is told why.
func TestACallCutOffByTheCapIsNotRun(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "write", Arguments: json.RawMessage(`{"path":"whole.txt","content":"fine"}`)},
			{ID: "c2", Name: "write", Arguments: json.RawMessage(`{"_malformed":"{\"path\":\"half.txt\",\"content\":\"abc"}`)},
		}, FinishReason: "length"},
		{Content: "I will write it in parts."},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "write two files")
	h.m.WaitIdle(s.ID())

	if _, err := os.Stat(filepath.Join(h.dir, "whole.txt")); err != nil {
		t.Errorf("the complete call should have run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "half.txt")); err == nil {
		t.Error("the cut-off call ran")
	}
	var refused, noticed bool
	for _, e := range s.Events() {
		switch e.Type {
		case protocol.EventToolResult:
			r := protocol.MustData[protocol.ToolResultData](e)
			if r.CallID == "c2" && r.Status == "error" && strings.Contains(r.Content, "cut off") {
				refused = true
			}
		case protocol.EventNotice:
			if strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, "cut off") {
				noticed = true
			}
		}
	}
	if !refused {
		t.Error("the model was not told the call was cut off")
	}
	if !noticed {
		t.Error("nobody was told the reply was cut off")
	}
	// The refusal reaches the model, so the next turn can do better.
	req := h.fake.Calls[1]
	if tail := req.Messages[len(req.Messages)-1]; tail.Role != "tool" || !strings.Contains(tail.Content, "cut off") {
		t.Errorf("second request tail: %+v", tail)
	}
}

// What a failed call had streamed was thrown away, so a call that ran into its
// timeout left no clue why. It is now a notice, and never sent to the model.
func TestAFailedCallLeavesWhatItHadWritten(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.fake.Errors = map[int]error{0: context.DeadlineExceeded}
	h.fake.Partial = map[int]provider.Response{0: {
		Reasoning: "Let me write the file.",
		ToolCalls: []provider.ToolCall{{ID: "c1", Name: "write",
			Arguments: json.RawMessage(`{"_malformed":"{\"path\":\"a.go\",\"content\":\"same line\nsame line\n"}`)}},
	}}
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var partial, failed string
	for _, e := range s.Events() {
		if e.Type != protocol.EventNotice {
			continue
		}
		msg := protocol.MustData[protocol.NoticeData](e).Message
		switch {
		case strings.HasPrefix(msg, "before the call failed"):
			partial = msg
		case strings.HasPrefix(msg, "model call failed"):
			failed = msg
		}
	}
	if failed == "" {
		t.Fatal("the failure itself was not recorded")
	}
	for _, want := range []string{"22 characters of thinking", "a write call with", "same line\nsame line"} {
		if !strings.Contains(partial, want) {
			t.Errorf("the partial reply notice lacks %q:\n%s", want, partial)
		}
	}
	if s.State().State != protocol.StateError {
		t.Errorf("state: %s", s.State().State)
	}
}

// A runaway reply is quoted by its end, not whole.
func TestAPartialReplyIsQuotedByItsEnd(t *testing.T) {
	long := strings.Repeat("loop ", 10000) + "THE END"
	got := partialReply(provider.Response{Content: long})
	if !strings.HasSuffix(got, "THE END") {
		t.Error("the end of the reply is missing")
	}
	if len(got) > partialTail+200 {
		t.Errorf("quoted %d characters of a %d-character reply", len(got), len(long))
	}
	if partialReply(provider.Response{}) != "" {
		t.Error("a call that wrote nothing should say nothing")
	}
}

// 01M39RT5: the reply repeated one paragraph for 7.5 minutes. Watching the
// stream stops it within a few copies, keeps the runaway out of the log, and
// blocks the session so the person decides what comes next.
func TestARepeatingReplyIsStoppedAndBlocksTheSession(t *testing.T) {
	thinking := testdata(t, "degenerate-thinking.txt")
	reply := testdata(t, "degenerate-reply.txt")
	h := newHarness(t, nil, []provider.Response{
		{Reasoning: thinking, Content: reply,
			ToolCalls: []provider.ToolCall{{ID: "c1", Name: "write", Arguments: json.RawMessage(`{"path":"after.txt","content":"x"}`)}}},
		{Content: "a turn that must not be taken"},
	})
	h.fake.Chunk = 20
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "create a PR for this work")
	h.m.WaitIdle(s.ID())

	if st := s.State(); st.State != protocol.StateBlocked {
		t.Fatalf("state = %s, want blocked", st.State)
	}
	if len(h.fake.Calls) != 1 {
		t.Fatalf("no further turn may be taken, made %d requests", len(h.fake.Calls))
	}
	if _, err := os.Stat(filepath.Join(h.dir, "after.txt")); err == nil {
		t.Fatal("a call in a stopped reply must not run")
	}
	var thought, said, notice string
	var change bool
	for _, e := range s.Events() {
		switch e.Type {
		case protocol.EventThinking:
			thought = protocol.MustData[protocol.ThinkingData](e).Content
		case protocol.EventMessage:
			if d := protocol.MustData[protocol.MessageData](e); d.Role == "assistant" {
				said = d.Content
			}
		case protocol.EventNotice:
			notice = protocol.MustData[protocol.NoticeData](e).Message
		case protocol.EventToolCall, protocol.EventToolResult:
			t.Fatalf("no call from a stopped reply is logged: %s", e.Type)
		case protocol.EventStateChange:
			d := protocol.MustData[protocol.StateChangeData](e)
			change = change || (d.To == protocol.StateBlocked && d.Reason == "the reply repeated itself")
		}
	}
	// The thinking repeated too, but only the reply stops a turn. The thinking
	// is logged with its run collapsed and the server's own way out kept.
	if len(thought) > 1024 || !strings.Contains(thought, "more times; dropped from the log") ||
		!strings.Contains(thought, "let's answer now") {
		t.Errorf("logged thinking (%d bytes) should be collapsed:\n%s", len(thought), thought)
	}
	if len(said) > 1024 || strings.Count(said, "I need to reset main back") != 1 {
		t.Errorf("logged reply (%d bytes) should hold one copy:\n%s", len(said), said)
	}
	for _, w := range []string{"the model's reply repeated one passage", "times, so it was stopped after", "I need to reset main back"} {
		if !strings.Contains(notice, w) {
			t.Errorf("notice missing %q: %s", w, notice)
		}
	}
	if !change {
		t.Error("the session should block with reason \"the reply repeated itself\"")
	}
}

// 01M3B65R: thinking looped, was stopped a thousand tokens in, and blocked the
// session twice, once straight after "continue here". Thinking that repeats
// now only costs time: the turn goes on, its call runs, and the log keeps one
// copy.
func TestRepeatingThinkingDoesNotStopTheTurn(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{Reasoning: testdata(t, "degenerate-thinking.txt"), Content: "writing it now",
			ToolCalls: []provider.ToolCall{{ID: "c1", Name: "write", Arguments: json.RawMessage(`{"path":"after.txt","content":"x"}`)}}},
		{Content: "done"},
	})
	h.fake.Chunk = 20
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "continue here")
	h.m.WaitIdle(s.ID())

	if st := s.State(); st.State == protocol.StateBlocked {
		t.Fatalf("state = %s: repeating thinking must not block the session", st.State)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "after.txt")); err != nil {
		t.Fatal("the call after the thinking should have run")
	}
	if len(h.fake.Calls) != 2 {
		t.Errorf("the turn should continue, made %d requests", len(h.fake.Calls))
	}
	var thought, notice string
	for _, e := range s.Events() {
		switch e.Type {
		case protocol.EventThinking:
			if thought == "" {
				thought = protocol.MustData[protocol.ThinkingData](e).Content
			}
		case protocol.EventNotice:
			notice += protocol.MustData[protocol.NoticeData](e).Message + "\n"
		}
	}
	if len(thought) > 1024 || !strings.Contains(thought, "more times; dropped from the log") {
		t.Errorf("logged thinking (%d bytes) should be collapsed:\n%s", len(thought), thought)
	}
	if !strings.Contains(notice, "the turn went on") || strings.Contains(notice, "stopped") {
		t.Errorf("notices should say the turn continued:\n%s", notice)
	}
}

// With the check off, a repeating reply runs as it always did.
func TestARepeatWatchOffLetsTheReplyRun(t *testing.T) {
	reply := testdata(t, "degenerate-reply.txt")
	h := newHarnessWithProvider(t, provider.Config{Name: "fake", ContextWindow: 128000, RepeatLimit: -1}, []provider.Response{
		{Content: reply},
	})
	h.fake.Chunk = 500
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	if st := s.State(); st.State == protocol.StateBlocked {
		t.Fatalf("state = %s, want not blocked", st.State)
	}
	var said string
	for _, e := range s.Events() {
		if e.Type == protocol.EventMessage {
			if d := protocol.MustData[protocol.MessageData](e); d.Role == "assistant" {
				said = d.Content
			}
		}
	}
	if said != reply {
		t.Errorf("logged %d bytes, want the whole %d", len(said), len(reply))
	}
}

// A reply with no thinking that repeats is cut to its first copy.
func TestARepeatingReplyWithoutThinkingIsCut(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: testdata(t, "degenerate-reply.txt")}})
	h.fake.Chunk = 20
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	if st := s.State(); st.State != protocol.StateBlocked {
		t.Fatalf("state = %s, want blocked", st.State)
	}
	var said, notice string
	for _, e := range s.Events() {
		switch e.Type {
		case protocol.EventThinking:
			t.Fatal("no thinking arrived, so none is logged")
		case protocol.EventMessage:
			if d := protocol.MustData[protocol.MessageData](e); d.Role == "assistant" {
				said = d.Content
			}
		case protocol.EventNotice:
			notice = protocol.MustData[protocol.NoticeData](e).Message
		}
	}
	if len(said) > 1024 || strings.Count(said, "I need to reset main back") != 1 {
		t.Errorf("logged reply (%d bytes) should hold one copy:\n%s", len(said), said)
	}
	if !strings.Contains(notice, "the model's reply repeated one passage") {
		t.Errorf("notice: %s", notice)
	}
}
