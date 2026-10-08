package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// logSession is a session whose log the test grows, and whose tool calls the
// fake host records into it.
type logSession struct {
	mu   sync.Mutex
	log  []protocol.Event
	mode protocol.PermissionMode
	dir  string
}

func (s *logSession) ID() string                  { return "S" }
func (s *logSession) Workspace() module.Workspace { return module.Workspace{Path: s.dir, Key: "k"} }
func (s *logSession) Events(*string) ([]protocol.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.Event(nil), s.log...), nil
}
func (s *logSession) State() protocol.State {
	return protocol.State{Options: protocol.Options{PermissionMode: s.mode}}
}
func (s *logSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}
func (s *logSession) add(events ...protocol.Event) {
	s.mu.Lock()
	s.log = append(s.log, events...)
	s.mu.Unlock()
}

// fakeHost calls tools the way the daemon does: the call is logged, the tool
// runs, the result is logged.
type fakeHost struct {
	m     *Module
	calls int
}

func (h *fakeHost) Model() module.Model            { return nil }
func (h *fakeHost) Tools() module.ToolCaller       { return h }
func (h *fakeHost) UI() module.UI                  { return nil }
func (h *fakeHost) DataDir(string) (string, error) { return "", nil }
func (h *fakeHost) Log() *slog.Logger              { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (h *fakeHost) Call(ctx context.Context, s module.Session, tool string, args json.RawMessage) (protocol.ToolResultData, error) {
	h.calls++
	ls := s.(*logSession)
	id := fmt.Sprintf("mod_%d", h.calls)
	ls.add(ev(protocol.EventToolCall, protocol.ToolCallData{CallID: id, Tool: tool, Arguments: args, Source: selfSource}))
	out, err := h.m.runAsk(ctx, s, args)
	res := protocol.ToolResultData{CallID: id, Tool: tool, Status: "ok", Content: out}
	if err != nil {
		res.Status, res.Content = "error", err.Error()
	}
	ls.add(ev(protocol.EventToolResult, res))
	return res, err
}

// rig is the module with a stand-in CLI that answers when the test says.
type rig struct {
	m    *Module
	s    *logSession
	mu   sync.Mutex
	asks []string // prompts the stand-in CLI was run with
	// started counts asks TurnEnd began. The CLI runs in the background, so
	// asks lags it; started does not.
	started int
	answer  chan struct{}
	fail    error
}

func newRig(t *testing.T, cfg module.Config) *rig {
	t.Helper()
	r := &rig{answer: make(chan struct{}, 8), s: &logSession{mode: protocol.PermissionAuto, dir: t.TempDir()}}
	cfg["path"] = "/usr/bin/claude"
	r.m = &Module{}
	if err := r.m.Init(&fakeHost{m: r.m}, cfg); err != nil {
		t.Fatal(err)
	}
	r.m.ask = func(_ context.Context, _, prompt string, _ time.Duration) (string, error) {
		r.mu.Lock()
		r.asks = append(r.asks, prompt)
		r.mu.Unlock()
		<-r.answer
		return "look at SettingsViewModel's init", r.fail
	}
	r.s.add(user("build mission control"))
	return r
}

func (r *rig) inFlight() bool {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	return r.m.waiting["S"] != nil
}

func (r *rig) asked() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.asks)
}

// turnsTo plays model turns, each with a failing build, until the stretch has
// n turns, ending each with the module's TurnEnd.
func (r *rig) turnsTo(n int) {
	log, _ := r.s.Events(nil)
	_, since := stretchOf(log)
	for i := turns(since); i < n; i++ {
		id := fmt.Sprintf("t%d-%d", len(log), i)
		r.s.add(said("trying again"), call(id, "bash", "model", map[string]string{"command": "gradlew test"}),
			result(id, "bash", "ok", "FAILED"))
		before := r.inFlight()
		r.m.TurnEnd(context.Background(), r.s)
		if !before && r.inFlight() {
			r.started++
		}
	}
}

// deliver lets the stand-in answer, waits for the module to have the answer,
// and runs the next request's hook.
func (r *rig) deliver(t *testing.T) {
	t.Helper()
	r.answer <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.m.mu.Lock()
		a := r.m.waiting["S"]
		ready := a != nil && a.done
		r.m.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background ask never finished")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := r.m.BeforeRequest(context.Background(), r.s); err != nil {
		t.Fatal(err)
	}
}

// recorded is the module's own claude.ask calls in the log, each with the
// result that must follow it at once.
func (r *rig) recorded(t *testing.T) (prompts []string, results []protocol.ToolResultData) {
	t.Helper()
	log, _ := r.s.Events(nil)
	for i, e := range log {
		var c protocol.ToolCallData
		if e.Type != protocol.EventToolCall || json.Unmarshal(e.Data, &c) != nil || c.Source != selfSource {
			continue
		}
		if i+1 >= len(log) || log[i+1].Type != protocol.EventToolResult {
			t.Fatalf("the call at %d has no result right after it", i)
		}
		var a struct{ Prompt string }
		_ = json.Unmarshal(c.Arguments, &a)
		var res protocol.ToolResultData
		_ = json.Unmarshal(log[i+1].Data, &res)
		prompts, results = append(prompts, a.Prompt), append(results, res)
	}
	return prompts, results
}

func TestAnAskAtEachMarkAndThenQuiet(t *testing.T) {
	r := newRig(t, module.Config{"auto_ask_after": 100, "auto_ask_max": 2})

	r.turnsTo(99)
	if r.started != 0 {
		t.Fatal("asked before 100 turns")
	}
	r.turnsTo(100)
	if r.started != 1 {
		t.Fatalf("asks at 100 = %d, want 1", r.started)
	}
	r.turnsTo(105)
	if r.started != 1 {
		t.Fatal("a second ask started while one was in flight")
	}
	r.deliver(t)
	prompts, results := r.recorded(t)
	if len(results) != 1 || results[0].Status != "ok" || results[0].Content != "look at SettingsViewModel's init" {
		t.Fatalf("recorded = %+v", results)
	}
	if r.asked() != 1 {
		t.Errorf("recording the answer ran the CLI again: %d asks", r.asked())
	}
	if prompts[0] != r.asks[0] || !strings.Contains(prompts[0], "spent 100 turns") {
		t.Errorf("the recorded prompt is not the one asked:\n%s", prompts[0])
	}

	r.turnsTo(199)
	if r.started != 1 {
		t.Fatal("asked again before 200")
	}
	r.turnsTo(200)
	if r.started != 2 {
		t.Fatalf("asks at 200 = %d, want 2", r.started)
	}
	r.deliver(t)
	r.turnsTo(300)
	if _, results := r.recorded(t); r.started != 2 || r.asked() != 2 || len(results) != 2 {
		t.Errorf("past auto_ask_max: %d asks, %d recorded", r.started, len(results))
	}
}

func TestWhenItNeverAsks(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  module.Config
		mode protocol.PermissionMode
	}{
		"turned off":           {module.Config{"auto_ask_after": 0}, protocol.PermissionAuto},
		"no asks allowed":      {module.Config{"auto_ask_after": 3, "auto_ask_max": 0}, protocol.PermissionAuto},
		"a person approves":    {module.Config{"auto_ask_after": 3}, protocol.PermissionAsk},
		"no mode means asking": {module.Config{"auto_ask_after": 3}, ""},
		"module disabled":      {module.Config{"auto_ask_after": 3, "enabled": false}, protocol.PermissionAuto},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, tc.cfg)
			r.s.mode = tc.mode
			r.turnsTo(10)
			if r.started != 0 {
				t.Errorf("asked %d times", r.started)
			}
		})
	}
}

func TestAPersonSpeakingDropsTheAnswer(t *testing.T) {
	r := newRig(t, module.Config{"auto_ask_after": 3})
	r.turnsTo(3)
	r.s.add(user("try kotlinx-coroutines-test"))
	r.deliver(t)
	if _, results := r.recorded(t); len(results) != 0 {
		t.Fatalf("an answer for a stretch that ended was recorded: %+v", results)
	}
	r.turnsTo(2)
	if r.started != 1 {
		t.Fatalf("the count should start again, asked %d", r.started)
	}
	r.turnsTo(3)
	if r.started != 2 {
		t.Errorf("the new stretch's mark should ask, asked %d", r.started)
	}
}

func TestAFailedAskIsRecordedAsOne(t *testing.T) {
	r := newRig(t, module.Config{"auto_ask_after": 3})
	r.fail = module.Fail(protocol.ToolErrorTimeout, "claude did not answer within 5m0s")
	r.turnsTo(3)
	r.deliver(t)
	_, results := r.recorded(t)
	if len(results) != 1 || results[0].Status != "error" || !strings.Contains(results[0].Content, "did not answer") {
		t.Errorf("recorded = %+v", results)
	}
}

// After a restart nothing is in memory: what was asked is read from the log.
func TestAMarkInTheLogIsNotAskedAgain(t *testing.T) {
	r := newRig(t, module.Config{"auto_ask_after": 3})
	r.turnsTo(3)
	r.deliver(t)

	again := &Module{}
	if err := again.Init(&fakeHost{m: again}, module.Config{"path": "/usr/bin/claude", "auto_ask_after": 3}); err != nil {
		t.Fatal(err)
	}
	ran := 0
	again.ask = func(context.Context, string, string, time.Duration) (string, error) { ran++; return "", nil }
	again.TurnEnd(context.Background(), r.s)
	if again.waiting["S"] != nil || ran != 0 {
		t.Error("a mark already answered in the log was asked again")
	}
}

func TestTheEndOfTheSessionDropsTheAsk(t *testing.T) {
	r := newRig(t, module.Config{"auto_ask_after": 3})
	r.turnsTo(3)
	r.m.SessionEnd(context.Background(), r.s)
	r.answer <- struct{}{}
	if _, err := r.m.BeforeRequest(context.Background(), r.s); err != nil {
		t.Fatal(err)
	}
	if _, results := r.recorded(t); len(results) != 0 {
		t.Errorf("an ask for an ended session was recorded: %+v", results)
	}
}

func TestTheModelsOwnAskStillRuns(t *testing.T) {
	r := newRig(t, module.Config{"auto_ask_after": 3})
	r.answer <- struct{}{}
	out, err := r.m.runAsk(context.Background(), r.s, json.RawMessage(`{"prompt":"is this right?"}`))
	if err != nil || out != "look at SettingsViewModel's init" || r.asked() != 1 {
		t.Errorf("the model's ask = %q, %v, ran %d", out, err, r.asked())
	}
}
