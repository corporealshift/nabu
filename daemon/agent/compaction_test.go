package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

// bigUsage reports input token usage at the given fraction of the harness's
// 8000-token window, so a test can drive the thresholds.
func bigUsage(frac float64) protocol.Usage {
	return protocol.Usage{InputTokens: int(8000 * frac), OutputTokens: 10}
}

func globCall(id string) []provider.ToolCall {
	return []provider.ToolCall{{ID: id, Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}
}

func TestClearResultsFiresAtTheLowerThreshold(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.10)},
		{ToolCalls: globCall("c2"), Usage: bigUsage(0.75)}, // crosses 0.70
		{Content: "done", Usage: bigUsage(0.20)},
	})
	h.m.cfg.Compaction.KeepTurns = 1
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var modes []string
	for _, e := range s.Events() {
		if e.Type == protocol.EventCompaction {
			modes = append(modes, string(protocol.MustData[protocol.CompactionData](e).Mode))
		}
	}
	if strings.Join(modes, ",") != "clear_results" {
		t.Fatalf("compactions: %v", modes)
	}
	last := h.fake.Calls[len(h.fake.Calls)-1]
	var stubs int
	for _, msg := range last.Messages {
		if msg.Role == "tool" && strings.HasPrefix(msg.Content, "[result cleared:") {
			stubs++
		}
	}
	if stubs != 1 {
		t.Fatalf("expected one cleared result, got %d: %+v", stubs, last.Messages)
	}
}

// preserver asks the summary to keep a string and re-injects a prefix block.
type preserver struct{ before, after int }

func (p *preserver) Name() string                          { return "preserver" }
func (p *preserver) Init(module.Host, module.Config) error { return nil }
func (p *preserver) BeforeCompaction(context.Context, module.Session, module.Range) []string {
	p.before++
	return []string{"the user's deploy key is never to be printed"}
}
func (p *preserver) AfterCompaction(context.Context, module.Session) ([]module.ContextBlock, error) {
	p.after++
	return []module.ContextBlock{{Slot: "prefix", Content: "# Skills (re-injected)"}}, nil
}

func TestSummarizeFiresAtTheUpperThreshold(t *testing.T) {
	p := &preserver{}
	h := newHarness(t, []module.Module{p}, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.90)}, // crosses 0.85
		{Content: "THE SUMMARY"},                           // the summarizer call
		{Content: "done", Usage: bigUsage(0.10)},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var cd *protocol.CompactionData
	for _, e := range s.Events() {
		if e.Type == protocol.EventCompaction {
			d := protocol.MustData[protocol.CompactionData](e)
			if d.Mode == protocol.CompactionSummarize {
				cd = d
			}
		}
	}
	if cd == nil || cd.Summary != "THE SUMMARY" {
		t.Fatalf("summarize event: %+v", cd)
	}
	if p.before != 1 || p.after != 1 {
		t.Fatalf("hooks: before=%d after=%d", p.before, p.after)
	}
	sum := h.fake.Calls[1]
	var joined strings.Builder
	for _, msg := range sum.Messages {
		joined.WriteString(msg.Content)
	}
	if !strings.Contains(joined.String(), "deploy key") {
		t.Fatalf("preserve string missing from the summary prompt: %s", joined.String())
	}
	last := h.fake.Calls[len(h.fake.Calls)-1]
	sys := last.Messages[0].Content
	if !strings.Contains(sys, "THE SUMMARY") || !strings.Contains(sys, "# Skills (re-injected)") ||
		!strings.Contains(sys, "## Current state") {
		t.Fatalf("final system message: %q", sys)
	}
	var msgs int
	for _, e := range s.Events() {
		if e.Type == protocol.EventMessage {
			msgs++
		}
	}
	if msgs < 3 {
		t.Fatalf("compaction must not delete events, found %d messages", msgs)
	}
}

func TestCompactionDisabledHardStops(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.95)},
	})
	s := h.create(t)
	if _, err := h.m.SetOption(context.Background(), s.ID(), "compaction_enabled", false); err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	if st := s.State(); st.State != protocol.StateBlocked {
		t.Fatalf("state: %s", st.State)
	}
	var sawNotice bool
	for _, e := range s.Events() {
		if e.Type == protocol.EventNotice &&
			strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, "context limit") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("a hard stop must say why")
	}
}

// rpcCode reports the protocol error code an error carries, or "" when it is
// not an RPC error at all. A refusal a client cannot tell apart from a crash is
// not a refusal.
func rpcCode(err error) int {
	var e *protocol.RPCError
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// primed puts two turns of history in the log, which is the least that is worth
// summarising.
func (h *harness) primed(t *testing.T) *session.Session {
	t.Helper()
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())
	return s
}

func TestCompactOnRequestSummarizesAndReturnsTheEvent(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: globCall("c1")},
		{Content: "done"},
		{Content: "THE SUMMARY"}, // the summarizer call Compact makes
	})
	s := h.primed(t)

	ev, err := h.m.Compact(context.Background(), s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != protocol.EventCompaction {
		t.Fatalf("event type: %s", ev.Type)
	}
	d := protocol.MustData[protocol.CompactionData](ev)
	if d.Mode != protocol.CompactionSummarize || d.Summary != "THE SUMMARY" {
		t.Fatalf("compaction: %+v", d)
	}
	// The returned event is the one in the log, not a copy assembled for the
	// reply: a client that stores the id must be able to find it again.
	log := s.Events()
	if log[len(log)-1].ID != ev.ID {
		t.Fatalf("returned %s, log ends at %s", ev.ID, log[len(log)-1].ID)
	}
}

// Nothing about the session was near the automatic threshold here, which is the
// whole point: asking is not the same as crossing a line.
func TestCompactWorksBelowTheAutomaticThreshold(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{Content: "done", Usage: bigUsage(0.01)},
		{Content: "THE SUMMARY"},
	})
	s := h.primed(t)

	if _, err := h.m.Compact(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	if protocol.Project(s.Events()).CompactedThrough == "" {
		t.Fatal("an explicit compaction must move the compaction point")
	}
}

// compaction_enabled off turns off the *automatic* pass. Asking explicitly is
// the owner overriding their own default, so it is still honoured.
func TestCompactIsAllowedWhenAutomaticCompactionIsOff(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{Content: "done"},
		{Content: "THE SUMMARY"},
	})
	s := h.primed(t)
	if _, err := h.m.SetOption(context.Background(), s.ID(), "compaction_enabled", false); err != nil {
		t.Fatal(err)
	}

	ev, err := h.m.Compact(context.Background(), s.ID())
	if err != nil {
		t.Fatalf("an explicit request must not be refused by the automatic switch: %v", err)
	}
	if protocol.MustData[protocol.CompactionData](ev).Mode != protocol.CompactionSummarize {
		t.Fatalf("event: %+v", ev)
	}
}

func TestCompactRefusesWhileTheSessionIsRunning(t *testing.T) {
	block := make(chan struct{})
	h := newHarness(t, nil, []provider.Response{{Content: "done"}})
	h.fake.BlockOn = block
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	waitForState(t, s, protocol.StateRunning)

	_, err := h.m.Compact(context.Background(), s.ID())
	if rpcCode(err) != protocol.CodeInvalidTransition {
		t.Fatalf("err: %v (code %d)", err, rpcCode(err))
	}
	if !strings.Contains(err.Error(), "interrupt") {
		t.Fatalf("the refusal must say what to do instead: %v", err)
	}

	close(block)
	h.m.WaitIdle(s.ID())
}

// Interrupting is the documented way through, so the pair has to actually work.
func TestCompactSucceedsAfterAnInterrupt(t *testing.T) {
	block := make(chan struct{})
	h := newHarness(t, nil, []provider.Response{
		{Content: "done"},
		{Content: "THE SUMMARY"},
	})
	h.fake.BlockOn = block
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	waitForState(t, s, protocol.StateRunning)

	if err := h.m.Interrupt(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())
	close(block)
	h.fake.BlockOn = nil

	if _, err := h.m.Compact(context.Background(), s.ID()); err != nil {
		t.Fatalf("compacting after an interrupt: %v", err)
	}
}

func TestCompactRefusesAnEndedSession(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "done"}})
	s := h.primed(t)
	if err := h.m.Stop(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}

	_, err := h.m.Compact(context.Background(), s.ID())
	if rpcCode(err) != protocol.CodeInvalidTransition {
		t.Fatalf("err: %v (code %d)", err, rpcCode(err))
	}
}

// The automatic pass treats "too little history" as "not yet" and says nothing.
// A request deserves an answer instead: a control that appears to do nothing is
// indistinguishable from one that is broken.
func TestCompactRefusesWhenThereIsTooLittleHistory(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)

	_, err := h.m.Compact(context.Background(), s.ID())
	if rpcCode(err) != protocol.CodeInvalidParams {
		t.Fatalf("err: %v (code %d)", err, rpcCode(err))
	}
	if h.fake.CallCount() != 0 {
		t.Fatalf("refusing must not cost a model call, made %d", h.fake.CallCount())
	}
}

// Twice in a row, with nothing between: the second has only the first
// compaction behind it, which is not history worth summarising.
func TestCompactTwiceRunningIsRefusedTheSecondTime(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{Content: "done"},
		{Content: "THE SUMMARY"},
	})
	s := h.primed(t)
	if _, err := h.m.Compact(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}

	_, err := h.m.Compact(context.Background(), s.ID())
	if rpcCode(err) != protocol.CodeInvalidParams {
		t.Fatalf("err: %v (code %d)", err, rpcCode(err))
	}
}

// A summariser that fails falls back to stubbing tool results. The reply says
// clear_results rather than claiming the summary it was asked for.
func TestCompactReportsTheFallbackItActuallyTook(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: globCall("c1")},
		{Content: "done"},
	})
	h.m.cfg.Compaction.KeepTurns = 0
	s := h.primed(t)
	h.fake.Errors = map[int]error{h.fake.CallCount(): errors.New("summariser down")}

	ev, err := h.m.Compact(context.Background(), s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if d := protocol.MustData[protocol.CompactionData](ev); d.Mode != protocol.CompactionClearResults {
		t.Fatalf("mode: %s", d.Mode)
	}
}

// waitForState blocks until the session reaches want. Prompt starts the loop in
// a goroutine, so "running" is not true the instant it returns; polling the
// projection is how a test observes a state it is not going to wait out.
func waitForState(t *testing.T, s *session.Session, want protocol.SessionState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.State().State == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("session never reached %s (last: %s)", want, s.State().State)
}
