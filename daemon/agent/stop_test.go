package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// vetoTimes vetoes the first n stop attempts, then allows.
type vetoTimes struct {
	n      int
	reason string
	seen   int
	infos  []module.StopInfo // one per stop-gate call, in order
}

func (v *vetoTimes) Name() string                          { return "vetoer" }
func (v *vetoTimes) Init(module.Host, module.Config) error { return nil }
func (v *vetoTimes) BeforeStop(_ context.Context, _ module.Session, info module.StopInfo) module.StopVerdict {
	v.seen++
	v.infos = append(v.infos, info)
	if v.seen <= v.n {
		return module.StopVerdict{Allow: false, Reason: v.reason}
	}
	return module.StopVerdict{Allow: true}
}

func TestVetoFeedsTheReasonBackAndTheLoopContinues(t *testing.T) {
	v := &vetoTimes{n: 1, reason: "working tree is dirty"}
	h := newHarness(t, []module.Module{v}, []provider.Response{
		{Content: "All done."},
		{Content: "Committed; done now."},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "do it")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StateIdle || st.Turns != 2 {
		t.Fatalf("state=%s turns=%d", st.State, st.Turns)
	}
	var vetoes int
	for _, e := range s.Events() {
		if e.Type == protocol.EventStopVeto {
			vetoes++
			if protocol.MustData[protocol.StopVetoData](e).Module != "vetoer" {
				t.Fatal("veto must name the module")
			}
		}
	}
	if vetoes != 1 {
		t.Fatalf("veto events: %d", vetoes)
	}
	tail := h.fake.Calls[1].Messages[len(h.fake.Calls[1].Messages)-1]
	if tail.Role != "user" || !strings.Contains(tail.Content, "working tree is dirty") {
		t.Fatalf("veto not fed back: %+v", tail)
	}
	// The first gate call sees the first assistant turn and no prior vetoes.
	if len(v.infos) != 2 {
		t.Fatalf("gate calls: %d", len(v.infos))
	}
	if v.infos[0].LastAssistantMessage != "All done." || v.infos[0].TurnsSinceUser != 1 || v.infos[0].VetoCount != 0 {
		t.Fatalf("first stop info: %+v", v.infos[0])
	}
	// The second sees the newer message and the round it already vetoed.
	if v.infos[1].LastAssistantMessage != "Committed; done now." || v.infos[1].TurnsSinceUser != 2 || v.infos[1].VetoCount != 1 {
		t.Fatalf("second stop info: %+v", v.infos[1])
	}
}

func TestRepeatedIdenticalVetoesBlockTheSession(t *testing.T) {
	v := &vetoTimes{n: 99, reason: "tests still fail"}
	h := newHarness(t, []module.Module{v}, []provider.Response{
		{Content: "done"}, {Content: "done"}, {Content: "done"}, {Content: "done"}, {Content: "done"},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "fix it")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StateBlocked {
		t.Fatalf("state: %s", st.State)
	}
	if st.Turns != 3 {
		t.Fatalf("the progress detector must stop after 3 identical rounds, got %d", st.Turns)
	}
	ev := s.Events()
	if lastReport(t, ev).ExitStatus != protocol.StateBlocked {
		t.Fatal("report must say blocked")
	}
}

func TestToolCallsResetTheProgressDetector(t *testing.T) {
	v := &vetoTimes{n: 99, reason: "not yet"}
	h := newHarness(t, []module.Module{v}, []provider.Response{
		{Content: "done"},
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}},
		{Content: "done"},
		{Content: "done"},
		{Content: "done"},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())
	if s.State().State != protocol.StateBlocked {
		t.Fatalf("state: %s", s.State().State)
	}
	// Turn 1 veto, turn 2 tools (reset), turns 3-5 vetoes → blocked on turn 5.
	if s.State().Turns != 5 {
		t.Fatalf("turns: %d", s.State().Turns)
	}
}

func TestBudgetPausesBeforeTheNextTurn(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}},
		{Content: "reached only after Resume raises the budget"},
	})
	s := h.create(t)
	if _, err := h.m.SetBudget(context.Background(), s.ID(), protocol.BudgetData{MaxTurns: 1, Source: "client"}); err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StatePaused || st.Turns != 1 {
		t.Fatalf("state=%s turns=%d", st.State, st.Turns)
	}
	if h.fake.CallCount() != 1 {
		t.Fatalf("budget must stop the loop before the second call, got %d", h.fake.CallCount())
	}
	ev := s.Events()
	if lastReport(t, ev).ExitStatus != protocol.StatePaused {
		t.Fatal("report must say paused")
	}
	if err := h.m.Resume(context.Background(), s.ID(), &protocol.BudgetData{MaxTurns: 5, Source: "client"}); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())
	if s.State().State != protocol.StateIdle {
		t.Fatalf("after resume: %s", s.State().State)
	}
}

func TestVetoesAreClearedByTheNextTurn(t *testing.T) {
	v := &vetoTimes{n: 1, reason: "once"}
	h := newHarness(t, []module.Module{v}, []provider.Response{{Content: "a"}, {Content: "b"}})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())
	if got := protocol.OutstandingVetoes(s.Events()); len(got) != 0 {
		t.Fatalf("outstanding: %+v", got)
	}
}
