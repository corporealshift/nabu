package agent

import (
	"context"
	"testing"

	"github.com/corporealshift/rho/daemon/provider"
	"github.com/corporealshift/rho/protocol"
)

// A session's effort reaches every request of its own, a change reaches the
// next one, and a summary never carries it
// (docs/specs/2026-10-16-session-effort-design.md).
func TestEffortReachesTheSessionsRequests(t *testing.T) {
	h := newHarnessWithProvider(t, provider.Config{Name: "fake", ContextWindow: 8000},
		[]provider.Response{
			{ToolCalls: globCall("c1"), Usage: bigUsage(0.10)},
			{Content: "done", Usage: bigUsage(0.10)},
			{ToolCalls: globCall("c2"), Usage: bigUsage(0.90)},
			{Content: "the summary"},
			{Content: "ok", Usage: bigUsage(0.10)},
		})
	s, err := h.m.Create(context.Background(), h.dir, CreateOptions{Effort: protocol.EffortLow})
	if err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())
	if _, err := h.m.SetOption(context.Background(), s.ID(), "effort", protocol.EffortXHigh); err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "more")
	h.m.WaitIdle(s.ID())

	want := []string{"low", "low", "xhigh", "", "xhigh"}
	if len(h.fake.Calls) != len(want) {
		t.Fatalf("calls: %d, want %d", len(h.fake.Calls), len(want))
	}
	for i, c := range h.fake.Calls {
		if c.Effort != want[i] {
			t.Errorf("call %d: effort %q, want %q", i, c.Effort, want[i])
		}
	}
}

// A session created without an effort sends none, and "" clears one.
func TestEffortUnsetAndCleared(t *testing.T) {
	h := newHarnessWithProvider(t, provider.Config{Name: "fake", ContextWindow: 8000},
		[]provider.Response{{Content: "a"}, {Content: "b"}})
	s := h.create(t)
	if e := s.State().Options.Effort; e != "" {
		t.Fatalf("effort = %q, want unset", e)
	}
	if _, err := h.m.SetOption(context.Background(), s.ID(), "effort", protocol.EffortMedium); err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())
	if _, err := h.m.SetOption(context.Background(), s.ID(), "effort", ""); err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "more")
	h.m.WaitIdle(s.ID())
	if got := []string{h.fake.Calls[0].Effort, h.fake.Calls[1].Effort}; got[0] != "medium" || got[1] != "" {
		t.Errorf("efforts = %q, want [medium \"\"]", got)
	}
}
